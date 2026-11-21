package org.mythicove.test;

import com.google.gson.Gson;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.LinkedHashMap;
import java.util.Map;
import org.bukkit.event.EventHandler;
import org.bukkit.event.Listener;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.plugin.java.JavaPlugin;

/** Native, read-only observation for the isolated online authentication scenario. */
public final class IdentityObserver extends JavaPlugin implements Listener {
    @Override
    public void onEnable() {
        getServer().getPluginManager().registerEvents(this, this);
        getServer().getScheduler().runTask(this, () -> write("server-ready.json",
                Map.of("onlineMode", getServer().getOnlineMode(), "serverVersion", getServer().getVersion())));
    }

    @EventHandler
    public void joined(PlayerJoinEvent event) {
        var player = event.getPlayer();
        var profile = player.getPlayerProfile();
        var textures = profile.getTextures();
        var result = new LinkedHashMap<String, Object>();
        result.put("name", player.getName());
        result.put("uuid", player.getUniqueId().toString());
        result.put("onlineMode", getServer().getOnlineMode());
        result.put("skinUrl", textures.getSkin() == null ? "" : textures.getSkin().toString());
        result.put("skinModel", textures.getSkinModel().name());
        result.put("skinSigned", textures.isSigned());
        write("server-player.json", result);
    }

    private void write(String name, Map<String, ?> observation) {
        Path destination = Path.of(name);
        Path temporary = Path.of(name + ".part");
        try {
            Files.writeString(temporary, new Gson().toJson(observation));
            Files.move(temporary, destination, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
        } catch (IOException error) {
            throw new IllegalStateException("Could not retain native player observation", error);
        }
    }
}
