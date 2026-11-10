package org.mythicove.isles;

import com.google.gson.Gson;
import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import org.bukkit.Bukkit;
import org.bukkit.command.Command;
import org.bukkit.command.CommandSender;
import org.bukkit.entity.Player;
import org.bukkit.plugin.java.JavaPlugin;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/** Paper/Spigot bridge. Network work never runs on the tick thread. */
public final class IslesBridge extends JavaPlugin {
    private final Gson json = new Gson();
    private final AtomicBoolean observing = new AtomicBoolean();
    private final Set<UUID> linking = ConcurrentHashMap.newKeySet();
    private final ExecutorService network = Executors.newSingleThreadExecutor();
    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
    private URI base;
    private String token;
    private LuckPermsHook luckPerms;
    private PlaceholderHook placeholders;
    private SparkHook spark;
    private ViaVersionHook viaVersion;
    private WorldGuardHook worldGuard;
    private VaultHook vault;
    private ChunkyHook chunky;
    private PlanHook plan;
    private TownyHook towny;

    @Override public void onEnable() {
        saveDefaultConfig();
        String url = getConfig().getString("url", "").replaceAll("/+$", "");
        String server = getConfig().getString("server", "");
        token = getConfig().getString("token", "");
        try {
            URI origin = URI.create(url);
            if (!List.of("http", "https").contains(origin.getScheme()) || origin.getHost() == null || origin.getUserInfo() != null || origin.getQuery() != null || origin.getFragment() != null || !server.matches("[a-z0-9][a-z0-9-]{0,63}") || token.length() < 32) throw new IllegalArgumentException();
            base = URI.create(url + "/api/isles/bridge/" + server + "/");
        } catch (IllegalArgumentException e) {
            getLogger().severe("Configure url, server and its Bridge token, then restart.");
            Bukkit.getPluginManager().disablePlugin(this);
            return;
        }
        if (enabled("LuckPerms")) luckPerms = new LuckPermsHook();
        if (enabled("PlaceholderAPI")) placeholders = new PlaceholderHook();
        if (enabled("ViaVersion")) viaVersion = new ViaVersionHook();
        if (enabled("WorldGuard")) worldGuard = new WorldGuardHook();
        if (enabled("Vault")) vault = new VaultHook();
        if (enabled("Chunky")) chunky = new ChunkyHook();
        if (enabled("Plan")) plan = new PlanHook();
        if (enabled("Towny")) towny = new TownyHook();
        Bukkit.getScheduler().runTaskTimer(this, this::observe, 20L, 400L);
        getLogger().info("Bridge enabled. Optional data providers are detected from installed plugins.");
    }

    private boolean enabled(String name) { return Bukkit.getPluginManager().isPluginEnabled(name); }

    @Override public void onDisable() {
        if (chunky != null) chunky.close();
        Bukkit.getScheduler().cancelTasks(this);
        network.shutdownNow();
        http.shutdownNow();
    }

    private void observe() {
        if (!observing.compareAndSet(false, true)) return;
        try {
            if (spark == null) {
                try { spark = new SparkHook(); } catch (NoClassDefFoundError | IllegalStateException unavailable) { }
            }
            Map<String, Object> snapshot = new LinkedHashMap<>();
            snapshot.put("platform", Bukkit.getName());
            snapshot.put("minecraft", Bukkit.getBukkitVersion());
            Map<String, String> plugins = new LinkedHashMap<>();
            for (String name : List.of("LuckPerms", "PlaceholderAPI", "spark", "ViaVersion", "WorldGuard", "Vault", "Chunky", "Plan", "Towny")) {
                var plugin = Bukkit.getPluginManager().getPlugin(name);
                if (plugin != null && plugin.isEnabled()) plugins.put(name, plugin.getDescription().getVersion());
            }
            snapshot.put("plugins", plugins);
            if (spark != null) plugins.putIfAbsent("spark", "server-service");
            snapshot.put("groups", luckPerms == null ? List.of() : luckPerms.groups());
            if (spark != null) snapshot.putAll(spark.statistics());
            if (viaVersion != null) snapshot.put("protocolVersions", viaVersion.supportedVersions());
            if (chunky != null) snapshot.put("chunky", chunky.worlds());
            var templates = getConfig().getConfigurationSection("placeholders");
            List<Map<String, Object>> players = Bukkit.getOnlinePlayers().stream().limit(2000).map(p -> {
                Map<String, Object> value = new LinkedHashMap<>();
                value.put("uuid", p.getUniqueId().toString()); value.put("name", p.getName());
                if (luckPerms != null) { value.put("group", luckPerms.primaryGroup(p.getUniqueId())); value.put("groups", luckPerms.playerGroups(p.getUniqueId())); }
                if (viaVersion != null) value.put("clientVersion", viaVersion.playerVersion(p.getUniqueId()));
                if (worldGuard != null) { var result = worldGuard.regions(p); value.put("regions", result.regions()); value.put("regionsTruncated", result.truncated()); }
                if (vault != null) { var balance = vault.balance(p); if (balance != null) value.put("economy", balance); }
                if (placeholders != null && templates != null) {
                    Map<String, String> values = new LinkedHashMap<>();
                    templates.getKeys(false).stream().limit(40).forEach(key -> {
                        String template = templates.getString(key, "");
                        String result = placeholders.resolve(p, template);
                        // An unresolved placeholder is unavailable, not a real value.
                        if (!result.equals(template) || !template.contains("%")) values.put(key, result.substring(0, Math.min(2000, result.length())));
                    });
                    value.put("values", values);
                }
                return value;
            }).toList();
            snapshot.put("players", players);
            network.submit(() -> {
                try {
                    if (plan != null || towny != null) {
                        try {
                            var identities = JsonParser.parseString(send("identities", "GET", null)).getAsJsonArray();
                            List<String> uuids = new java.util.ArrayList<>();
                            for (var identity : identities) uuids.add(identity.getAsString());
                            if (plan != null) {
                                try { snapshot.put("plan", plan.collect(uuids)); }
                                catch (Exception unavailable) { getLogger().warning("Plan statistics unavailable; check its database and Query API."); }
                            }
                            if (towny != null) {
                                var collection = Bukkit.getScheduler().callSyncMethod(this, () -> towny.collect(uuids));
                                try { snapshot.put("towny", collection.get(10, java.util.concurrent.TimeUnit.SECONDS)); }
                                catch (Exception unavailable) { collection.cancel(false); getLogger().warning("Towny data unavailable; check the provider on the game server."); }
                            }
                        } catch (Exception unavailable) {
                            getLogger().warning("Verified identity list unavailable; private provider observations omitted.");
                        }
                    }
                    send("heartbeat", "POST", json.toJson(snapshot));
                    JsonArray tasks = JsonParser.parseString(send("tasks", "GET", null)).getAsJsonArray();
                    for (var element : tasks) executeTask(element.getAsJsonObject());
                } catch (Exception e) {
                    getLogger().warning("Isles unavailable or rejected the observation; check connection and token.");
                } finally { observing.set(false); }
            });
        } catch (Exception e) {
            observing.set(false);
            getLogger().warning("Could not collect optional provider data: " + e.getClass().getSimpleName());
        }
    }

