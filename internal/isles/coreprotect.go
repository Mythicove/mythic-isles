package isles

import (
	"errors"
	"math"
	"net/http"
	"time"
)

// Exact-coordinate history only. Offset pages are independent database reads.
type CoreProtectQuery struct {
	World   string `json:"world"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Z       int    `json:"z"`
	Seconds int    `json:"seconds"`
	Offset  int    `json:"offset"`
}
type CoreProtectRecord struct {
	Player      string `json:"player"`
	TimestampMs int64  `json:"timestampMs"`
	Action      string `json:"action"`
	Material    string `json:"material,omitempty"`
	RolledBack  bool   `json:"rolledBack"`
}
type CoreProtectResult struct {
	Records []CoreProtectRecord `json:"records"`
	HasMore bool                `json:"hasMore"`
}

func (a *App) enqueueCoreProtect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.server(id, false); !ok {
		http.NotFound(w, r)
		return
	}
	var query CoreProtectQuery
	if err := decode(r, &query); err != nil {
		bad(w, err)
		return
	}
	if query.World == "" || len(query.World) > 256 || math.Abs(float64(query.X)) > 29_999_984 || math.Abs(float64(query.Z)) > 29_999_984 || query.Y < -2048 || query.Y > 2048 || query.Seconds < 1 || query.Seconds > 2_592_000 || query.Offset < 0 || query.Offset > 10_000 {
		bad(w, errors.New("select an exact world coordinate, 1–2592000 seconds and offset 0–10000"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot, ok := a.bridges[id]
	if !ok || time.Since(snapshot.ReceivedAt) > 90*time.Second || snapshot.Plugins["CoreProtect"] == "" {
		http.Error(w, "current CoreProtect observation required", 409)
		return
	}
	a.queueTaskLocked(w, r, BridgeTask{Kind: "coreprotect", CoreProtect: &query})
}

func validateCoreProtectResult(query *CoreProtectQuery, result *CoreProtectResult) error {
	if query == nil || result == nil || result.Records == nil || len(result.Records) > 100 {
		return errors.New("bounded CoreProtect result required")
	}
	for _, row := range result.Records {
		if row.Player == "" || len(row.Player) > 256 || row.TimestampMs <= 0 || row.Action == "" || len(row.Action) > 64 || len(row.Material) > 256 {
			return errors.New("invalid CoreProtect record")
		}
	}
	return nil
}
