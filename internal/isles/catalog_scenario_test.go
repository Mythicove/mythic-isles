package isles_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

// External catalog servers use the documented deployment-origin settings. All
// requests enter the real Isles HTTP router, parser, cache and catalog client.
func TestCatalogDiscoveryAndVersionsScenario(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.Contains(r.Header.Get("User-Agent"), "Mythicove/") {
			t.Error("catalog client is not identified")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("service credential leaked to catalog")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/modrinth/search":
			if r.URL.Query().Get("query") == "Fabric" {
				var facets [][]string
				if err := json.Unmarshal([]byte(r.URL.Query().Get("facets")), &facets); err != nil || len(facets) != 2 || facets[0][0] != "categories:fabric" || facets[1][0] != "versions:1.21.1" {
					t.Errorf("both filters must survive as AND facets: %s", r.URL.RawQuery)
				}
			}
			if r.URL.Query().Get("query") == "broken" {
				fmt.Fprint(w, `{invalid`)
				return
			}
			fmt.Fprint(w, `{"hits":[{"project_id":"fabric-api","slug":"fabric-api","title":"Fabric API","project_type":"mod","versions":["1.21.1"],"categories":["fabric"],"downloads":42}]}`)
		case "/modrinth/project/fabric-api/version":
			if r.URL.Query().Get("loaders") != `["fabric"]` {
				t.Error("version loader missing")
			}
			fmt.Fprint(w, `[{"id":"release-one","name":"Fabric release","version_number":"1.0","version_type":"release","game_versions":["1.21.1"],"loaders":["fabric"],"files":[{"filename":"fabric.jar","url":"https://cdn.modrinth.com/fabric.jar","hashes":{"sha512":"abcd"},"size":100}]}]`)
		case "/hangar/projects":
			if r.URL.Query().Get("platform") != "PAPER" {
				t.Error("Hangar platform missing")
			}
			fmt.Fprint(w, `{"result":[{"name":"ViaVersion","namespace":{"owner":"ViaVersion","slug":"ViaVersion"},"description":"Protocol compatibility","category":"admin_tools","stats":{"downloads":20},"supportedPlatforms":{"PAPER":["1.21.1"],"VELOCITY":["3.4"]}}]}`)
		case "/hangar/projects/ViaVersion/ViaVersion/versions":
			fmt.Fprint(w, `{"result":[{"name":"5.0.0","channel":{"name":"Release"},"platformDependencies":{"PAPER":["1.21.1"],"VELOCITY":["3.4"]},"downloads":{"PAPER":{"fileInfo":{"name":"ViaVersion.jar","sizeBytes":120,"sha256Hash":"def0"},"downloadUrl":"https://hangarcdn.papermc.io/ViaVersion.jar"},"VELOCITY":{"fileInfo":{"name":"ViaVersion.jar","sizeBytes":120,"sha256Hash":"def0"},"downloadUrl":"https://hangarcdn.papermc.io/ViaVersion.jar"}}}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	t.Setenv("ISLES_MODRINTH_URL", upstream.URL+"/modrinth")
	t.Setenv("ISLES_HANGAR_URL", upstream.URL+"/hangar")
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	path := "/api/isles/catalog/modrinth/search?q=Fabric&loader=fabric&game_version=1.21.1&limit=3"
	status, b := request(t, api.Client(), api.URL, "GET", path, nil, false)
	mustStatus(t, 200, status, b)
	var projects []isles.CatalogProject
	if err := json.Unmarshal(b, &projects); err != nil || len(projects) != 1 || projects[0].Source != "modrinth" || projects[0].ID != "fabric-api" {
		t.Fatalf("projects: %s", b)
	}
	status, b = request(t, api.Client(), api.URL, "GET", path, nil, false)
	mustStatus(t, 200, status, b)
	if calls.Load() != 1 {
		t.Fatal("identical searches did not share cache")
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/modrinth/versions/fabric-api?loader=fabric&game_version=1.21.1", nil, false)
	mustStatus(t, 200, status, b)
	var versions []isles.CatalogVersion
	if err := json.Unmarshal(b, &versions); err != nil || len(versions) != 1 || versions[0].Files[0].Hashes["sha512"] != "abcd" {
		t.Fatalf("version hash/provenance missing: %s", b)
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/hangar/search?q=ViaVersion&platform=PAPER", nil, false)
	mustStatus(t, 200, status, b)
	if err := json.Unmarshal(b, &projects); err != nil || projects[0].ID != "ViaVersion/ViaVersion" || projects[0].Source != "hangar" {
		t.Fatalf("Hangar identity: %s", b)
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/hangar/versions/ViaVersion/ViaVersion?platform=PAPER&game_version=1.21.1", nil, false)
	mustStatus(t, 200, status, b)
	if err := json.Unmarshal(b, &versions); err != nil || len(versions) != 1 || len(versions[0].Loaders) != 1 || versions[0].Files[0].Platform != "PAPER" || versions[0].Files[0].Hashes["sha256"] != "def0" {
		t.Fatalf("Hangar platform/hash: %s", b)
	}
	before := calls.Load()
	for _, query := range []string{"?q=x&limit=abc", "?q=x&limit=51", "?q=x&q=y", "?q=x&url=http://untrusted.example", "?q="} {
		status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/modrinth/search"+query, nil, false)
		mustStatus(t, 400, status, b)
	}
	if calls.Load() != before {
		t.Fatal("invalid input reached upstream")
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/modrinth/search?q=broken", nil, false)
	mustStatus(t, 502, status, b)
	if strings.Contains(string(b), upstream.URL) {
		t.Fatal("private origin leaked in error")
	}
}

func TestCatalogResponseAndAdmissionBounds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "large" {
			fmt.Fprint(w, strings.Repeat(" ", (2<<20)+1))
			return
		}
		fmt.Fprint(w, `{"hits":[]}`)
	}))
	defer upstream.Close()
	t.Setenv("ISLES_MODRINTH_URL", upstream.URL)
	app, err := isles.Open(t.TempDir(), token, key)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	status, b := request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/modrinth/search?q=large", nil, false)
	mustStatus(t, 502, status, b)
	for i := 0; i < 59; i++ {
		status, b = request(t, api.Client(), api.URL, "GET", fmt.Sprintf("/api/isles/catalog/modrinth/search?q=unique%d", i), nil, false)
		mustStatus(t, 200, status, b)
	}
	status, b = request(t, api.Client(), api.URL, "GET", "/api/isles/catalog/modrinth/search?q=over-budget", nil, false)
	mustStatus(t, 429, status, b)
}
