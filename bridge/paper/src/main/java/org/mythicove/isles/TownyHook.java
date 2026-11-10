package org.mythicove.isles;

import com.palmergames.bukkit.towny.TownyAPI;
import com.palmergames.bukkit.towny.object.Town;
import com.palmergames.bukkit.towny.object.Nation;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/** Read-only Towny data, collected on the Bukkit thread, including offline residents. */
final class TownyHook {
    Map<String, Object> collect(List<String> identities) {
        var api = TownyAPI.getInstance();
        var towns = api.getTowns();
        var nations = api.getNations();
        String observed = Instant.now().toString();
        List<Map<String, Object>> residents = new ArrayList<>();
        for (String uuid : identities) {
            var resident = api.getResident(UUID.fromString(uuid));
            if (resident == null || resident.isNPC()) continue;
            if (resident.getTownRanks().size() > 32 || resident.getNationRanks().size() > 32) throw new IllegalStateException("Towny rank limit exceeded");
            Map<String, Object> row = new LinkedHashMap<>();
            row.put("uuid", uuid);
            row.put("mayor", resident.isMayor());
            row.put("king", resident.isKing());
            row.put("townRanks", List.copyOf(resident.getTownRanks()));
            row.put("nationRanks", List.copyOf(resident.getNationRanks()));
            row.put("observedAt", observed);
            if (resident.hasTown()) row.put("town", town(resident.getTownOrNull()));
            residents.add(row);
        }
        return Map.of("townCount", towns.size(), "nationCount", nations.size(), "observedAt", observed,
            "towns", towns.stream().sorted(Comparator.comparing(t -> t.getUUID().toString())).limit(100).map(this::town).toList(),
            "nations", nations.stream().sorted(Comparator.comparing(n -> n.getUUID().toString())).limit(100).map(this::nation).toList(),
            "residents", residents);
    }

    private Map<String, Object> town(Town town) {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("id", town.getUUID().toString());
        result.put("name", town.getName());
        result.put("residents", town.getNumResidents());
        result.put("claimedTownBlocks", town.getNumTownBlocks());
        result.put("open", town.isOpen());
        result.put("public", town.isPublic());
        var nation = town.getNationOrNull();
        if (nation != null) result.put("nation", Map.of("id", nation.getUUID().toString(), "name", nation.getName()));
        return result;
    }

    private Map<String, Object> nation(Nation nation) {
        return Map.of("id", nation.getUUID().toString(), "name", nation.getName(), "towns", nation.getNumTowns(),
            "residents", nation.getNumResidents(), "open", nation.isOpen(), "public", nation.isPublic());
    }
}
