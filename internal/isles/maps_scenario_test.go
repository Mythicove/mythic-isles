package isles_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

func TestMapConfigurationAndPublicVisibility(t *testing.T) {
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	s := map[string]any{"name": "Map world", "address": "localhost:25565", "edition": "java", "published": true, "links": []any{}}
	for _, kind := range []string{"bluemap", "squaremap", "dynmap"} {
		s["map"] = map[string]string{"kind": kind, "url": "https://map.example.org/world/?view=flat#saved-camera"}
		status, b := request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/map", s, true)
		mustStatus(t, 200, status, b)
		status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers/map", nil, false)
		mustStatus(t, 200, status, b)
		var public isles.Server
		if err := json.Unmarshal(b, &public); err != nil || public.Map == nil || public.Map.Kind != kind || public.Map.URL != "https://map.example.org/world/?view=flat#saved-camera" {
			t.Fatalf("map identity or saved camera lost: %s", b)
		}
	}
	for _, url := range []string{"javascript:alert(1)", "https://username:password@map.example.org/", "file:///private/map.html"} {
		s["map"] = map[string]string{"kind": "bluemap", "url": url}
		status, b := request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/map", s, true)
		mustStatus(t, 400, status, b)
	}
	s["map"] = map[string]string{"kind": "unknown", "url": "https://map.example.org/"}
	status, b := request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/map", s, true)
	mustStatus(t, 400, status, b)
	s["map"] = map[string]string{"kind": "bluemap", "url": "https://map.example.org/"}
	s["published"] = false
	status, b = request(t, api.Client(), api.URL, "PUT", "/api/isles/admin/servers/map", s, true)
	mustStatus(t, 200, status, b)
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/servers/map", nil, false)
	mustStatus(t, 404, status, b)
}
