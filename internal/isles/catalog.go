package isles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CatalogProject struct {
	ID           string   `json:"id"`
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Type         string   `json:"projectType,omitempty"`
	IconURL      string   `json:"iconUrl,omitempty"`
	Source       string   `json:"source"`
	URL          string   `json:"url"`
	Downloads    int64    `json:"downloads,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
	GameVersions []string `json:"gameVersions,omitempty"`
}

type CatalogFile struct {
	Name     string            `json:"name"`
	URL      string            `json:"url"`
	Hashes   map[string]string `json:"hashes"`
	Size     int64             `json:"size"`
	Platform string            `json:"platform,omitempty"`
}

type CatalogVersion struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Number       string        `json:"number"`
	Channel      string        `json:"channel"`
	GameVersions []string      `json:"gameVersions"`
	Loaders      []string      `json:"loaders"`
	Files        []CatalogFile `json:"files"`
}

type catalogEntry struct {
	data    []byte
	expires time.Time
}
type catalogClient struct {
	mu       sync.Mutex
	modrinth string
	hangar   string
	cache    map[string]catalogEntry
	window   time.Time
	requests int
}

func (c *catalogClient) init() error {
	c.modrinth = strings.TrimRight(os.Getenv("ISLES_MODRINTH_URL"), "/")
	c.hangar = strings.TrimRight(os.Getenv("ISLES_HANGAR_URL"), "/")
	if c.modrinth == "" {
		c.modrinth = "https://api.modrinth.com/v2"
	}
	if c.hangar == "" {
		c.hangar = "https://hangar.papermc.io/api/v1"
	}
	for _, raw := range []string{c.modrinth, c.hangar} {
		if err := validURL(raw); err != nil {
			return errors.New("catalog origins must be absolute HTTP(S) URLs")
		}
		u, _ := url.Parse(raw)
		if u.RawQuery != "" {
			return errors.New("catalog origins must not contain queries")
		}
	}
	c.cache = map[string]catalogEntry{}
	return nil
}

func (a *App) catalogRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/isles/catalog/modrinth/search", a.modrinthSearch)
	m.HandleFunc("GET /api/isles/catalog/hangar/search", a.hangarSearch)
	m.HandleFunc("GET /api/isles/catalog/modrinth/versions/{project}", a.modrinthVersions)
	m.HandleFunc("GET /api/isles/catalog/hangar/versions/{owner}/{project}", a.hangarVersions)
}

var errCatalogLimit = errors.New("catalog request budget exhausted; try again in a minute")

// One bounded cache and admission owner for catalog calls; concurrent identical
// requests share the cached response. Only deployment-configured origins are used.
func (a *App) catalogHTTP(ctx context.Context, raw string, out any) error {
	c := &a.catalog
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.cache[raw]; ok && time.Now().Before(entry.expires) {
		return json.Unmarshal(entry.data, out)
	}
	if time.Since(c.window) >= time.Minute {
		c.window = time.Now()
		c.requests = 0
	}
	if c.requests >= 60 {
		return errCatalogLimit
	}
	c.requests++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return errors.New("invalid catalog request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mythicove/Isles/0.1 (https://github.com/Mythicove/mythic-isles)")
	res, err := a.http.Do(req)
	if err != nil {
		return errors.New("catalog source unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("catalog source returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return errors.New("catalog response too large or unreadable")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("invalid catalog response")
	}
	for key, entry := range c.cache {
		if time.Now().After(entry.expires) {
			delete(c.cache, key)
		}
	}
	if len(c.cache) >= 16 {
		for key := range c.cache {
			delete(c.cache, key)
			break
		}
	}
	c.cache[raw] = catalogEntry{data: data, expires: time.Now().Add(time.Minute)}
	return nil
}

func catalogError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCatalogLimit) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	upstreamError(w, err)
}

func catalogFilters(r *http.Request) (url.Values, error) {
	q := r.URL.Query()
	for name, values := range q {
		if len(values) != 1 || !slices.Contains([]string{"q", "limit", "loader", "game_version", "platform"}, name) {
			return nil, errors.New("unknown or repeated catalog parameter")
		}
	}
	for name, max := range map[string]int{"q": 120, "loader": 64, "game_version": 32, "platform": 32} {
		if len([]rune(q.Get(name))) > max {
			return nil, fmt.Errorf("%s is too long", name)
		}
	}
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			return nil, errors.New("limit must be between 1 and 50")
		}
		limit = n
	}
	q.Set("q", strings.TrimSpace(q.Get("q")))
	q.Set("limit", strconv.Itoa(limit))
	return q, nil
}

func (a *App) modrinthSearch(w http.ResponseWriter, r *http.Request) {
	q, err := catalogFilters(r)
	if err != nil {
		bad(w, err)
		return
	}
	if q.Get("q") == "" {
		bad(w, errors.New("query is required"))
		return
	}
	params := url.Values{"query": {q.Get("q")}, "limit": {q.Get("limit")}}
	facets := [][]string{}
	if loader := q.Get("loader"); loader != "" {
		facets = append(facets, []string{"categories:" + loader})
	}
	if version := q.Get("game_version"); version != "" {
		facets = append(facets, []string{"versions:" + version})
	}
	if len(facets) > 0 {
		data, _ := json.Marshal(facets)
		params.Set("facets", string(data))
	}
	var response struct {
		Hits []struct {
			ID          string   `json:"project_id"`
			Slug        string   `json:"slug"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Type        string   `json:"project_type"`
			Icon        string   `json:"icon_url"`
			Downloads   int64    `json:"downloads"`
			Categories  []string `json:"categories"`
			Versions    []string `json:"versions"`
			Loaders     []string `json:"loaders"`
		} `json:"hits"`
	}
	if err := a.catalogHTTP(r.Context(), a.catalog.modrinth+"/search?"+params.Encode(), &response); err != nil {
		catalogError(w, err)
		return
	}
	if response.Hits == nil {
		upstreamError(w, errors.New("catalog returned no hit collection"))
		return
	}
	items := make([]CatalogProject, 0, len(response.Hits))
	for _, hit := range response.Hits {
		items = append(items, CatalogProject{ID: hit.ID, Slug: hit.Slug, Title: hit.Title, Description: hit.Description, Type: hit.Type, IconURL: hit.Icon, Source: "modrinth", URL: "https://modrinth.com/project/" + url.PathEscape(hit.ID), Downloads: hit.Downloads, Categories: hit.Categories, Loaders: hit.Loaders, GameVersions: hit.Versions})
	}
	reply(w, items)
}

