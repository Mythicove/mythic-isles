package org.mythicove.isles;
import me.lucko.spark.api.SparkProvider;
import me.lucko.spark.api.Spark;
import org.bukkit.Bukkit;
import me.lucko.spark.api.statistic.StatisticWindow;
import java.util.LinkedHashMap;
import java.util.Map;

final class SparkHook {
    private final Spark spark;
    SparkHook() {
        var registration = Bukkit.getServicesManager().getRegistration(Spark.class);
        spark = registration == null ? SparkProvider.get() : registration.getProvider();
    }
    Map<String, Object> statistics() {
        Map<String, Object> data = new LinkedHashMap<>();
        if (spark.tps() != null) data.put("tps", spark.tps().poll(StatisticWindow.TicksPerSecond.SECONDS_10));
        if (spark.mspt() != null) data.put("mspt", spark.mspt().poll(StatisticWindow.MillisPerTick.MINUTES_1).mean());
        data.put("cpu", spark.cpuSystem().poll(StatisticWindow.CpuUsage.MINUTES_1));
        return data;
    }
}
