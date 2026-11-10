package org.mythicove.isles;

import de.maxhenkel.voicechat.api.BukkitVoicechatService;
import de.maxhenkel.voicechat.api.VoicechatPlugin;
import de.maxhenkel.voicechat.api.VoicechatServerApi;
import de.maxhenkel.voicechat.api.events.EventRegistration;
import de.maxhenkel.voicechat.api.events.VoicechatServerStartedEvent;
import de.maxhenkel.voicechat.api.events.VoicechatServerStoppedEvent;
import org.bukkit.Bukkit;

import java.util.Map;
import java.util.UUID;

/** Read-only provider observations. Never retains a connection beyond this call. */
final class VoicechatHook implements VoicechatPlugin {
    private volatile VoicechatServerApi api;

    VoicechatHook() {
        var service = Bukkit.getServicesManager().load(BukkitVoicechatService.class);
        if (service == null) throw new IllegalStateException("Voice chat service unavailable");
        service.registerPlugin(this);
    }

    @Override public String getPluginId() { return "mythicisles"; }

    @Override public void registerEvents(EventRegistration events) {
        events.registerEvent(VoicechatServerStartedEvent.class, event -> api = event.getVoicechat());
        events.registerEvent(VoicechatServerStoppedEvent.class, event -> api = null);
    }

    Map<String, Boolean> player(UUID uuid) {
        var server = api;
        if (server == null) return null;
        var connection = server.getConnectionOf(uuid);
        if (connection == null) return null;
        return Map.of("installed", connection.isInstalled(), "connected", connection.isConnected(),
            "disabled", connection.isDisabled());
    }
}
