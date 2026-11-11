package isles_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mythicove/mythic-isles/internal/isles"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Production composition: service auth, durable server config, Bridge ingress,
// exporter, and the upstream Prometheus text parser. No internal state injection.
func TestPrometheusBridgeExportScenario(t *testing.T) {
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	status, b := request(t, api.Client(), api.URL, "GET", "/metrics", nil, false)
	mustStatus(t, 401, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/healthz", nil, false)
	mustStatus(t, 200, status, b)
	for _, id := range []string{"public", "draft"} {
		status, b = request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/"+id, map[string]any{"name": "private-display-name", "edition": "java", "address": "localhost:25565", "published": id == "public"}, true)
		mustStatus(t, 200, status, b)
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/metrics", nil, true)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`mythic_isles_bridge_fresh{server="draft"} 0`)) || bytes.Contains(b, []byte("private-display-name")) {
		t.Fatalf("unreported server identity/freshness: %s", b)
	}
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/public/bridge-token", nil, true)
	mustStatus(t, 200, status, b)
	var secret struct{ Token string }
	_ = json.Unmarshal(b, &secret)
	beat := func(snapshot any) {
		data, _ := json.Marshal(snapshot)
		req, _ := http.NewRequest("POST", api.URL+"/api/isles/bridge/public/heartbeat", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+secret.Token)
		res, err := api.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 204 {
			t.Fatalf("heartbeat: %s", res.Status)
		}
	}
	collector := "GC\"\\\ncollector"
	snapshot := map[string]any{"platform": "Paper", "minecraft": "1.21.1", "plugins": map[string]string{"spark": "service"}, "groups": []string{}, "players": []any{map[string]string{"uuid": "private-game-uuid", "name": "private-player-name"}}, "tps": 19.9, "mspt": 12.5, "cpu": 0.25, "garbageCollectors": map[string]any{collector: map[string]any{"collections": 0, "timeMs": 1234}, "unknown": map[string]any{}}}
	beat(snapshot)
	req, _ := http.NewRequest("GET", api.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+secret.Token)
	res, err := api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("per-server credential admitted to fleet metrics")
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/metrics", nil, true)
	mustStatus(t, 200, status, b)
	for _, private := range []string{token, secret.Token, "private-player-name", "private-game-uuid", "private-display-name", "spark"} {
		if bytes.Contains(b, []byte(private)) {
			t.Fatalf("private value leaked in metrics: %s", private)
		}
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{"mythic_isles_bridge_tps": 19.9, "mythic_isles_bridge_mspt_seconds": 0.0125, "mythic_isles_bridge_system_cpu_ratio": 0.25, "mythic_isles_bridge_gc_collections": 0, "mythic_isles_bridge_gc_collection_seconds": 1.234} {
		family := families[name]
		if family == nil || len(family.Metric) != 1 || family.Metric[0].GetGauge().GetValue() != want {
			t.Fatalf("%s: %v", name, family)
		}
	}
	gc := families["mythic_isles_bridge_gc_collections"].Metric[0]
	if gc.Label[0].GetName() != "collector" || gc.Label[0].GetValue() != collector {
		t.Fatalf("collector label was not safely round-tripped: %v", gc.Label)
	}
	if families["mythic_isles_bridge_observed_timestamp_seconds"].Metric[0].GetGauge().GetValue() <= 0 {
		t.Fatal("observation timestamp absent")
	}
	delete(snapshot, "tps")
	delete(snapshot, "mspt")
	delete(snapshot, "cpu")
	delete(snapshot, "garbageCollectors")
	beat(snapshot)
	status, b = request(t, api.Client(), api.URL, "GET", "/metrics", nil, true)
	mustStatus(t, 200, status, b)
	if strings.Contains(string(b), "mythic_isles_bridge_tps{") || strings.Contains(string(b), "mythic_isles_bridge_gc_collections{") {
		t.Fatal("unavailable measurements fabricated or retained")
	}
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/public/bridge-token", nil, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/metrics", nil, true)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`mythic_isles_bridge_fresh{server="public"} 0`)) || bytes.Contains(b, []byte(`mythic_isles_bridge_observed_timestamp_seconds{`)) {
		t.Fatal("rotation retained invalidated bridge observation")
	}
	status, b = request(t, api.Client(), api.URL, "DELETE", "/api/isles/admin/servers/public", nil, true)
	mustStatus(t, 204, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/metrics", nil, true)
	mustStatus(t, 200, status, b)
	if bytes.Contains(b, []byte(`server="public"`)) {
		t.Fatal("deleted server series retained")
	}
}
