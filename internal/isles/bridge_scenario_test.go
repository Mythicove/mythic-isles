package isles_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

func TestVerifiedIdentityAndMemberTaskScenario(t *testing.T) {
	dir := t.TempDir()
	app, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	client := api.Client()
	status, b := request(t, client, api.URL, "PUT", "/api/isles/admin/servers/survival", map[string]any{"name": "Survival", "edition": "java", "address": "localhost:25565", "published": true}, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, client, api.URL, "POST", "/api/isles/admin/servers/survival/bridge-token", nil, true)
	mustStatus(t, 200, status, b)
	var generated struct{ Token string }
	if err := json.Unmarshal(b, &generated); err != nil {
		t.Fatal(err)
	}
	bridgeToken := generated.Token
	call := func(method, path, credential, player string, body any) (int, []byte) {
		var input bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&input).Encode(body)
		}
		req, err := http.NewRequest(method, api.URL+path, &input)
		if err != nil {
			t.Fatal(err)
		}
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		if player != "" {
			req.Header.Set("X-Isles-Player", player)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out bytes.Buffer
		_, _ = out.ReadFrom(res.Body)
		return res.StatusCode, out.Bytes()
	}
	status, b = call("POST", "/api/isles/players/site-player/pair", token, "another-player", map[string]string{"server": "survival"})
	mustStatus(t, 401, status, b)
	status, b = call("POST", "/api/isles/players/site-player/pair", token, "site-player", map[string]string{"server": "survival"})
	mustStatus(t, 200, status, b)
	var pair struct{ Code string }
	_ = json.Unmarshal(b, &pair)
	identity := map[string]string{"code": pair.Code, "uuid": "95db2f25-60a5-4c75-adfb-a69de4db748e", "name": "Alice", "platform": "java"}
	status, b = call("POST", "/api/isles/bridge/survival/link", "wrong-key", "", identity)
	mustStatus(t, 401, status, b)
	status, b = call("POST", "/api/isles/bridge/survival/link", bridgeToken, "", identity)
	mustStatus(t, 204, status, b)
	status, b = call("POST", "/api/isles/bridge/survival/link", bridgeToken, "", identity)
	mustStatus(t, 403, status, b)
	status, b = call("GET", "/api/isles/bridge/survival/identities", bridgeToken, "", nil)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(identity["uuid"])) || bytes.Contains(b, []byte("site-player")) {
		t.Fatalf("invalid UUID allowlist: %s", b)
	}
	status, b = call("GET", "/api/isles/bridge/other/identities", bridgeToken, "", nil)
	mustStatus(t, 401, status, b)
	snapshot := map[string]any{"platform": "Paper", "minecraft": "1.21.1", "plugins": map[string]string{"LuckPerms": "5.4", "spark": "1.10"}, "groups": []string{"member"}, "tps": 19.9, "players": []any{map[string]any{"uuid": identity["uuid"], "name": "Alice", "group": "member", "values": map[string]string{"balance": "12.00"}}}}
	snapshot["protocolVersions"] = []string{"1.21.1"}
	snapshot["players"] = []any{map[string]any{"uuid": identity["uuid"], "name": "Alice", "group": "member", "values": map[string]string{"balance": "12.00"}, "clientVersion": "1.21.1", "regions": []any{map[string]any{"id": "home", "world": "world", "role": "member", "centerX": 5, "centerY": 70, "centerZ": 5}}}}
	snapshot["players"].([]any)[0].(map[string]any)["economy"] = map[string]any{"provider": "EssentialsX Economy", "balance": 123.44999999999999, "formatted": "$123.44", "currency": "$"}
	activity := map[string]any{"uuid": identity["uuid"], "sessions": 2, "playtimeMs": 90000, "afkMs": 1000, "firstSeen": "2026-11-10T10:00:00Z", "lastSeen": "2026-11-10T10:01:30Z", "observedAt": "2026-11-10T10:02:00Z"}
	snapshot["plan"] = map[string]any{"serverUUID": "12345678-1234-1234-1234-123456789012", "recordedPlayers": 1, "sessions": 2, "playtimeMs": 90000, "afkMs": 1000, "observedAt": "2026-11-10T10:02:00Z", "players": []any{activity}}
	status, b = call("POST", "/api/isles/bridge/survival/heartbeat", bridgeToken, "", snapshot)
	mustStatus(t, 204, status, b)
	status, b = call("GET", "/api/isles/players/site-player/links", token, "site-player", nil)
	mustStatus(t, 200, status, b)
	var links []struct {
		Fresh    bool
		Identity struct{ UUID string }
		Player   struct {
			Group  string
			Values map[string]string
		}
	}
	if err := json.Unmarshal(b, &links); err != nil || len(links) != 1 || !links[0].Fresh || links[0].Identity.UUID != identity["uuid"] || links[0].Player.Values["balance"] != "12.00" {
		t.Fatalf("linked profile: %s", b)
	}
	if !bytes.Contains(b, []byte(`"clientVersion":"1.21.1"`)) || !bytes.Contains(b, []byte(`"id":"home"`)) {
		t.Fatalf("provider data missing: %s", b)
	}
	if !bytes.Contains(b, []byte(`"formatted":"$123.44"`)) {
		t.Fatalf("provider currency format was rewritten: %s", b)
	}
	if !bytes.Contains(b, []byte(`"activity"`)) || !bytes.Contains(b, []byte(`"playtimeMs":90000`)) || bytes.Contains(b, []byte("recordedPlayers")) {
		t.Fatalf("private Plan projection: %s", b)
	}
	// Persisted activity is independent of the online Bukkit player list.
	snapshot["players"] = []any{}
	status, b = call("POST", "/api/isles/bridge/survival/heartbeat", bridgeToken, "", snapshot)
	mustStatus(t, 204, status, b)
	status, b = call("GET", "/api/isles/players/site-player/links", token, "site-player", nil)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`"activity"`)) || bytes.Contains(b, []byte(`"player":`)) {
		t.Fatalf("offline activity missing: %s", b)
	}
	status, b = call("GET", "/api/isles/players/unlinked/links", token, "unlinked", nil)
	mustStatus(t, 200, status, b)
	if bytes.Contains(b, []byte("playtimeMs")) {
		t.Fatal("Plan activity disclosed to unlinked owner")
	}
	activity["playtimeMs"] = -1
	status, b = call("POST", "/api/isles/bridge/survival/heartbeat", bridgeToken, "", snapshot)
	mustStatus(t, 400, status, b)
	activity["playtimeMs"] = 90000
	status, b = request(t, client, api.URL, "POST", "/api/isles/admin/servers/survival/tasks", map[string]any{"player": "site-player", "kind": "member", "group": "member", "grant": true}, true)
	mustStatus(t, 200, status, b)
	var task struct {
		ID     string
		Status string
	}
	_ = json.Unmarshal(b, &task)
	if task.Status != "pending" {
		t.Fatal(string(b))
	}
	status, b = call("GET", "/api/isles/bridge/survival/tasks", bridgeToken, "", nil)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(task.ID)) {
		t.Fatal("task not available to bound bridge")
	}
	// Restart before acknowledgement: desired state is durable and can be replayed.
	reloaded, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(reloaded.Handler())
	defer other.Close()
	status, b = request(t, other.Client(), other.URL, "GET", "/api/isles/admin/servers/survival/tasks", nil, true)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(task.ID)) {
		t.Fatal("pending task lost on restart")
	}
	status, b = call("POST", "/api/isles/bridge/survival/tasks/"+task.ID, bridgeToken, "", map[string]any{"success": true, "error": ""})
	mustStatus(t, 204, status, b)
	status, b = call("GET", "/api/isles/bridge/survival/tasks", bridgeToken, "", nil)
	mustStatus(t, 200, status, b)
	if string(bytes.TrimSpace(b)) != "[]" {
		t.Fatalf("acknowledged task replayed: %s", b)
	}
	status, b = request(t, client, api.URL, "GET", "/api/isles/servers", nil, false)
	mustStatus(t, 200, status, b)
	for _, secret := range []string{bridgeToken, identity["uuid"], "balance", "plan", "playtimeMs"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("bridge/private profile leaked publicly")
		}
	}
	status, b = request(t, client, api.URL, "POST", "/api/isles/admin/servers/survival/bridge-token", nil, true)
	mustStatus(t, 200, status, b)
	status, b = call("GET", "/api/isles/bridge/survival/tasks", bridgeToken, "", nil)
	mustStatus(t, 401, status, b)
	_ = json.Unmarshal(b, &generated)
	status, b = request(t, client, api.URL, "DELETE", "/api/isles/admin/servers/survival", nil, true)
	mustStatus(t, 204, status, b)
	status, b = call("GET", "/api/isles/bridge/survival/tasks", bridgeToken, "", nil)
	mustStatus(t, 401, status, b)
}
