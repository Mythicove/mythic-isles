package isles

import (
	"bytes"
	"crypto/subtle"
	"net/http"
	"slices"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// Snapshot gauges, not accumulating counters: provider baselines can reset.
// Only bounded server/collector labels enter this private scrape surface.
func (a *App) prometheus(w http.ResponseWriter, r *http.Request) {
	credential := a.metricsToken
	if credential == "" {
		credential = a.token
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+credential)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	families := map[string]*dto.MetricFamily{}
	add := func(name, help, server string, collector *string, value float64) {
		family := families[name]
		if family == nil {
			family = &dto.MetricFamily{Name: &name, Help: &help, Type: dto.MetricType_GAUGE.Enum()}
			families[name] = family
		}
		serverLabel := "server"
		metric := &dto.Metric{Gauge: &dto.Gauge{Value: &value}, Label: []*dto.LabelPair{{Name: &serverLabel, Value: &server}}}
		if collector != nil {
			collectorLabel := "collector"
			metric.Label = append([]*dto.LabelPair{{Name: &collectorLabel, Value: collector}}, metric.Label...)
		}
		family.Metric = append(family.Metric, metric)
	}
	a.mu.Lock()
	servers := slices.Clone(a.config.Servers)
	snapshots := make(map[string]BridgeSnapshot, len(servers))
	for _, server := range servers {
		if snapshot, ok := a.bridges[server.ID]; ok {
			snapshots[server.ID] = snapshot
		}
	}
	a.mu.Unlock()
	now := time.Now()
	for _, server := range servers {
		snapshot, observed := snapshots[server.ID]
		fresh := observed && now.Sub(snapshot.ReceivedAt) < 90*time.Second
		value := 0.0
		if fresh {
			value = 1
		}
		add("mythic_isles_bridge_fresh", "Whether this configured server has a Bridge observation less than 90 seconds old.", server.ID, nil, value)
		if observed {
			add("mythic_isles_bridge_observed_timestamp_seconds", "Unix time of the last accepted Bridge observation, including stale observations.", server.ID, nil, float64(snapshot.ReceivedAt.UnixNano())/1e9)
		}
		if !fresh {
			continue
		}
		if snapshot.TPS != nil {
			add("mythic_isles_bridge_tps", "Provider-reported 10-second average ticks per second from the latest fresh observation.", server.ID, nil, *snapshot.TPS)
		}
		if snapshot.MSPT != nil {
			add("mythic_isles_bridge_mspt_seconds", "Provider-reported one-minute mean tick duration in seconds from the latest fresh observation.", server.ID, nil, *snapshot.MSPT/1000)
		}
		if snapshot.CPU != nil {
			add("mythic_isles_bridge_system_cpu_ratio", "Provider-reported one-minute system CPU ratio from the latest fresh observation.", server.ID, nil, *snapshot.CPU)
		}
		collectors := make([]string, 0, len(snapshot.GarbageCollectors))
		for name := range snapshot.GarbageCollectors {
			collectors = append(collectors, name)
		}
		slices.Sort(collectors)
		for _, name := range collectors {
			stats := snapshot.GarbageCollectors[name]
			if stats.Collections != nil {
				add("mythic_isles_bridge_gc_collections", "Provider GC collections since its normal-operation baseline; resets are possible, not a monotonic counter.", server.ID, &name, float64(*stats.Collections))
			}
			if stats.TimeMs != nil {
				add("mythic_isles_bridge_gc_collection_seconds", "Provider accumulated GC time in seconds since its normal-operation baseline; not a pause histogram.", server.ID, &name, float64(*stats.TimeMs)/1000)
			}
		}
	}
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	slices.Sort(names)
	var body bytes.Buffer
	format := expfmt.NewFormat(expfmt.TypeTextPlain)
	encoder := expfmt.NewEncoder(&body, format)
	for _, name := range names {
		if err := encoder.Encode(families[name]); err != nil {
			failed(w)
			return
		}
	}
	w.Header().Set("Content-Type", string(format))
	_, _ = w.Write(body.Bytes())
}
