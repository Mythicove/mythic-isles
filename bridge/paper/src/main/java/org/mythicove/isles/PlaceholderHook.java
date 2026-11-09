package org.mythicove.isles;
import me.clip.placeholderapi.PlaceholderAPI;
import org.bukkit.entity.Player;

final class PlaceholderHook {
    String resolve(Player player, String template) { return PlaceholderAPI.setPlaceholders(player, template); }
}
