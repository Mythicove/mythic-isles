package isles

import (
	"io"
	"net/http"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type Metric struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	c, ok := a.connection(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if c.Kind != "prometheus" {
		http.Error(w, "select a metrics connection", 400)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", c.URL, nil)
	if err != nil {
		failed(w)
		return
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	res, err := a.http.Do(req)
	if err != nil {
		http.Error(w, "metrics unreachable", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		http.Error(w, "metrics endpoint rejected request", 502)
		return
	}
	// Exporters can include player-labelled series: only server-level metrics enter the admin view.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		http.Error(w, "invalid Prometheus exposition", 502)
		return
	}
	items := []Metric{}
	for name, f := range families {
		if !allowedMetric(name) {
			continue
		}
		for _, m := range f.Metric {
			labels := map[string]string{}
			for _, l := range m.Label {
				labels[l.GetName()] = l.GetValue()
			}
			var value float64
			if m.Gauge != nil {
				value = m.Gauge.GetValue()
			} else if m.Counter != nil {
				value = m.Counter.GetValue()
			} else {
				continue
			}
			items = append(items, Metric{Name: name, Labels: labels, Value: value})
		}
	}
	reply(w, map[string]any{"checkedAt": time.Now().UTC(), "metrics": items})
}

func allowedMetric(s string) bool {
	switch s {
	case "mc_players_online_total", "mc_tps", "mc_tick_duration_average", "mc_tick_duration_median", "mc_tick_duration_max", "mc_jvm_memory", "mc_loaded_chunks_total", "mc_entities_total", "mc_world_size":
		return true
	}
	return false
}
