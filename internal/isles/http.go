package isles

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	a.bridgeRoutes(m)
	a.catalogRoutes(m)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	m.HandleFunc("GET /metrics", a.prometheus)
	m.HandleFunc("GET /api/isles/servers", a.listPublic)
	m.HandleFunc("GET /api/isles/servers/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.server(r.PathValue("id"), true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(w, publicServer(s))
	})
	m.HandleFunc("GET /api/isles/servers/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.server(r.PathValue("id"), true)
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(w, a.probe(r.Context(), s))
	})
	m.HandleFunc("GET /api/isles/packs/{version}", a.downloadPack)
	m.HandleFunc("/api/isles/admin/", func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a.adminMux().ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		m.ServeHTTP(w, r)
	})
}

func (a *App) listPublic(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	servers := []Server{}
	for _, s := range a.config.Servers {
		if s.Published {
			servers = append(servers, publicServer(s))
		}
	}
	reply(w, servers)
}

func (a *App) adminMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/isles/admin/config", func(w http.ResponseWriter, _ *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		cs := []Connection{}
		for _, c := range a.config.Connections {
			cs = append(cs, c.Connection)
		}
		reply(w, map[string]any{"servers": a.config.Servers, "connections": cs})
	})
	m.HandleFunc("PUT /api/isles/admin/servers/{id}", a.putServer)
	m.HandleFunc("DELETE /api/isles/admin/servers/{id}", a.deleteServer)
	m.HandleFunc("PUT /api/isles/admin/connections/{id}", a.putConnection)
	m.HandleFunc("DELETE /api/isles/admin/connections/{id}", a.deleteConnection)
	m.HandleFunc("GET /api/isles/admin/connections/{id}/instances", a.instances)
	m.HandleFunc("GET /api/isles/admin/connections/{id}/metrics", a.metrics)
	m.HandleFunc("GET /api/isles/admin/servers/{id}/runtime", a.runtime)
	m.HandleFunc("POST /api/isles/admin/servers/{id}/actions/{action}", a.action)
	m.HandleFunc("GET /api/isles/admin/servers/{id}/logs", a.logs)
	m.HandleFunc("POST /api/isles/admin/servers/{id}/publish-pack", a.publishPack)
	m.HandleFunc("POST /api/isles/admin/servers/{id}/bridge-token", a.rotateBridge)
	m.HandleFunc("GET /api/isles/admin/servers/{id}/bridge", a.bridgeState)
	m.HandleFunc("GET /api/isles/admin/servers/{id}/tasks", a.listTasks)
	m.HandleFunc("POST /api/isles/admin/servers/{id}/tasks", a.enqueueTask)
	m.HandleFunc("POST /api/isles/admin/servers/{id}/chunky", a.enqueueChunky)
	m.HandleFunc("GET /api/isles/admin/servers/{id}/identities", a.gameIdentities)
	return m
}

