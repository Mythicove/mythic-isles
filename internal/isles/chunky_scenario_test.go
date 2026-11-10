package isles_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

// Exercise the production HTTP assembly, including persisted at-most-once
// dispatch: a lost result must never restart or cancel a second generation.
func TestChunkyAdministrationScenario(t *testing.T) {
	dir := t.TempDir()
	app, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	call := func(method, path string, body any) (int, []byte) {
		return request(t, api.Client(), api.URL, method, "/api/isles/"+path, body, true)
	}
	status, b := call("PUT", "admin/servers/live", map[string]any{"name": "Worlds", "edition": "java", "address": "localhost:25565", "published": true})
	mustStatus(t, 200, status, b)
	status, b = call("POST", "admin/servers/live/bridge-token", nil)
	mustStatus(t, 200, status, b)
	var secret struct{ Token string }
	_ = json.Unmarshal(b, &secret)
	bridge := func(method, path string, body any) (int, []byte) {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/isles/bridge/live/"+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+secret.Token)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		return res.Code, res.Body.Bytes()
	}
	input := map[string]any{"action": "start", "world": "world", "shape": "square", "centerX": 1000, "centerZ": 1000, "radius": 128}
	status, b = call("POST", "admin/servers/live/chunky", input)
	mustStatus(t, 409, status, b)
	snapshot := map[string]any{"platform": "Paper", "minecraft": "1.21.1", "plugins": map[string]string{"Chunky": "1.4.40"}, "groups": []string{}, "players": []any{}, "chunky": []any{map[string]any{"world": "world", "state": "idle"}}}
	status, b = bridge("POST", "heartbeat", snapshot)
	mustStatus(t, 204, status, b)
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/live/chunky", input, false)
	mustStatus(t, 401, status, b)
	for field, invalid := range map[string]any{"world": "missing", "radius": 0, "shape": "arbitrary-command", "centerX": 30_000_000, "action": "trim"} {
		old := input[field]
		input[field] = invalid
		status, b = call("POST", "admin/servers/live/chunky", input)
		if status != 400 && status != 409 {
			t.Fatalf("invalid %s: %d %s", field, status, b)
		}
		input[field] = old
	}
	status, b = call("POST", "admin/servers/live/chunky", input)
	mustStatus(t, 200, status, b)
	var task struct {
		ID     string
		Status string
	}
	_ = json.Unmarshal(b, &task)
	if task.ID == "" || task.Status != "pending" {
		t.Fatal(string(b))
	}
	status, b = call("POST", "admin/servers/live/chunky", input)
	mustStatus(t, 409, status, b)
	// A Chunky task cannot be acknowledged without first claiming it.
	status, b = bridge("POST", "tasks/"+task.ID, map[string]any{"success": true})
	mustStatus(t, 409, status, b)
	status, b = bridge("POST", "tasks/"+task.ID+"/claim", nil)
	mustStatus(t, 204, status, b)
	status, b = bridge("POST", "tasks/"+task.ID+"/claim", nil)
	mustStatus(t, 409, status, b)
	status, b = bridge("GET", "tasks", nil)
	mustStatus(t, 200, status, b)
	if bytes.Contains(b, []byte(task.ID)) {
		t.Fatal("claimed operation redelivered")
	}
	reloaded, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(reloaded.Handler())
	defer other.Close()
	status, b = request(t, other.Client(), other.URL, "GET", "/api/isles/admin/servers/live/tasks", nil, true)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`"status":"dispatched"`)) {
		t.Fatalf("lost dispatch state: %s", b)
	}
	status, b = bridge("POST", "tasks/"+task.ID, map[string]any{"success": true})
	mustStatus(t, 204, status, b)
	snapshot["chunky"] = []any{map[string]any{"world": "world", "state": "running", "selection": map[string]any{"shape": "square", "centerX": 1000, "centerZ": 1000, "radiusX": 128, "radiusZ": 128}, "progress": map[string]any{"chunks": 42, "total": 289, "percent": 14.53, "rate": 2.0, "etaSeconds": 123}}}
	status, b = bridge("POST", "heartbeat", snapshot)
	mustStatus(t, 204, status, b)
	status, b = call("GET", "admin/servers/live/bridge", nil)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`"chunks":42`)) {
		t.Fatal(string(b))
	}
	input["action"] = "pause"
	status, b = call("POST", "admin/servers/live/chunky", input)
	mustStatus(t, 200, status, b)
	_ = json.Unmarshal(b, &task)
	status, b = bridge("POST", "tasks/"+task.ID+"/claim", nil)
	mustStatus(t, 204, status, b)
	status, b = bridge("POST", "tasks/"+task.ID, map[string]any{"success": false, "error": "Provider changed state"})
	mustStatus(t, 204, status, b)
	status, b = call("GET", "admin/servers/live/tasks", nil)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`"status":"failed"`)) {
		t.Fatal("provider rejection lost")
	}
	snapshot["chunky"] = []any{map[string]any{"world": "world", "state": "paused"}}
	status, b = bridge("POST", "heartbeat", snapshot)
	mustStatus(t, 204, status, b)
	input["action"] = "start"
	status, b = call("POST", "admin/servers/live/chunky", input)
	mustStatus(t, 409, status, b)
	snapshot["chunky"] = []any{map[string]any{"world": "world", "state": "completed", "progress": map[string]any{"chunks": 1, "total": 1, "percent": 101}}}
	status, b = bridge("POST", "heartbeat", snapshot)
	mustStatus(t, 400, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers", nil, false)
	mustStatus(t, 200, status, b)
	if bytes.Contains(b, []byte("chunky")) {
		t.Fatal("maintenance details leaked publicly")
	}
	status, b = call("POST", "admin/servers/live/bridge-token", nil)
	mustStatus(t, 200, status, b)
	input["action"] = "continue"
	status, b = call("POST", "admin/servers/live/chunky", input)
	mustStatus(t, 409, status, b)
	status, b = bridge("POST", "tasks/"+task.ID+"/claim", nil)
	mustStatus(t, 401, status, b)
}
