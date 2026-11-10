package isles

import (
	"errors"
	"net/http"
	"slices"
	"time"
)

type PlanActivity struct {
	UUID       string    `json:"uuid"`
	Sessions   int64     `json:"sessions"`
	PlaytimeMs int64     `json:"playtimeMs"`
	AFKMs      int64     `json:"afkMs"`
	FirstSeen  time.Time `json:"firstSeen"`
	LastSeen   time.Time `json:"lastSeen"`
	ObservedAt time.Time `json:"observedAt"`
}
type PlanSnapshot struct {
	ServerUUID      string         `json:"serverUUID"`
	RecordedPlayers int64          `json:"recordedPlayers"`
	Sessions        int64          `json:"sessions"`
	PlaytimeMs      int64          `json:"playtimeMs"`
	AFKMs           int64          `json:"afkMs"`
	ObservedAt      time.Time      `json:"observedAt"`
	Players         []PlanActivity `json:"players"`
}

func validatePlanSnapshot(p *PlanSnapshot) error {
	if p == nil {
		return nil
	}
	if len(p.ServerUUID) < 16 || len(p.ServerUUID) > 64 || p.RecordedPlayers < 0 || p.Sessions < 0 || p.PlaytimeMs < 0 || p.AFKMs < 0 || p.ObservedAt.IsZero() || len(p.Players) > 2000 {
		return errors.New("invalid Plan observation")
	}
	seen := map[string]bool{}
	for _, v := range p.Players {
		if len(v.UUID) < 16 || len(v.UUID) > 64 || seen[v.UUID] || v.Sessions < 1 || v.PlaytimeMs < 0 || v.AFKMs < 0 || v.FirstSeen.IsZero() || v.LastSeen.Before(v.FirstSeen) || v.ObservedAt.IsZero() {
			return errors.New("invalid Plan activity")
		}
		seen[v.UUID] = true
	}
	return nil
}

// Only the bound game server receives its verified UUID allowlist. Website IDs
// and other servers' identities are not needed by the native provider.
func (a *App) bridgeIdentities(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := []string{}
	for _, links := range a.config.GameLinks {
		if link, ok := links[r.PathValue("id")]; ok {
			ids = append(ids, link.UUID)
		}
	}
	if len(ids) > 2000 {
		http.Error(w, "verified identity capacity exceeded", 409)
		return
	}
	slices.Sort(ids)
	reply(w, ids)
}
