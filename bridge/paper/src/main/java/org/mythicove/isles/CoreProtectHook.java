package org.mythicove.isles;

import com.google.gson.JsonObject;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.Map;
import net.coreprotect.CoreProtect;
import net.coreprotect.CoreProtectAPI;
import net.coreprotect.api.LookupOptions;
import org.bukkit.Bukkit;
import org.bukkit.block.Block;

/** API v12 exact-block history. Database work runs on the existing network worker. */
final class CoreProtectHook {
    private final CoreProtectAPI api = ((CoreProtect) Bukkit.getPluginManager().getPlugin("CoreProtect")).getAPI();

    Block block(JsonObject query) {
        if (!api.isEnabled() || api.APIVersion() < 12) throw new IllegalStateException("CoreProtect API v12 required");
        var world = Bukkit.getWorld(query.get("world").getAsString());
        if (world == null) throw new IllegalArgumentException("World is not loaded");
        int y = query.get("y").getAsInt();
        if (y < world.getMinHeight() || y >= world.getMaxHeight()) throw new IllegalArgumentException("Coordinate outside world height");
        return world.getBlockAt(query.get("x").getAsInt(), y, query.get("z").getAsInt());
    }

    Map<String, Object> lookup(Block block, JsonObject query) {
        if (Bukkit.isPrimaryThread()) throw new IllegalStateException("Database lookup requires asynchronous thread");
        if (!api.isEnabled()) throw new IllegalStateException("CoreProtect API disabled");
        var rows = api.blockLookup(block, LookupOptions.builder().time(query.get("seconds").getAsInt()).limit(query.get("offset").getAsInt(), 101).build());
        if (rows == null) throw new IllegalStateException("CoreProtect lookup unavailable");
        var records = new ArrayList<Map<String, Object>>();
        for (var row : rows.stream().limit(100).toList()) {
            Map<String, Object> value = new LinkedHashMap<>();
            value.put("player", row.getPlayer());
            value.put("timestampMs", row.getTimestamp());
            value.put("action", row.getActionString());
            if (row.getType() != null) value.put("material", row.getType().getKey().toString());
            value.put("rolledBack", row.isRolledBack());
            records.add(value);
        }
        return Map.of("records", records, "hasMore", rows.size() > 100);
    }
}
