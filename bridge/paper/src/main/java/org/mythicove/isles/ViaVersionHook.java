package org.mythicove.isles;

import com.viaversion.viaversion.api.Via;
import com.viaversion.viaversion.api.protocol.version.ProtocolVersion;
import java.util.List;
import java.util.UUID;

final class ViaVersionHook {
    ViaVersionHook() {
        if (Via.getAPI().majorVersion() != 5) throw new IllegalStateException("Unsupported ViaVersion API major");
    }
    String playerVersion(UUID uuid) {
        var version = Via.getAPI().getPlayerProtocolVersion(uuid);
        return version == ProtocolVersion.unknown ? "" : version.getName();
    }
    List<String> supportedVersions() {
        // This API excludes configured blocked versions. Protocol support still
        // does not prove modpack, proxy or authentication compatibility.
        com.viaversion.viaversion.api.ViaAPI<?> api = Via.getAPI();
        return api.getSupportedProtocolVersions().stream().map(ProtocolVersion::getName).distinct().limit(256).toList();
    }
}
