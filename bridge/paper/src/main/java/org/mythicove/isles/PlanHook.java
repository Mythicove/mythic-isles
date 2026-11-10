package org.mythicove.isles;

import com.djrapitops.plan.capability.CapabilityService;
import com.djrapitops.plan.query.QueryService;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** Read-only Plan 5 Query API. Called exclusively on the network executor. */
final class PlanHook {
    // Stored sessions only: do not add an in-memory current session to persisted
    // totals, or label a missing/disabled provider as zero activity.
    private static final String TOTALS = "COUNT(*) AS sessions, COALESCE(SUM(session_end-session_start),0) AS playtime, COALESCE(SUM(afk_time),0) AS afk";
    private static final String SCOPE = " FROM plan_sessions s JOIN plan_servers v ON s.server_id=v.id WHERE v.uuid=? AND s.session_end>=s.session_start";

    Map<String, Object> collect(List<String> identities) {
        if (!CapabilityService.getInstance().hasCapability("QUERY_API")) throw new IllegalStateException("Plan Query API unavailable");
        QueryService query = QueryService.getInstance();
        String server = query.getServerUUID().orElseThrow(() -> new IllegalStateException("Plan server not ready")).toString();
        String observed = Instant.now().toString();
        Map<String, Object> result = query.query("SELECT COUNT(DISTINCT s.user_id) AS players, " + TOTALS + SCOPE, statement -> {
            statement.setQueryTimeout(5);
            statement.setString(1, server);
            try (var rows = statement.executeQuery()) {
                if (!rows.next()) throw new IllegalStateException("Plan aggregate unavailable");
                Map<String, Object> stats = new LinkedHashMap<>();
                stats.put("serverUUID", server);
                stats.put("observedAt", observed);
                stats.put("recordedPlayers", rows.getLong("players"));
                stats.put("sessions", rows.getLong("sessions"));
                stats.put("playtimeMs", rows.getLong("playtime"));
                stats.put("afkMs", rows.getLong("afk"));
                return stats;
            }
        });
        List<Map<String, Object>> players = new ArrayList<>();
        // Keep parameter counts below SQLite's historical limit. No UUID,
        // table name or SQL fragment is accepted from a website request.
        for (int offset = 0; offset < identities.size(); offset += 500) {
            var batch = identities.subList(offset, Math.min(offset + 500, identities.size()));
            String sql = "SELECT u.uuid, " + TOTALS + ", MIN(session_start) AS first_seen, MAX(session_end) AS last_seen"
                + " FROM plan_sessions s JOIN plan_servers v ON s.server_id=v.id JOIN plan_users u ON s.user_id=u.id"
                + " WHERE v.uuid=? AND s.session_end>=s.session_start AND u.uuid IN (" + String.join(",", Collections.nCopies(batch.size(), "?")) + ") GROUP BY u.uuid";
            query.query(sql, statement -> {
                statement.setQueryTimeout(5);
                statement.setString(1, server);
                for (int i = 0; i < batch.size(); i++) statement.setString(i + 2, batch.get(i));
                try (var rows = statement.executeQuery()) {
                    while (rows.next()) {
                        players.add(Map.of("uuid", rows.getString("uuid"), "sessions", rows.getLong("sessions"),
                            "playtimeMs", rows.getLong("playtime"), "afkMs", rows.getLong("afk"),
                            "firstSeen", Instant.ofEpochMilli(rows.getLong("first_seen")).toString(),
                            "lastSeen", Instant.ofEpochMilli(rows.getLong("last_seen")).toString(), "observedAt", observed));
                    }
                }
                return null;
            });
        }
        result.put("players", players);
        return result;
    }
}
