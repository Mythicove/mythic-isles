package isles_test

import (
	"bytes"
	"encoding/json"
	"github.com/Mythicove/mythic-isles/internal/isles"
	"net/http/httptest"
	"testing"
)

func TestCoreProtectHistoryScenario(t *testing.T) {
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
	status, b := call("PUT", "admin/servers/live", map[string]any{"name": "History", "edition": "java", "address": "localhost:25565", "published": true})
	mustStatus(t, 200, status, b)
	status, b = call("POST", "admin/servers/live/bridge-token", nil)
	mustStatus(t, 200, status, b)
	var secret struct{ Token string }
	_ = json.Unmarshal(b, &secret)
	bridge := func(path string, body any) (int, []byte) {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/isles/bridge/live/"+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+secret.Token)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		return res.Code, res.Body.Bytes()
	}
	query := map[string]any{"world": "world", "x": 10, "y": 64, "z": 20, "seconds": 3600, "offset": 0}
	status, b = call("POST", "admin/servers/live/coreprotect", query)
	mustStatus(t, 409, status, b)
	status, b = bridge("heartbeat", map[string]any{"platform": "Paper", "minecraft": "1.21.1", "plugins": map[string]string{"CoreProtect": "24.1"}, "groups": []string{}, "players": []any{}})
	mustStatus(t, 204, status, b)
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/live/coreprotect", query, false)
	mustStatus(t, 401, status, b)
	for field, value := range map[string]any{"world": "", "seconds": 0, "offset": -1, "x": 30_000_000, "y": 2049} {
		old := query[field]
		query[field] = value
		status, b = call("POST", "admin/servers/live/coreprotect", query)
		mustStatus(t, 400, status, b)
		query[field] = old
	}
	status, b = call("POST", "admin/servers/live/coreprotect", query)
	mustStatus(t, 200, status, b)
	var task struct{ ID string }
	_ = json.Unmarshal(b, &task)
	status, b = bridge("tasks/"+task.ID, map[string]any{"success": true})
	mustStatus(t, 400, status, b)
	result := map[string]any{"records": []any{map[string]any{"player": "RecordedActor", "timestampMs": int64(1700000000000), "action": "place", "material": "minecraft:stone", "rolledBack": false}}, "hasMore": false}
	status, b = bridge("tasks/"+task.ID, map[string]any{"success": true, "coreprotectResult": result})
	mustStatus(t, 204, status, b)
	reloaded, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/isles/admin/servers/live/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	reloaded.Handler().ServeHTTP(res, req)
	if res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte("RecordedActor")) {
		t.Fatal(res.Body.String())
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers/live", nil, false)
	mustStatus(t, 200, status, b)
	if bytes.Contains(b, []byte("RecordedActor")) || bytes.Contains(b, []byte("coreprotect")) {
		t.Fatal("history leaked to public directory")
	}
}