    private String send(String path, String method, String body) throws Exception {
        HttpRequest request = HttpRequest.newBuilder(base.resolve(path))
            .timeout(Duration.ofSeconds(10)).header("Authorization", "Bearer " + token)
            .header("Content-Type", "application/json")
            .method(method, body == null ? HttpRequest.BodyPublishers.noBody() : HttpRequest.BodyPublishers.ofString(body)).build();
        HttpResponse<String> response = http.send(request, HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() < 200 || response.statusCode() >= 300) throw new IllegalStateException("HTTP " + response.statusCode());
        if (response.body().length() > 1_000_000) throw new IllegalStateException("Response too large");
        return response.body();
    }

    @Override public boolean onCommand(CommandSender sender, Command command, String label, String[] args) {
        if (!(sender instanceof Player player)) { sender.sendMessage("Run this command in game."); return true; }
        if (args.length != 2 || !args[0].equalsIgnoreCase("link") || !args[1].matches("[a-fA-F0-9]{12}")) return false;
        UUID uuid = player.getUniqueId();
        if (!linking.add(uuid)) { player.sendMessage("A linking request is already in progress."); return true; }
        String body = json.toJson(Map.of("code", args[1], "uuid", uuid.toString(), "name", player.getName(), "platform", "java"));
        player.sendMessage("Verifying your website linking code…");
        network.submit(() -> {
            boolean success;
            try { send("link", "POST", body); success = true; } catch (Exception e) { success = false; }
            boolean linked = success;
            linking.remove(uuid);
            if (isEnabled()) Bukkit.getScheduler().runTask(this, () -> {
                Player current = Bukkit.getPlayer(uuid);
                if (current != null) current.sendMessage(linked ? "Game identity linked. Return to the website to see your profile." : "Linking failed. Generate a new code and check the server connection.");
            });
        });
        return true;
    }

    private void executeTask(JsonObject task) throws Exception {
        String id = task.get("id").getAsString();
        if (task.get("kind").getAsString().equals("chunky")) {
            // Claim is durably persisted by Isles before any provider call. A
            // lost claim/result response leaves dispatched, never auto-replayed.
            send("tasks/" + id + "/claim", "POST", "{}");
            boolean accepted = false;
            String error = "Chunky rejected operation; refresh world state";
            var execution = Bukkit.getScheduler().callSyncMethod(this, () -> {
                if (chunky == null) throw new IllegalStateException("Chunky unavailable");
                return chunky.execute(task.getAsJsonObject("chunky"));
            });
            try {
                accepted = execution.get(10, java.util.concurrent.TimeUnit.SECONDS);
            } catch (java.util.concurrent.ExecutionException e) {
                error = "Chunky rejected operation: " + e.getCause().getClass().getSimpleName();
            } catch (java.util.concurrent.TimeoutException e) {
                execution.cancel(false);
                throw e; // Execution may have started: leave outcome uncertain.
            }
            send("tasks/" + id, "POST", json.toJson(Map.of("success", accepted, "error", accepted ? "" : error)));
            return;
        }
        boolean success = false;
        String error = "";
        if (!success) {
            try {
                String kind = task.get("kind").getAsString();
                UUID uuid = UUID.fromString(task.get("uuid").getAsString());
                boolean grant = task.get("grant").getAsBoolean();
                if (kind.equals("whitelist")) {
                    CompletableFuture<Void> done = new CompletableFuture<>();
                    Bukkit.getScheduler().runTask(this, () -> { try { Bukkit.getOfflinePlayer(uuid).setWhitelisted(grant); done.complete(null); } catch (Exception e) { done.completeExceptionally(e); } });
                    done.get(10, java.util.concurrent.TimeUnit.SECONDS);
                } else if (kind.equals("member") && luckPerms != null) {
                    luckPerms.setGroup(uuid, task.get("group").getAsString(), grant).get(10, java.util.concurrent.TimeUnit.SECONDS);
                } else { throw new IllegalStateException("Provider unavailable"); }
                // Both operations set desired state. A lost acknowledgement can
                // replay safely without a second, in-memory deduplication store.
                success = true;
            } catch (Exception e) { error = "Provider rejected operation: " + e.getClass().getSimpleName(); }
        }
        send("tasks/" + id, "POST", json.toJson(Map.of("success", success, "error", error)));
    }
}
