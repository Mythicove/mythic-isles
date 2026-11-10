package isles

import (
	"errors"
	"maps"
	"math"
	"net/http"
	"slices"
	"time"
)

type ChunkySelection struct {
	Shape   string  `json:"shape"`
	CenterX float64 `json:"centerX"`
	CenterZ float64 `json:"centerZ"`
	RadiusX float64 `json:"radiusX"`
	RadiusZ float64 `json:"radiusZ"`
}
type ChunkyProgress struct {
	Chunks     int64    `json:"chunks"`
	Total      int64    `json:"total"`
	Percent    float64  `json:"percent"`
	Rate       *float64 `json:"rate,omitempty"`
	ETASeconds *int64   `json:"etaSeconds,omitempty"`
}
type ChunkyWorld struct {
	World     string           `json:"world"`
	State     string           `json:"state"`
	Selection *ChunkySelection `json:"selection,omitempty"`
	Progress  *ChunkyProgress  `json:"progress,omitempty"`
}
type ChunkyOperation struct {
	Action  string  `json:"action"`
	World   string  `json:"world"`
	Shape   string  `json:"shape,omitempty"`
	CenterX float64 `json:"centerX"`
	CenterZ float64 `json:"centerZ"`
	Radius  float64 `json:"radius,omitempty"`
}

func validateChunkySnapshot(worlds []ChunkyWorld) error {
	if len(worlds) > 100 {
		return errors.New("too many Chunky worlds")
	}
	seen := map[string]bool{}
	for _, w := range worlds {
		if w.World == "" || len(w.World) > 256 || seen[w.World] || !slices.Contains([]string{"idle", "running", "paused", "completed", "cancelled", "stopped"}, w.State) {
			return errors.New("invalid Chunky world")
		}
		seen[w.World] = true
		if s := w.Selection; s != nil && (len(s.Shape) > 32 || math.Abs(s.CenterX) > 30_000_000 || math.Abs(s.CenterZ) > 30_000_000 || s.RadiusX < 0 || s.RadiusZ < 0) {
			return errors.New("invalid Chunky selection")
		}
		if p := w.Progress; p != nil && (p.Chunks < 0 || p.Total < 0 || p.Percent < 0 || p.Percent > 100 || (p.Rate != nil && *p.Rate < 0) || (p.ETASeconds != nil && *p.ETASeconds < 0)) {
			return errors.New("invalid Chunky progress")
		}
	}
	return nil
}

func (a *App) enqueueChunky(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.server(id, false); !ok {
		http.NotFound(w, r)
		return
	}
	var input ChunkyOperation
	if err := decode(r, &input); err != nil {
		bad(w, err)
		return
	}
	if input.World == "" || len(input.World) > 256 || !slices.Contains([]string{"start", "pause", "continue", "cancel"}, input.Action) {
		bad(w, errors.New("select a world and Chunky action"))
		return
	}
	if input.Action == "start" && (!slices.Contains([]string{"square", "circle"}, input.Shape) || input.Radius < 1 || input.Radius > 100_000 || math.Abs(input.CenterX)+input.Radius > 29_999_984 || math.Abs(input.CenterZ)+input.Radius > 29_999_984) {
		bad(w, errors.New("select square/circle, radius 1–100000 blocks and coordinates inside the world limit"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot, ok := a.bridges[id]
	if !ok || time.Since(snapshot.ReceivedAt) > 90*time.Second || snapshot.Plugins["Chunky"] == "" {
		http.Error(w, "current Chunky observation required", 409)
		return
	}
	i := slices.IndexFunc(snapshot.Chunky, func(v ChunkyWorld) bool { return v.World == input.World })
	if i < 0 {
		bad(w, errors.New("select a world reported by Chunky"))
		return
	}
	state := snapshot.Chunky[i].State
	allowed := (input.Action == "start" && slices.Contains([]string{"idle", "completed", "cancelled", "stopped"}, state)) || (input.Action == "pause" && state == "running") || (input.Action == "continue" && state == "paused") || (input.Action == "cancel" && (state == "running" || state == "paused"))
	if !allowed {
		http.Error(w, "action does not match observed Chunky state; refresh first", 409)
		return
	}
	if slices.ContainsFunc(a.config.Tasks[id], func(t BridgeTask) bool {
		return t.Kind == "chunky" && t.Status == "pending" && t.Chunky != nil && t.Chunky.World == input.World
	}) {
		http.Error(w, "a Chunky operation is already queued for this world", 409)
		return
	}
	a.queueTaskLocked(w, r, BridgeTask{Kind: "chunky", Chunky: &input})
}

// Chunky operations are not desired-state assignments. Persist dispatch BEFORE
// execution and never redeliver after a lost response or process restart.
func (a *App) claimTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := r.PathValue("id")
	tasks := slices.Clone(a.config.Tasks[id])
	i := slices.IndexFunc(tasks, func(t BridgeTask) bool { return t.ID == r.PathValue("task") })
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	if tasks[i].Kind != "chunky" || tasks[i].Status != "pending" {
		http.Error(w, "operation already dispatched or not claimable", 409)
		return
	}
	tasks[i].Status = "dispatched"
	next := a.config
	next.Tasks = maps.Clone(next.Tasks)
	next.Tasks[id] = tasks
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	w.WriteHeader(204)
}