func (a *App) hangarSearch(w http.ResponseWriter, r *http.Request) {
	q, err := catalogFilters(r)
	if err != nil {
		bad(w, err)
		return
	}
	if q.Get("q") == "" {
		bad(w, errors.New("query is required"))
		return
	}
	params := url.Values{"query": {q.Get("q")}, "limit": {q.Get("limit")}, "offset": {"0"}}
	if platform := q.Get("platform"); platform != "" {
		params.Set("platform", platform)
	}
	var response struct {
		Result []struct {
			Name      string `json:"name"`
			Namespace struct {
				Owner string `json:"owner"`
				Slug  string `json:"slug"`
			} `json:"namespace"`
			Description string `json:"description"`
			Category    string `json:"category"`
			AvatarURL   string `json:"avatarUrl"`
			Stats       struct {
				Downloads int64 `json:"downloads"`
			} `json:"stats"`
			Platforms map[string][]string `json:"supportedPlatforms"`
		} `json:"result"`
	}
	if err := a.catalogHTTP(r.Context(), a.catalog.hangar+"/projects?"+params.Encode(), &response); err != nil {
		catalogError(w, err)
		return
	}
	if response.Result == nil {
		upstreamError(w, errors.New("catalog returned no project collection"))
		return
	}
	items := make([]CatalogProject, 0, len(response.Result))
	for _, hit := range response.Result {
		loaders := []string{}
		versions := []string{}
		for loader, v := range hit.Platforms {
			loaders = append(loaders, loader)
			versions = append(versions, v...)
		}
		sort.Strings(loaders)
		sort.Strings(versions)
		versions = slices.Compact(versions)
		items = append(items, CatalogProject{ID: hit.Namespace.Owner + "/" + hit.Namespace.Slug, Slug: hit.Namespace.Slug, Title: hit.Name, Description: hit.Description, Type: hit.Category, IconURL: hit.AvatarURL, Source: "hangar", URL: "https://hangar.papermc.io/" + url.PathEscape(hit.Namespace.Owner) + "/" + url.PathEscape(hit.Namespace.Slug), Downloads: hit.Stats.Downloads, Loaders: loaders, GameVersions: versions})
	}
	reply(w, items)
}

