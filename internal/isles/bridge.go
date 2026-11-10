package isles

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

type GameLink struct {
	UUID     string    `json:"uuid"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	LinkedAt time.Time `json:"linkedAt"`
}
type PlayerSnapshot struct {
	UUID             string            `json:"uuid"`
	Name             string            `json:"name"`
	Group            string            `json:"group,omitempty"`
	Groups           []string          `json:"groups,omitempty"`
	Values           map[string]string `json:"values,omitempty"`
	ClientVersion    string            `json:"clientVersion,omitempty"`
	Regions          []GameRegion      `json:"regions,omitempty"`
	RegionsTruncated bool              `json:"regionsTruncated,omitempty"`
	Economy          *GameEconomy      `json:"economy,omitempty"`
	Connection       *GameConnection   `json:"connection,omitempty"`
	Voicechat        *GameVoicechat    `json:"voicechat,omitempty"`
}
type GameVoicechat struct {
	Installed *bool `json:"installed"`
	Connected *bool `json:"connected"`
	Disabled  *bool `json:"disabled"`
}
type GameEconomy struct {
	Provider  string  `json:"provider"`
	Balance   float64 `json:"balance"`
	Formatted string  `json:"formatted"`
	Currency  string  `json:"currency"`
}
type GameRegion struct {
	ID      string `json:"id"`
	World   string `json:"world"`
	Role    string `json:"role"`
	CenterX int    `json:"centerX"`
	CenterY int    `json:"centerY"`
	CenterZ int    `json:"centerZ"`
}
type BridgeSnapshot struct {
	Platform         string             `json:"platform"`
	Minecraft        string             `json:"minecraft"`
	Plugins          map[string]string  `json:"plugins"`
	Groups           []string           `json:"groups"`
	TPS              *float64           `json:"tps,omitempty"`
	MSPT             *float64           `json:"mspt,omitempty"`
	CPU              *float64           `json:"cpu,omitempty"`
	Players          []PlayerSnapshot   `json:"players"`
	ReceivedAt       time.Time          `json:"receivedAt"`
	ProtocolVersions []string           `json:"protocolVersions,omitempty"`
	Chunky           []ChunkyWorld      `json:"chunky,omitempty"`
	Plan             *PlanSnapshot      `json:"plan,omitempty"`
	Towny            *TownySnapshot     `json:"towny,omitempty"`
	Crossplay        *CrossplaySnapshot `json:"crossplay,omitempty"`
}
type pairing struct {
	Player  string
	Server  string
	Expires time.Time
}
type BridgeTask struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	UUID      string           `json:"uuid"`
	Group     string           `json:"group,omitempty"`
	Grant     bool             `json:"grant"`
	Status    string           `json:"status"`
	Error     string           `json:"error,omitempty"`
	CreatedAt time.Time        `json:"createdAt"`
	Chunky    *ChunkyOperation `json:"chunky,omitempty"`
}

func randomID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }

func (a *App) bridgeAuth(r *http.Request, id string) bool {
	a.mu.Lock()
	ciphertext := a.config.BridgeSecrets[id]
	a.mu.Unlock()
	if ciphertext == "" {
		return false
	}
	secret, err := a.decrypt(ciphertext)
	return err == nil && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+secret)) == 1
}

func (a *App) bridgeRoutes(m *http.ServeMux) {
	m.HandleFunc("/api/isles/bridge/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !a.bridgeAuth(r, r.PathValue("id")) {
			http.Error(w, "unauthorized", 401)
			return
		}
		sub := http.NewServeMux()
		sub.HandleFunc("POST /api/isles/bridge/{id}/heartbeat", a.heartbeat)
		sub.HandleFunc("POST /api/isles/bridge/{id}/link", a.linkGame)
		sub.HandleFunc("GET /api/isles/bridge/{id}/tasks", a.bridgeTasks)
		sub.HandleFunc("GET /api/isles/bridge/{id}/identities", a.bridgeIdentities)
		sub.HandleFunc("POST /api/isles/bridge/{id}/tasks/{task}", a.finishTask)
		sub.HandleFunc("POST /api/isles/bridge/{id}/tasks/{task}/claim", a.claimTask)
		sub.ServeHTTP(w, r)
	})
	m.HandleFunc("/api/isles/players/{player}/", func(w http.ResponseWriter, r *http.Request) {
		// The BFF first verifies ownership using Mirror's player API. It supplies
		// the chosen player in a header; browsers never receive this service token.
		if !a.authorized(r) || r.Header.Get("X-Isles-Player") != r.PathValue("player") {
			http.Error(w, "unauthorized", 401)
			return
		}
		sub := http.NewServeMux()
		sub.HandleFunc("GET /api/isles/players/{player}/links", a.playerLinks)
		sub.HandleFunc("POST /api/isles/players/{player}/pair", a.createPair)
		sub.ServeHTTP(w, r)
	})
}

func (a *App) rotateBridge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.server(id, false); !ok {
		http.NotFound(w, r)
		return
	}
	secret := randomID() + randomID()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.config
	next.BridgeSecrets = maps.Clone(next.BridgeSecrets)
	if next.BridgeSecrets == nil {
		next.BridgeSecrets = map[string]string{}
	}
	next.BridgeSecrets[id] = a.encrypt(secret)
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	delete(a.bridges, id)
	a.audit(r, "bridge.rotate", id)
	reply(w, map[string]string{"token": secret})
}

func (a *App) heartbeat(w http.ResponseWriter, r *http.Request) {
	var snapshot BridgeSnapshot
	if err := decode(r, &snapshot); err != nil {
		bad(w, err)
		return
	}
	if len(snapshot.Players) > 2000 || len(snapshot.Plugins) > 200 || len(snapshot.Groups) > 500 || len(snapshot.ProtocolVersions) > 256 || len(snapshot.Platform) > 120 || len(snapshot.Minecraft) > 120 {
		bad(w, errors.New("snapshot too large"))
		return
	}
	if err := validateChunkySnapshot(snapshot.Chunky); err != nil {
		bad(w, err)
		return
	}
	if err := validatePlanSnapshot(snapshot.Plan); err != nil {
		bad(w, err)
		return
	}
	if err := validateTownySnapshot(snapshot.Towny); err != nil {
		bad(w, err)
		return
	}
	if snapshot.Crossplay != nil && (snapshot.Crossplay.JavaVersion == "" || len(snapshot.Crossplay.JavaVersion) > 120 || len(snapshot.Crossplay.BedrockVersions) == 0 || len(snapshot.Crossplay.BedrockVersions) > 64) {
		bad(w, errors.New("invalid crossplay versions"))
		return
	}
	if snapshot.Crossplay != nil {
		for _, version := range snapshot.Crossplay.BedrockVersions {
			if version == "" || len(version) > 120 {
				bad(w, errors.New("invalid Bedrock version"))
				return
			}
		}
	}
	for _, p := range snapshot.Players {
		if p.Voicechat != nil && (p.Voicechat.Installed == nil || p.Voicechat.Connected == nil || p.Voicechat.Disabled == nil) {
			bad(w, errors.New("incomplete voice observation"))
			return
		}
		if p.Connection != nil && (p.Connection.Edition != "bedrock" || !slices.Contains([]string{"geyser", "floodgate"}, p.Connection.Provider) || p.Connection.Version == "" || len(p.Connection.Version) > 120) {
			bad(w, errors.New("invalid game connection"))
			return
		}
		if p.Economy != nil && (len(p.Economy.Provider) > 120 || len(p.Economy.Formatted) > 256 || len(p.Economy.Currency) > 120) {
			bad(w, errors.New("invalid economy observation"))
			return
		}
		if len(p.UUID) > 64 || len(p.Name) > 64 || len(p.Values) > 40 || len(p.ClientVersion) > 120 || len(p.Regions) > 32 {
			bad(w, errors.New("invalid player snapshot"))
			return
		}
		for _, region := range p.Regions {
			if len(region.ID) > 256 || len(region.World) > 256 || (region.Role != "owner" && region.Role != "member") {
				bad(w, errors.New("invalid region observation"))
				return
			}
		}
		for k, v := range p.Values {
			if len(k) > 100 || len(v) > 2000 {
				bad(w, errors.New("player value too long"))
				return
			}
		}
	}
	snapshot.ReceivedAt = time.Now().UTC()
	a.mu.Lock()
	a.bridges[r.PathValue("id")] = snapshot
	a.mu.Unlock()
	w.WriteHeader(204)
}

func (a *App) bridgeState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	snapshot, ok := a.bridges[r.PathValue("id")]
	a.mu.Unlock()
	if !ok {
		http.Error(w, "bridge has not connected", 409)
		return
	}
	reply(w, map[string]any{"snapshot": snapshot, "fresh": time.Since(snapshot.ReceivedAt) < 90*time.Second})
}

func (a *App) createPair(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Server string `json:"server"`
	}
	if err := decode(r, &input); err != nil {
		bad(w, err)
		return
	}
	if _, ok := a.server(input.Server, true); !ok {
		http.NotFound(w, r)
		return
	}
	player := r.PathValue("player")
	if len(player) > 64 || len(player) < 1 {
		bad(w, errors.New("invalid player"))
		return
	}
	code := strings.ToUpper(randomID()[:12])
	expires := time.Now().Add(10 * time.Minute)
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, p := range a.pairings {
		if time.Now().After(p.Expires) || p.Player == player {
			delete(a.pairings, k)
		}
	}
	if len(a.pairings) >= 4096 {
		http.Error(w, "pairing capacity reached", 429)
		return
	}
	a.pairings[code] = pairing{Player: player, Server: input.Server, Expires: expires}
	reply(w, map[string]any{"code": code, "expiresAt": expires, "command": "/mythic link " + code})
}

func (a *App) linkGame(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code     string `json:"code"`
		UUID     string `json:"uuid"`
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if err := decode(r, &input); err != nil {
		bad(w, err)
		return
	}
	if len(input.UUID) < 16 || len(input.UUID) > 64 || len(input.Name) > 64 || !slices.Contains([]string{"java", "bedrock"}, input.Platform) {
		bad(w, errors.New("invalid identity"))
		return
	}
	id := r.PathValue("id")
	a.mu.Lock()
	defer a.mu.Unlock()
	code := strings.ToUpper(input.Code)
	pair, ok := a.pairings[code]
	if !ok || pair.Server != id || time.Now().After(pair.Expires) {
		http.Error(w, "pairing code invalid or expired", 403)
		return
	}
	for player, links := range a.config.GameLinks {
		if player != pair.Player && links[id].UUID == input.UUID {
			http.Error(w, "game identity already linked", 409)
			return
		}
	}
	next := a.config
	next.GameLinks = maps.Clone(next.GameLinks)
	if next.GameLinks == nil {
		next.GameLinks = map[string]map[string]GameLink{}
	}
	links := maps.Clone(next.GameLinks[pair.Player])
	if links == nil {
		links = map[string]GameLink{}
	}
	links[id] = GameLink{UUID: input.UUID, Name: input.Name, Platform: input.Platform, LinkedAt: time.Now().UTC()}
	next.GameLinks[pair.Player] = links
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	delete(a.pairings, code)
	a.audit(r, "game.link", id)
	w.WriteHeader(204)
}

func (a *App) playerLinks(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := []map[string]any{}
	for server, link := range a.config.GameLinks[r.PathValue("player")] {
		item := map[string]any{"server": server, "identity": link, "fresh": false}
		if snapshot, ok := a.bridges[server]; ok {
			item["fresh"] = time.Since(snapshot.ReceivedAt) < 90*time.Second
			if snapshot.Plan != nil {
				for _, activity := range snapshot.Plan.Players {
					if activity.UUID == link.UUID {
						item["activity"] = activity
						break
					}
				}
			}
			for _, p := range snapshot.Players {
				if p.UUID == link.UUID {
					item["player"] = p
					break
				}
			}
			if snapshot.Towny != nil {
				for _, resident := range snapshot.Towny.Residents {
					if resident.UUID == link.UUID {
						item["towny"] = resident
						break
					}
				}
			}
		}
		items = append(items, item)
	}
	reply(w, items)
}

func (a *App) enqueueTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.server(id, false); !ok {
		http.NotFound(w, r)
		return
	}
	var input struct {
		Player string `json:"player"`
		Kind   string `json:"kind"`
		Group  string `json:"group"`
		Grant  bool   `json:"grant"`
	}
	if err := decode(r, &input); err != nil {
		bad(w, err)
		return
	}
	if input.Kind != "whitelist" && input.Kind != "member" {
		bad(w, errors.New("kind must be whitelist or member"))
		return
	}
	if input.Kind == "member" && !idPattern.MatchString(input.Group) {
		bad(w, errors.New("select a valid LuckPerms group"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	link, ok := a.config.GameLinks[input.Player][id]
	if !ok {
		http.Error(w, "verified game identity required", 409)
		return
	}
	snapshot, ok := a.bridges[id]
	if !ok || time.Since(snapshot.ReceivedAt) > 90*time.Second {
		http.Error(w, "bridge unavailable", 409)
		return
	}
	if input.Kind == "member" && snapshot.Plugins["LuckPerms"] == "" {
		http.Error(w, "LuckPerms unavailable", 409)
		return
	}
	if input.Kind == "member" && !slices.Contains(snapshot.Groups, input.Group) {
		bad(w, errors.New("select a group reported by the connected server"))
		return
	}
	a.queueTaskLocked(w, r, BridgeTask{Kind: input.Kind, UUID: link.UUID, Group: input.Group, Grant: input.Grant})
}

// Caller holds a.mu so provider preconditions and queue insertion are atomic.
func (a *App) queueTaskLocked(w http.ResponseWriter, r *http.Request, task BridgeTask) {
	id := r.PathValue("id")
	next := a.config
	next.Tasks = maps.Clone(next.Tasks)
	if next.Tasks == nil {
		next.Tasks = map[string][]BridgeTask{}
	}
	tasks := slices.Clone(next.Tasks[id])
	if len(tasks) >= 100 {
		tasks = slices.DeleteFunc(tasks, func(t BridgeTask) bool { return t.Status == "applied" || t.Status == "failed" })
	}
	if len(tasks) >= 100 {
		http.Error(w, "pending task capacity reached", 429)
		return
	}
	task.ID, task.Status, task.CreatedAt = randomID(), "pending", time.Now().UTC()
	next.Tasks[id] = append(tasks, task)
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	a.audit(r, "bridge.task."+task.Kind, id)
	reply(w, task)
}

func (a *App) bridgeTasks(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := []BridgeTask{}
	for _, task := range a.config.Tasks[r.PathValue("id")] {
		if task.Status == "pending" {
			items = append(items, task)
			if len(items) >= 10 {
				break
			}
		}
	}
	reply(w, items)
}
func (a *App) listTasks(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := a.config.Tasks[r.PathValue("id")]
	if items == nil {
		items = []BridgeTask{}
	}
	reply(w, items)
}

func (a *App) gameIdentities(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := []map[string]any{}
	for player, links := range a.config.GameLinks {
		if link, ok := links[r.PathValue("id")]; ok {
			items = append(items, map[string]any{"player": player, "identity": link})
		}
	}
	reply(w, items)
}
func (a *App) finishTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := decode(r, &input); err != nil {
		bad(w, err)
		return
	}
	if len(input.Error) > 500 {
		bad(w, errors.New("error too long"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := r.PathValue("id")
	next := a.config
	next.Tasks = maps.Clone(next.Tasks)
	tasks := slices.Clone(next.Tasks[id])
	i := slices.IndexFunc(tasks, func(t BridgeTask) bool { return t.ID == r.PathValue("task") })
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	if tasks[i].Kind == "chunky" && tasks[i].Status == "pending" {
		http.Error(w, "claim operation before execution", 409)
		return
	}
	if tasks[i].Status != "pending" && tasks[i].Status != "dispatched" {
		w.WriteHeader(204)
		return
	}
	tasks[i].Status = "failed"
	if input.Success {
		tasks[i].Status = "applied"
	}
	tasks[i].Error = input.Error
	next.Tasks[id] = tasks
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	w.WriteHeader(204)
}
