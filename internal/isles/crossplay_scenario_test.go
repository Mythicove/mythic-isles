package isles_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

func TestBedrockIdentityAndMembershipScenario(t *testing.T) {
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	status, b := request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/java-host", map[string]any{"name": "Crossplay", "edition": "java", "address": "localhost:25565", "published": true}, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/java-host/bridge-token", nil, true)
	mustStatus(t, 200, status, b)
	var secret struct{ Token string }
	_ = json.Unmarshal(b, &secret)
	call := func(method, path, credential, owner string, body any) (int, []byte) {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/isles/"+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("X-Isles-Player", owner)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		return res.Code, res.Body.Bytes()
	}
	status, b = call("POST", "players/owner/pair", token, "owner", map[string]string{"server": "java-host"})
	mustStatus(t, 200, status, b)
	var pair struct{ Code string }
	_ = json.Unmarshal(b, &pair)
	// The actual Bukkit UUID can be a linked Java account's UUID. Neither XUID
	// conversion nor username prefixes may redirect the membership operation.
	uuid := "95db2f25-60a5-4c75-adfb-a69de4db748e"
	identity := map[string]string{"code": pair.Code, "uuid": uuid, "name": ".BedrockTester", "platform": "prefix-guessed"}
	status, b = call("POST", "bridge/java-host/link", secret.Token, "", identity)
	mustStatus(t, 400, status, b)
	identity["platform"] = "bedrock"
	status, b = call("POST", "bridge/java-host/link", secret.Token, "", identity)
	mustStatus(t, 204, status, b)
	connection := map[string]any{"edition": "bedrock", "provider": "floodgate", "version": "1.21.111", "linkedJavaAccount": true}
	snapshot := map[string]any{"platform": "Paper", "minecraft": "1.21.1", "plugins": map[string]string{"floodgate": "2.2.5", "Geyser-Spigot": "2.8.3"}, "groups": []string{}, "players": []any{map[string]any{"uuid": uuid, "name": ".BedrockTester", "connection": connection}}, "crossplay": map[string]any{"javaVersion": "1.21.8", "bedrockVersions": []string{"1.21.111"}}}
	status, b = call("POST", "bridge/java-host/heartbeat", secret.Token, "", snapshot)
	mustStatus(t, 204, status, b)
	status, b = call("GET", "players/owner/links", token, "owner", nil)
	mustStatus(t, 200, status, b)
	for _, value := range []string{`"platform":"bedrock"`, `"provider":"floodgate"`, `"linkedJavaAccount":true`} {
		if !bytes.Contains(b, []byte(value)) {
			t.Fatal(string(b))
		}
	}
	status, b = call("GET", "players/owner/links", token, "other", nil)
	mustStatus(t, 401, status, b)
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/java-host/tasks", map[string]any{"player": "owner", "kind": "whitelist", "grant": true}, true)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(uuid)) {
		t.Fatalf("membership identity changed: %s", b)
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers", nil, false)
	mustStatus(t, 200, status, b)
	for _, private := range []string{uuid, "crossplay", "floodgate", "linkedJavaAccount"} {
		if bytes.Contains(b, []byte(private)) {
			t.Fatal("private connection data exposed")
		}
	}
	connection["provider"] = "prefix"
	status, b = call("POST", "bridge/java-host/heartbeat", secret.Token, "", snapshot)
	mustStatus(t, 400, status, b)
	connection["provider"] = "geyser"
	status, b = call("POST", "bridge/java-host/heartbeat", secret.Token, "", snapshot)
	mustStatus(t, 204, status, b)
	// After disconnect the observation disappears, while the verified platform
	// and Bukkit UUID remain attached to the website player's durable link.
	snapshot["players"] = []any{}
	status, b = call("POST", "bridge/java-host/heartbeat", secret.Token, "", snapshot)
	mustStatus(t, 204, status, b)
	status, b = call("GET", "players/owner/links", token, "owner", nil)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte(`"platform":"bedrock"`)) || bytes.Contains(b, []byte(`"connection":`)) {
		t.Fatal(string(b))
	}
}
