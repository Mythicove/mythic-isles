package isles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Instance struct {
	ID       string `json:"id"`
	DaemonID string `json:"daemonId,omitempty"`
	Name     string `json:"name"`
}

// upstream returns sanitized errors: MCSManager includes API keys in query strings,
// so neither transport errors nor upstream bodies may become logs or API errors.
func (a *App) upstream(ctx context.Context, c Connection, method, path string, q url.Values, body any, out any) error {
	u, err := url.Parse(strings.TrimRight(c.URL, "/") + path)
	if err != nil {
		return errors.New("invalid upstream URL")
	}
	if q == nil {
		q = url.Values{}
	}
	if c.Kind == "mcsm" {
		q.Set("apikey", c.Secret)
	}
	u.RawQuery = q.Encode()
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), input)
	if err != nil {
		return errors.New("invalid upstream request")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "Mythicove-Isles/0.1")
	if c.Kind == "mcsm" {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	} else if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	res, err := a.http.Do(req)
	if err != nil {
		return errors.New("upstream unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("upstream returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil || len(data) > 2<<20 {
		return errors.New("upstream response too large or unreadable")
	}
	if c.Kind == "mcsm" {
		var envelope struct {
			Status int             `json:"status"`
			Data   json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Status != 200 {
			return errors.New("panel rejected request")
		}
		data = envelope.Data
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("unexpected upstream response")
	}
	return nil
}

func (a *App) instances(w http.ResponseWriter, r *http.Request) {
	c, ok := a.connection(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	items := []Instance{}
	if c.Kind == "mcsm" {
		var nodes []struct {
			UUID string `json:"uuid"`
		}
		if err := a.upstream(r.Context(), c, "GET", "/api/service/remote_services_list", nil, nil, &nodes); err != nil {
			upstreamError(w, err)
			return
		}
		for _, node := range nodes {
			id := node.UUID
			if id == "" {
				continue
			}
			for page := 1; page <= 100; page++ {
				var result struct {
					MaxPage int `json:"maxPage"`
					Data    []struct {
						ID     string `json:"instanceUuid"`
						Config struct {
							Nickname string `json:"nickname"`
						} `json:"config"`
					} `json:"data"`
				}
				q := url.Values{"daemonId": {id}, "page": {strconv.Itoa(page)}, "page_size": {"50"}, "instance_name": {""}, "status": {""}, "tag": {"[]"}}
				if err := a.upstream(r.Context(), c, "GET", "/api/service/remote_service_instances", q, nil, &result); err != nil {
					upstreamError(w, err)
					return
				}
				for _, i := range result.Data {
					items = append(items, Instance{ID: i.ID, DaemonID: id, Name: i.Config.Nickname})
				}
				if page >= result.MaxPage {
					break
				}
			}
		}
	} else if c.Kind == "pterodactyl" {
		for page := 1; page <= 100; page++ {
			var result struct {
				Data []struct {
					Attributes struct {
						ID   string `json:"identifier"`
						Name string `json:"name"`
					} `json:"attributes"`
				} `json:"data"`
				Meta struct {
					Pagination struct {
						TotalPages int `json:"total_pages"`
					} `json:"pagination"`
				} `json:"meta"`
			}
			if err := a.upstream(r.Context(), c, "GET", "/api/client", url.Values{"page": {strconv.Itoa(page)}}, nil, &result); err != nil {
				upstreamError(w, err)
				return
			}
			for _, i := range result.Data {
				items = append(items, Instance{ID: i.Attributes.ID, Name: i.Attributes.Name})
			}
			if page >= result.Meta.Pagination.TotalPages {
				break
			}
		}
	} else {
		bad(w, errors.New("connection is not a panel"))
		return
	}
	reply(w, items)
}

func (a *App) binding(w http.ResponseWriter, r *http.Request) (Server, Connection, bool) {
	s, ok := a.server(r.PathValue("id"), false)
	if !ok {
		http.NotFound(w, r)
		return s, Connection{}, false
	}
	c, ok := a.connection(s.ConnectionID)
	if !ok || s.InstanceID == "" {
		http.Error(w, "no panel instance bound", 409)
		return s, c, false
	}
	return s, c, true
}

type Runtime struct {
	State     string    `json:"state"`
	CPU       *float64  `json:"cpuPercent,omitempty"`
	Memory    *uint64   `json:"memoryBytes,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

func (a *App) runtime(w http.ResponseWriter, r *http.Request) {
	s, c, ok := a.binding(w, r)
	if !ok {
		return
	}
	result := Runtime{State: "unknown", CheckedAt: time.Now().UTC()}
	if c.Kind == "mcsm" {
		var v struct {
			Status      int `json:"status"`
			ProcessInfo *struct {
				CPU    float64 `json:"cpu"`
				Memory uint64  `json:"memory"`
			} `json:"processInfo"`
		}
		if err := a.upstream(r.Context(), c, "GET", "/api/instance", mcQuery(s), nil, &v); err != nil {
			upstreamError(w, err)
			return
		}
		states := map[int]string{-1: "busy", 0: "stopped", 1: "stopping", 2: "starting", 3: "running"}
		if state, ok := states[v.Status]; ok {
			result.State = state
		}
		if v.ProcessInfo != nil {
			result.CPU = &v.ProcessInfo.CPU
			result.Memory = &v.ProcessInfo.Memory
		}
	} else if c.Kind == "pterodactyl" {
		var v struct {
			Attributes struct {
				State     string `json:"current_state"`
				Resources struct {
					CPU    float64 `json:"cpu_absolute"`
					Memory uint64  `json:"memory_bytes"`
				} `json:"resources"`
			} `json:"attributes"`
		}
		if err := a.upstream(r.Context(), c, "GET", pteroPath(s)+"/resources", nil, nil, &v); err != nil {
			upstreamError(w, err)
			return
		}
		result.State = v.Attributes.State
		result.CPU = &v.Attributes.Resources.CPU
		result.Memory = &v.Attributes.Resources.Memory
	} else {
		bad(w, errors.New("unsupported panel"))
		return
	}
	reply(w, result)
}

func (a *App) action(w http.ResponseWriter, r *http.Request) {
	s, c, ok := a.binding(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	var cmd string
	if action == "command" {
		var input struct {
			Command string `json:"command"`
		}
		if err := decode(r, &input); err != nil {
			bad(w, err)
			return
		}
		cmd = strings.TrimSpace(input.Command)
		if cmd == "" || len(cmd) > 2000 || strings.ContainsAny(cmd, "\r\n\x00") {
			bad(w, errors.New("one command of 1–2000 characters required"))
			return
		}
	}
	if action != "start" && action != "stop" && action != "restart" && action != "command" {
		http.NotFound(w, r)
		return
	}
	var err error
	if c.Kind == "mcsm" {
		path := action
		if path == "start" {
			path = "open"
		}
		q := mcQuery(s)
		if action == "command" {
			q.Set("command", cmd)
		}
		err = a.upstream(r.Context(), c, "POST", "/api/protected_instance/"+path, q, nil, nil)
	} else if c.Kind == "pterodactyl" {
		path := "/power"
		body := map[string]string{"signal": action}
		if action == "command" {
			path = "/command"
			body = map[string]string{"command": cmd}
		}
		err = a.upstream(r.Context(), c, "POST", pteroPath(s)+path, nil, body, nil)
	} else {
		bad(w, errors.New("unsupported panel"))
		return
	}
	if err != nil {
		upstreamError(w, err)
		return
	}
	// No command text in audit logs: commands can contain passwords or private text.
	a.audit(r, "server."+action, s.ID)
	w.WriteHeader(http.StatusAccepted)
}

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	s, c, ok := a.binding(w, r)
	if !ok {
		return
	}
	if c.Kind != "mcsm" {
		http.Error(w, "this panel requires its WebSocket console", 501)
		return
	}
	var text string
	q := mcQuery(s)
	q.Set("size", "64KB")
	if err := a.upstream(r.Context(), c, "GET", "/api/protected_instance/outputlog", q, nil, &text); err != nil {
		upstreamError(w, err)
		return
	}
	reply(w, map[string]string{"text": text})
}

func mcQuery(s Server) url.Values {
	return url.Values{"uuid": {s.InstanceID}, "daemonId": {s.DaemonID}}
}
func pteroPath(s Server) string { return "/api/client/servers/" + url.PathEscape(s.InstanceID) }
func upstreamError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadGateway)
}
