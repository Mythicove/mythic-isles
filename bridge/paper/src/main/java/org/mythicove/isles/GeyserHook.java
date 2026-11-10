package org.mythicove.isles;

import org.geysermc.geyser.api.GeyserApi;
import java.util.Map;
import java.util.UUID;

/** Local Geyser API observations; no addresses, Xbox IDs or session tokens. */
final class GeyserHook {
    Map<String, Object> versions() {
        var api = GeyserApi.api();
        return Map.of("javaVersion", api.supportedJavaVersion().versionString(), "bedrockVersions",
            api.supportedBedrockVersions().stream().map(v -> v.versionString()).distinct().toList());
    }

    Map<String, Object> connection(UUID uuid) {
        var connection = GeyserApi.api().connectionByUuid(uuid);
        if (connection == null) return null;
        return Map.of("edition", "bedrock", "provider", "geyser", "version", connection.version(), "linkedJavaAccount", connection.isLinked());
    }
}
