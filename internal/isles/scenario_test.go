package isles_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

const token = "scenario-admin-token-with-32-characters"
const key = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func request(t *testing.T, client *http.Client, base, method, path string, body any, admin bool) (int, []byte) {
	t.Helper()
	var b io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		b = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, base+path, b)
	if err != nil {
		t.Fatal(err)
	}
	if admin {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, data
}
func mustStatus(t *testing.T, want, got int, b []byte) {
	t.Helper()
	if want != got {
		t.Fatalf("HTTP %d want %d: %s", got, want, b)
	}
}

func TestServerManagementScenario(t *testing.T) {
	dir := t.TempDir()
	app, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	var commands atomic.Int32
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "panel-private-key" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Error("missing MCSManager credentials/headers")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/service/remote_services_list":
			fmt.Fprint(w, `{"status":200,"data":[{"uuid":"daemon-a"}]}`)
		case "/api/service/remote_service_instances":
			fmt.Fprint(w, `{"status":200,"data":{"maxPage":1,"data":[{"instanceUuid":"instance-a","config":{"nickname":"Survival"}}]}}`)
		case "/api/instance":
			fmt.Fprint(w, `{"status":200,"data":{"status":3,"processInfo":{"cpu":12.5,"memory":1024}}}`)
		case "/api/protected_instance/open":
			if r.Method != "POST" {
				t.Error("mutation must use POST")
			}
			commands.Add(1)
			fmt.Fprint(w, `{"status":200,"data":true}`)
		case "/api/protected_instance/outputlog":
			fmt.Fprint(w, `{"status":200,"data":"[INFO] Done!"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer panel.Close()
	client := api.Client()
	base := api.URL
	status, b := request(t, client, base, "GET", "/api/isles/admin/config", nil, false)
	mustStatus(t, 401, status, b)
	connection := map[string]any{"name": "Panel", "kind": "mcsm", "url": panel.URL, "token": "panel-private-key"}
	status, b = request(t, client, base, "PUT", "/api/isles/admin/connections/main", connection, true)
	mustStatus(t, 200, status, b)
	if bytes.Contains(b, []byte("panel-private-key")) {
		t.Fatal("secret returned to admin")
	}
	status, b = request(t, client, base, "GET", "/api/isles/admin/connections/main/instances", nil, true)
	mustStatus(t, 200, status, b)
	var items []struct{ ID, DaemonID string }
	if err := json.Unmarshal(b, &items); err != nil || len(items) != 1 {
		t.Fatalf("instances: %s", b)
	}
	s := map[string]any{"name": "Survival", "address": "127.0.0.1:25565", "edition": "java", "connectionId": "main", "daemonId": items[0].DaemonID, "instanceId": items[0].ID, "published": false, "links": []any{}}
	status, b = request(t, client, base, "PUT", "/api/isles/admin/servers/survival", s, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, client, base, "GET", "/api/isles/servers/survival", nil, false)
	mustStatus(t, 404, status, b)
	s["published"] = true
	status, b = request(t, client, base, "PUT", "/api/isles/admin/servers/survival", s, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, client, base, "GET", "/api/isles/servers", nil, false)
	mustStatus(t, 200, status, b)
	for _, secret := range []string{"connectionId", "daemonId", "instanceId", "panel-private-key"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("public leak: %s", b)
		}
	}
	status, b = request(t, client, base, "POST", "/api/isles/admin/servers/survival/actions/start", nil, false)
	mustStatus(t, 401, status, b)
	status, b = request(t, client, base, "POST", "/api/isles/admin/servers/survival/actions/start", nil, true)
	mustStatus(t, 202, status, b)
	if commands.Load() != 1 {
		t.Fatal("unauthorized operation reached panel")
	}
	status, b = request(t, client, base, "GET", "/api/isles/admin/servers/survival/runtime", nil, true)
	mustStatus(t, 200, status, b)
	var runtime struct {
		State string
		CPU   float64 `json:"cpuPercent"`
	}
	_ = json.Unmarshal(b, &runtime)
	if runtime.State != "running" || runtime.CPU != 12.5 {
		t.Fatalf("runtime: %s", b)
	}
	status, b = request(t, client, base, "GET", "/api/isles/admin/servers/survival/logs", nil, true)
	mustStatus(t, 200, status, b)
	if !bytes.Contains(b, []byte("Done!")) {
		t.Fatal(string(b))
	}
	status, b = request(t, client, base, "DELETE", "/api/isles/admin/connections/main", nil, true)
	mustStatus(t, 409, status, b)
	disk, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte("panel-private-key")) {
		t.Fatal("plaintext secret on disk")
	}
	// Omitting the token preserves it across UI edits and process restarts.
	delete(connection, "token")
	connection["name"] = "Updated"
	status, b = request(t, client, base, "PUT", "/api/isles/admin/connections/main", connection, true)
	mustStatus(t, 200, status, b)
	reloaded, err := isles.Open(dir, token, key)
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(reloaded.Handler())
	defer other.Close()
	status, b = request(t, other.Client(), other.URL, "GET", "/api/isles/admin/connections/main/instances", nil, true)
	mustStatus(t, 200, status, b)
	manifest := []byte(`{"anonymousAdmin":401,"privateServer":404,"publicCredentials":false,"start":202,"runtime":"running","secretPersistedEncrypted":true}`)
	if err := os.WriteFile(filepath.Join(dir, "scenario-result.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGameProbeScenario(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var probes atomic.Int32
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				buffer := make([]byte, 512)
				if _, err := c.Read(buffer); err != nil {
					return
				}
				probes.Add(1)
				data := []byte(`{"version":{"name":"Paper"},"players":{"online":3,"max":20},"description":{"text":"Hello","extra":[{"text":" world"}]}}`)
				payload := append([]byte{0}, testVarint(len(data))...)
				payload = append(payload, data...)
				_, _ = c.Write(append(testVarint(len(payload)), payload...))
			}()
		}
	}()
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	s := map[string]any{"name": "Test", "address": listener.Addr().String(), "edition": "java", "published": true}
	status, b := request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/test", s, true)
	mustStatus(t, 200, status, b)
	for range 2 {
		status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers/test/status", nil, false)
		mustStatus(t, 200, status, b)
		var st struct {
			Reachable bool
			Players   int
			MOTD      string
		}
		_ = json.Unmarshal(b, &st)
		if !st.Reachable || st.Players != 3 || st.MOTD != "Hello world" {
			t.Fatalf("status: %s", b)
		}
	}
	if probes.Load() != 1 {
		t.Fatal("status requests bypassed shared cache")
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buf := make([]byte, 512)
		_, addr, err := udp.ReadFrom(buf)
		if err != nil {
			return
		}
		pong := append([]byte{0x1c}, buf[1:9]...)
		pong = append(pong, make([]byte, 8)...)
		pong = append(pong, buf[9:25]...)
		text := []byte("MCPE;Bedrock;776;1.21;4;10")
		pong = binary.BigEndian.AppendUint16(pong, uint16(len(text)))
		pong = append(pong, text...)
		_, _ = udp.WriteTo(pong, addr)
	}()
	s["address"] = udp.LocalAddr().String()
	s["edition"] = "bedrock"
	status, b = request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/test", s, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers/test/status", nil, false)
	mustStatus(t, 200, status, b)
	var st struct {
		Reachable bool
		Players   int
	}
	_ = json.Unmarshal(b, &st)
	if !st.Reachable || st.Players != 4 {
		t.Fatalf("bedrock: %s", b)
	}
}
func testVarint(n int) []byte {
	var b []byte
	for {
		v := byte(n & 127)
		n >>= 7
		if n != 0 {
			v |= 128
		}
		b = append(b, v)
		if n == 0 {
			return b
		}
	}
}

func TestPackPublicationScenario(t *testing.T) {
	mod := []byte("real boundary fixture bytes, hash checked before publication")
	hash := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	var sourceURL string
	var corrupt atomic.Bool
	config := []byte("difficulty=hard\n")
	meta := func() []byte {
		return []byte(fmt.Sprintf("name='Example'\nfilename='example.jar'\nside='client'\n[download]\nurl='%s/example.jar'\nhash-format='sha256'\nhash='%s'\n", sourceURL, hash(mod)))
	}
	index := func() []byte {
		return []byte(fmt.Sprintf("hash-format='sha256'\n[[files]]\nfile='mods/example.pw.toml'\nhash='%s'\nmetafile=true\n[[files]]\nfile='config/test.txt'\nhash='%s'\n", hash(meta()), hash(config)))
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pack.toml":
			fmt.Fprintf(w, "name='Private pack'\nversion='v1'\npack-format='packwiz:1.1.0'\n[index]\nfile='index.toml'\nhash-format='sha256'\nhash='%s'\n[versions]\nminecraft='1.21.1'\nfabric='0.16.9'\n", hash(index()))
		case "/index.toml":
			_, _ = w.Write(index())
		case "/mods/example.pw.toml":
			_, _ = w.Write(meta())
		case "/config/test.txt":
			_, _ = w.Write(config)
		case "/example.jar":
			if corrupt.Load() {
				fmt.Fprint(w, "corrupt")
			} else {
				_, _ = w.Write(mod)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer source.Close()
	sourceURL = source.URL
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	s := map[string]any{"name": "Pack server", "address": "localhost:25565", "edition": "java", "published": true, "packwizUrl": source.URL + "/pack.toml"}
	status, b := request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/pack", s, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/pack/publish-pack", nil, true)
	mustStatus(t, 200, status, b)
	var release struct{ Path string }
	if err := json.Unmarshal(b, &release); err != nil {
		t.Fatal(err)
	}
	status, b = request(t, api.Client(), api.URL, "GET", release.Path, nil, false)
	mustStatus(t, 200, status, b)
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	foundIndex, foundConfig := false, false
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		switch f.Name {
		case "overrides/config/test.txt":
			foundConfig = bytes.Equal(data, config)
		case "modrinth.index.json":
			var idx struct {
				Dependencies map[string]string
				Files        []struct {
					Path   string
					Env    map[string]string
					Hashes map[string]string
				}
			}
			if err := json.Unmarshal(data, &idx); err != nil {
				t.Fatal(err)
			}
			foundIndex = idx.Dependencies["fabric-loader"] == "0.16.9" && len(idx.Files) == 1 && idx.Files[0].Path == "mods/example.jar" && idx.Files[0].Env["server"] == "unsupported" && len(idx.Files[0].Hashes["sha512"]) == 128
		}
	}
	if !foundIndex || !foundConfig {
		t.Fatal("published pack lost dependency, physical side, hash or override")
	}
	corrupt.Store(true)
	status, b = request(t, api.Client(), api.URL, "POST", "/api/isles/admin/servers/pack/publish-pack", nil, true)
	mustStatus(t, 502, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers/pack", nil, false)
	mustStatus(t, 200, status, b)
	if !strings.Contains(string(b), strings.TrimPrefix(release.Path, "/api/isles/packs/")) {
		t.Fatal("failed publication replaced working release")
	}
}
