package org.mythicove.isles;

import com.sk89q.worldedit.bukkit.BukkitAdapter;
import com.sk89q.worldguard.WorldGuard;
import com.sk89q.worldguard.bukkit.WorldGuardPlugin;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

final class WorldGuardHook {
    record Region(String id, String world, String role, int centerX, int centerY, int centerZ) {}
    record Result(List<Region> regions, boolean truncated) {}
    Result regions(Player player) {
        var local = WorldGuardPlugin.inst().wrapPlayer(player);
        var container = WorldGuard.getInstance().getPlatform().getRegionContainer();
        List<Region> regions = new ArrayList<>();
        boolean truncated = false;
        for (var world : Bukkit.getWorlds()) {
            var manager = container.get(BukkitAdapter.adapt(world));
            if (manager == null) continue;
            for (var region : manager.getRegions().values()) {
                if (!region.isPhysicalArea() || !region.isMember(local)) continue;
                if (regions.size() == 32) { truncated = true; continue; }
                var min = region.getMinimumPoint(); var max = region.getMaximumPoint();
                regions.add(new Region(region.getId(), world.getName(), region.isOwner(local) ? "owner" : "member",
                    (int)(((long)min.x() + max.x()) / 2),
                    (int)(((long)min.y() + max.y()) / 2),
                    (int)(((long)min.z() + max.z()) / 2)));
            }
        }
        regions.sort(Comparator.comparing(Region::world).thenComparing(Region::id));
        return new Result(regions, truncated);
    }
}