func (a *App) putServer(w http.ResponseWriter, r *http.Request) {
	var s Server
	if err := decode(r, &s); err != nil {
		bad(w, err)
		return
	}
	s.ID = r.PathValue("id")
	if err := validateServer(s); err != nil {
		bad(w, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s.ConnectionID != "" && !slices.ContainsFunc(a.config.Connections, func(c storedConnection) bool { return c.ID == s.ConnectionID && c.Kind != "prometheus" }) {
		bad(w, errors.New("select an existing panel connection"))
		return
	}
	if s.ConnectionID != "" && s.InstanceID == "" {
		bad(w, errors.New("select an instance"))
		return
	}
	// Published releases are selected by the publish operation, never by arbitrary paths.
	s.PackVersion = ""
	servers := slices.Clone(a.config.Servers)
	i := slices.IndexFunc(servers, func(v Server) bool { return v.ID == s.ID })
	if i >= 0 {
		s.PackVersion = servers[i].PackVersion
		servers[i] = s
	} else {
		if len(servers) >= 100 {
			bad(w, errors.New("at most 100 servers"))
			return
		}
		servers = append(servers, s)
	}
	next := a.config
	next.Servers = servers
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	delete(a.probes, s.ID)
	a.audit(r, "server.save", s.ID)
	reply(w, s)
}

func (a *App) deleteServer(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.config
	id := r.PathValue("id")
	next.BridgeSecrets = maps.Clone(next.BridgeSecrets)
	delete(next.BridgeSecrets, id)
	next.Tasks = maps.Clone(next.Tasks)
	delete(next.Tasks, id)
	next.GameLinks = maps.Clone(next.GameLinks)
	for player, links := range next.GameLinks {
		if _, ok := links[id]; ok {
			copy := maps.Clone(links)
			delete(copy, id)
			next.GameLinks[player] = copy
		}
	}
	next.Servers = slices.DeleteFunc(slices.Clone(next.Servers), func(s Server) bool { return s.ID == r.PathValue("id") })
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	delete(a.probes, r.PathValue("id"))
	delete(a.bridges, id)
	a.audit(r, "server.delete", r.PathValue("id"))
	w.WriteHeader(204)
}

func (a *App) putConnection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Connection
		Token *string `json:"token"`
	}
	if err := decode(r, &input); err != nil {
		bad(w, err)
		return
	}
	c := input.Connection
	c.ID = r.PathValue("id")
	if !idPattern.MatchString(c.ID) || strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 || !slices.Contains([]string{"mcsm", "pterodactyl", "prometheus"}, c.Kind) {
		bad(w, errors.New("invalid connection"))
		return
	}
	if err := validURL(c.URL); err != nil {
		bad(w, err)
		return
	}
	u := strings.SplitN(c.URL, "?", 2)
	if len(u) != 1 {
		bad(w, errors.New("connection URL must not contain a query"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cs := slices.Clone(a.config.Connections)
	i := slices.IndexFunc(cs, func(v storedConnection) bool { return v.ID == c.ID })
	if i >= 0 {
		c.Secret = cs[i].Secret
	}
	if input.Token != nil {
		c.Secret = *input.Token
	}
	if len(c.Secret) > 8192 {
		bad(w, errors.New("token too long"))
		return
	}
	if c.Kind != "prometheus" && c.Secret == "" {
		bad(w, errors.New("panel API token required"))
		return
	}
	c.Configured = c.Secret != ""
	stored := storedConnection{Connection: c, Ciphertext: a.encrypt(c.Secret)}
	if i >= 0 {
		cs[i] = stored
	} else {
		if len(cs) >= 30 {
			bad(w, errors.New("at most 30 connections"))
			return
		}
		cs = append(cs, stored)
	}
	next := a.config
	next.Connections = cs
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	a.audit(r, "connection.save", c.ID)
	reply(w, c)
}

func (a *App) deleteConnection(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := r.PathValue("id")
	if slices.ContainsFunc(a.config.Servers, func(s Server) bool { return s.ConnectionID == id }) {
		http.Error(w, "connection is in use", 409)
		return
	}
	next := a.config
	next.Connections = slices.DeleteFunc(slices.Clone(next.Connections), func(c storedConnection) bool { return c.ID == id })
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	a.audit(r, "connection.delete", id)
	w.WriteHeader(204)
}

func (a *App) downloadPack(w http.ResponseWriter, r *http.Request) {
	v := r.PathValue("version")
	if !releasePattern.MatchString(v) {
		http.NotFound(w, r)
		return
	}
	// Releases are public only while attached to a published server.
	a.mu.Lock()
	visible := slices.ContainsFunc(a.config.Servers, func(s Server) bool { return s.Published && s.PackVersion == v })
	a.mu.Unlock()
	if !visible {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(a.dir, "packs", v+".mrpack"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		failed(w)
		return
	}
	w.Header().Set("Content-Type", "application/x-modrinth-modpack+zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+v+`.mrpack"`)
	http.ServeContent(w, r, v+".mrpack", st.ModTime(), f)
}

func (a *App) audit(r *http.Request, action, target string) {
	actor := r.Header.Get("X-Isles-Actor")
	if len(actor) > 150 {
		actor = actor[:150]
	}
	if actor == "" {
		actor = "service-token"
	}
	slog.Info("admin", "actor", actor, "action", action, "target", target)
}

func decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid JSON request")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func bad(w http.ResponseWriter, err error) { http.Error(w, err.Error(), 400) }
func failed(w http.ResponseWriter)         { http.Error(w, "operation failed", 500) }
