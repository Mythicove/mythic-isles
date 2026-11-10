package org.mythicove.isles;

import org.geysermc.floodgate.api.FloodgateApi;
import java.util.Map;
import java.util.UUID;

final class FloodgateHook {
    Map<String, Object> connection(UUID uuid) {
        var player = FloodgateApi.getInstance().getPlayer(uuid);
        if (player == null || !uuid.equals(player.getCorrectUniqueId())) return null;
        // In linked mode the Bukkit UUID is the linked Java account UUID, not
        // the generated Floodgate UUID. Keep binding and membership on Bukkit's
        // actual identity; never infer Bedrock from a prefix or UUID layout.
        return Map.of("edition", "bedrock", "provider", "floodgate", "version", player.getVersion(), "linkedJavaAccount", player.isLinked());
    }
}