func (a *App) modrinthVersions(w http.ResponseWriter, r *http.Request) {
	q, err := catalogFilters(r)
	if err != nil {
		bad(w, err)
		return
	}
	params := url.Values{}
	for param, upstream := range map[string]string{"loader": "loaders", "game_version": "game_versions"} {
		if value := q.Get(param); value != "" {
			b, _ := json.Marshal([]string{value})
			params.Set(upstream, string(b))
		}
	}
	var versions []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Number   string   `json:"version_number"`
		Channel  string   `json:"version_type"`
		Versions []string `json:"game_versions"`
		Loaders  []string `json:"loaders"`
		Files    []struct {
			Name   string            `json:"filename"`
			URL    string            `json:"url"`
			Hashes map[string]string `json:"hashes"`
			Size   int64             `json:"size"`
		} `json:"files"`
	}
	if err := a.catalogHTTP(r.Context(), a.catalog.modrinth+"/project/"+url.PathEscape(r.PathValue("project"))+"/version?"+params.Encode(), &versions); err != nil {
		catalogError(w, err)
		return
	}
	items := []CatalogVersion{}
	limit, _ := strconv.Atoi(q.Get("limit"))
	for _, v := range versions {
		if len(items) >= limit {
			break
		}
		files := []CatalogFile{}
		for _, f := range v.Files {
			if validURL(f.URL) == nil {
				files = append(files, CatalogFile{Name: f.Name, URL: f.URL, Hashes: f.Hashes, Size: f.Size})
			}
		}
		items = append(items, CatalogVersion{ID: v.ID, Name: v.Name, Number: v.Number, Channel: v.Channel, GameVersions: v.Versions, Loaders: v.Loaders, Files: files})
	}
	reply(w, items)
}

func (a *App) hangarVersions(w http.ResponseWriter, r *http.Request) {
	q, err := catalogFilters(r)
	if err != nil {
		bad(w, err)
		return
	}
	params := url.Values{"limit": {q.Get("limit")}, "offset": {"0"}}
	if platform := q.Get("platform"); platform != "" {
		params.Set("platform", platform)
	}
	var response struct {
		Result []struct {
			Name    string `json:"name"`
			Channel struct {
				Name string `json:"name"`
			} `json:"channel"`
			Platforms map[string][]string `json:"platformDependencies"`
			Downloads map[string]struct {
				File struct {
					Name string `json:"name"`
					Size int64  `json:"sizeBytes"`
					Hash string `json:"sha256Hash"`
				} `json:"fileInfo"`
				URL string `json:"downloadUrl"`
			} `json:"downloads"`
		} `json:"result"`
	}
	// Hangar accepts the project slug as a shorthand, but owner/slug avoids ambiguity.
	project := url.PathEscape(r.PathValue("owner")) + "/" + url.PathEscape(r.PathValue("project"))
	if err := a.catalogHTTP(r.Context(), a.catalog.hangar+"/projects/"+project+"/versions?"+params.Encode(), &response); err != nil {
		catalogError(w, err)
		return
	}
	if response.Result == nil {
		upstreamError(w, errors.New("catalog returned no version collection"))
		return
	}
	items := []CatalogVersion{}
	for _, v := range response.Result {
		files := []CatalogFile{}
		loaders := []string{}
		versions := []string{}
		for loader, vv := range v.Platforms {
			if q.Get("platform") != "" && loader != q.Get("platform") {
				continue
			}
			if q.Get("game_version") != "" && !slices.Contains(vv, q.Get("game_version")) {
				continue
			}
			loaders = append(loaders, loader)
			versions = append(versions, vv...)
			f, ok := v.Downloads[loader]
			if ok && validURL(f.URL) == nil {
				files = append(files, CatalogFile{Name: f.File.Name, URL: f.URL, Hashes: map[string]string{"sha256": f.File.Hash}, Size: f.File.Size, Platform: loader})
			}
		}
		if len(loaders) == 0 {
			continue
		}
		sort.Strings(loaders)
		sort.Strings(versions)
		versions = slices.Compact(versions)
		sort.Slice(files, func(i, j int) bool { return files[i].Platform < files[j].Platform })
		items = append(items, CatalogVersion{ID: v.Name, Name: v.Name, Number: v.Name, Channel: v.Channel.Name, GameVersions: versions, Loaders: loaders, Files: files})
	}
	reply(w, items)
}
