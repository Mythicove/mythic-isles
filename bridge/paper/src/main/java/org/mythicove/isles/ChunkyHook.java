package org.mythicove.isles;

import com.google.gson.JsonObject;
import org.popcraft.chunky.Chunky;
import org.popcraft.chunky.ChunkyProvider;
import org.popcraft.chunky.GenerationTask;
import org.popcraft.chunky.event.task.GenerationTaskUpdateEvent;
import org.popcraft.chunky.event.task.GenerationTaskFinishEvent;
import org.popcraft.chunky.platform.World;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.Consumer;

/** Chunky 1.4 common API supplies saved tasks as well as running ones. */
final class ChunkyHook {
    private final Chunky chunky = ChunkyProvider.get();
    // Chunky 1.4 can save/remove a task before async chunk callbacks finish.
    // Retain the provider object published by its lifecycle events, rather than
    // mistaking that earlier disk checkpoint for a final completion result.
    private record Observation(GenerationTask task, boolean cancelled) { }
    private final Map<String, Observation> observed = new ConcurrentHashMap<>();
    private final Consumer<GenerationTaskUpdateEvent> update = event -> remember(event.generationTask());
    private final Consumer<GenerationTaskFinishEvent> finish = event -> remember(event.generationTask());

    ChunkyHook() {
        chunky.getEventBus().subscribe(GenerationTaskUpdateEvent.class, update);
        chunky.getEventBus().subscribe(GenerationTaskFinishEvent.class, finish);
    }

    private void remember(GenerationTask task) {
        observed.compute(task.getSelection().world().getName(), (world, previous) ->
            new Observation(task, previous != null && previous.task() == task && previous.cancelled()));
    }

    private void rememberCancellation(GenerationTask task) {
        observed.put(task.getSelection().world().getName(), new Observation(task, true));
    }

    void close() {
        chunky.getEventBus().unsubscribe(GenerationTaskUpdateEvent.class, update);
        chunky.getEventBus().unsubscribe(GenerationTaskFinishEvent.class, finish);
    }

    List<Map<String, Object>> worlds() {
        return chunky.getServer().getWorlds().stream().limit(100).map(world -> {
            Map<String, Object> value = new LinkedHashMap<>();
            value.put("world", world.getName());
            GenerationTask active = chunky.getGenerationTasks().get(world.getName());
            GenerationTask saved = chunky.getTaskLoader().loadTask(world).orElse(null);
            Observation observation = observed.get(world.getName());
            GenerationTask recent = observation == null ? null : observation.task();
            boolean matching = recent != null && saved != null && sameCheckpoint(recent, saved);
            GenerationTask task = active != null ? active : matching ? recent : saved;
            if (task == null) { value.put("state", "idle"); return value; }
            long total = task.getChunkIterator().total();
            long count = task.getCount();
            value.put("state", active != null ? "running" : !task.isCancelled() ? "paused" : count >= total ? "completed" : matching && observation.cancelled() ? "cancelled" : "stopped");
            var selection = task.getSelection();
            value.put("selection", Map.of("shape", selection.shape(), "centerX", selection.centerX(), "centerZ", selection.centerZ(), "radiusX", selection.radiusX(), "radiusZ", selection.radiusZ()));
            Map<String, Object> progress = new LinkedHashMap<>();
            progress.put("chunks", count);
            progress.put("total", total);
            progress.put("percent", total > 0 ? Math.min(100d, 100d * count / total) : 0d);
            var observed = task.getProgress();
            // Chunky's time fields are ETA while running, elapsed time on
            // completion. Never present completed/paused elapsed time as ETA.
            if (active != null && !observed.isComplete() && Double.isFinite(observed.getRate()) && observed.getRate() > 0) {
                progress.put("rate", observed.getRate());
                progress.put("etaSeconds", observed.getHours() * 3600 + observed.getMinutes() * 60 + observed.getSeconds());
            }
            value.put("progress", progress);
            return value;
        }).toList();
    }

    private boolean sameCheckpoint(GenerationTask recent, GenerationTask saved) {
        var a = recent.getSelection();
        var b = saved.getSelection();
        return recent.isCancelled() == saved.isCancelled() && a.world().getUUID().equals(b.world().getUUID())
            && a.shape().equals(b.shape()) && recent.getChunkIterator().name().equals(saved.getChunkIterator().name())
            && a.centerX() == b.centerX() && a.centerZ() == b.centerZ() && a.radiusX() == b.radiusX() && a.radiusZ() == b.radiusZ();
    }

    boolean execute(JsonObject operation) {
        String name = operation.get("world").getAsString();
        World world = chunky.getServer().getWorld(name).orElseThrow(() -> new IllegalStateException("World is no longer loaded"));
        String action = operation.get("action").getAsString();
        GenerationTask saved = chunky.getTaskLoader().loadTask(world).orElse(null);
        boolean running = chunky.getApi().isRunning(name);
        return switch (action) {
            case "start" -> {
                // Do not overwrite a paused task, including a task created by a
                // console operator after the last heartbeat.
                if (running || (saved != null && !saved.isCancelled())) yield false;
                double radius = operation.get("radius").getAsDouble();
                if (radius > chunky.getLimit()) throw new IllegalStateException("Radius exceeds the server's Chunky limit");
                yield chunky.getApi().startTask(name, operation.get("shape").getAsString(), operation.get("centerX").getAsDouble(), operation.get("centerZ").getAsDouble(), radius, radius, "concentric");
            }
            case "pause" -> chunky.getApi().pauseTask(name);
            case "continue" -> chunky.getApi().continueTask(name);
            case "cancel" -> {
                if (running) {
                    GenerationTask task = chunky.getGenerationTasks().get(name);
                    boolean accepted = chunky.getApi().cancelTask(name);
                    if (accepted && task != null) rememberCancellation(task);
                    yield accepted;
                }
                if (saved == null || saved.isCancelled()) yield false;
                // The public v0 facade only cancels running tasks. The common
                // loader also persists cancellation of paused tasks.
                chunky.getTaskLoader().cancelTask(world);
                chunky.getTaskLoader().loadTask(world).ifPresent(this::rememberCancellation);
                yield true;
            }
            default -> throw new IllegalArgumentException("Unsupported Chunky action");
        };
    }
}
