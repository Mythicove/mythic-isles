// Package isles provides the optional server directory and Minecraft integration API.
package isles

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Link struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Label string `json:"label"`
}

type Server struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Address      string    `json:"address"`
	Edition      string    `json:"edition"` // java or bedrock
	CoverURL     string    `json:"coverUrl,omitempty"`
	Links        []Link    `json:"links"`
	Published    bool      `json:"published"`
	ConnectionID string    `json:"connectionId,omitempty"`
	DaemonID     string    `json:"daemonId,omitempty"`
	InstanceID   string    `json:"instanceId,omitempty"`
	PackwizURL   string    `json:"packwizUrl,omitempty"`
	PackVersion  string    `json:"packVersion,omitempty"`
	Map          *WorldMap `json:"map,omitempty"`
}

type WorldMap struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

type Connection struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // mcsm, pterodactyl, prometheus
	URL        string `json:"url"`
	Secret     string `json:"-"`
	Configured bool   `json:"configured"`
}

type storedConnection struct {
	Connection
	Ciphertext string `json:"ciphertext"`
}

type Config struct {
	Servers       []Server                       `json:"servers"`
	Connections   []storedConnection             `json:"connections"`
	BridgeSecrets map[string]string              `json:"bridgeSecrets,omitempty"`
	GameLinks     map[string]map[string]GameLink `json:"gameLinks,omitempty"`
	Tasks         map[string][]BridgeTask        `json:"tasks,omitempty"`
}

type App struct {
	mu           sync.Mutex
	config       Config
	dir          string
	token        string
	metricsToken string
	aead         cipher.AEAD
	http         *http.Client
	probes       map[string]*probeEntry
	bridges      map[string]BridgeSnapshot
	pairings     map[string]pairing
	catalog      catalogClient
}

// Open uses one process per data directory. Secrets are encrypted using a separate
// deployment key. No token or encryption key is accepted from public requests.
func Open(dir, token, key string) (*App, error) {
	if len(token) < 32 {
		return nil, errors.New("ISLES_ADMIN_TOKEN must contain at least 32 characters")
	}
	metricsToken := os.Getenv("ISLES_METRICS_TOKEN")
	if metricsToken != "" && (len(metricsToken) < 32 || metricsToken == token) {
		return nil, errors.New("ISLES_METRICS_TOKEN must contain at least 32 characters and differ from ISLES_ADMIN_TOKEN")
	}
	k, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(k) != 32 {
		return nil, errors.New("ISLES_SECRET_KEY must be a base64-encoded 32-byte key")
	}
	b, _ := aes.NewCipher(k)
	aead, _ := cipher.NewGCM(b)
	if dir == "" {
		dir = "data"
	}
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0700); err != nil {
		return nil, err
	}
	a := &App{dir: dir, token: token, aead: aead, probes: map[string]*probeEntry{}, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	a.bridges = map[string]BridgeSnapshot{}
	a.metricsToken = metricsToken
	a.pairings = map[string]pairing{}
	if err := a.catalog.init(); err != nil {
		return nil, err
	}
	a.config = Config{Servers: []Server{}, Connections: []storedConnection{}}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		if err := json.Unmarshal(data, &a.config); err != nil {
			return nil, fmt.Errorf("read configuration: %w", err)
		}
		for i := range a.config.Connections {
			c := &a.config.Connections[i]
			plain, err := a.decrypt(c.Ciphertext)
			if err != nil {
				return nil, fmt.Errorf("decrypt connection %s: check ISLES_SECRET_KEY", c.ID)
			}
			c.Secret, c.Configured = plain, plain != ""
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return a, nil
}

func (a *App) decrypt(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) < a.aead.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	p, err := a.aead.Open(nil, b[:a.aead.NonceSize()], b[a.aead.NonceSize():], nil)
	return string(p), err
}

func (a *App) encrypt(s string) string {
	if s == "" {
		return ""
	}
	nonce := make([]byte, a.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(a.aead.Seal(nonce, nonce, []byte(s), nil))
}

func (a *App) saveLocked(next Config) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(a.dir, ".config-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(a.dir, "config.json"))
	}
	if err == nil {
		a.config = next
	}
	return err
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var releasePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return errors.New("use an absolute HTTP(S) URL without user info or fragment")
	}
	return nil
}

func validateServer(s Server) error {
	if s.Map != nil {
		if s.Map.Kind != "bluemap" && s.Map.Kind != "squaremap" && s.Map.Kind != "dynmap" {
			return errors.New("map kind must be bluemap, squaremap or dynmap")
		}
		u, err := url.Parse(s.Map.URL)
		if err != nil {
			return errors.New("invalid map URL")
		}
		u.Fragment = ""
		if err := validURL(u.String()); err != nil {
			return err
		}
	}
	if !idPattern.MatchString(s.ID) || strings.TrimSpace(s.Name) == "" || len(s.Name) > 120 || len(s.Description) > 4000 {
		return errors.New("invalid server id, name or description")
	}
	if s.Edition != "java" && s.Edition != "bedrock" {
		return errors.New("edition must be java or bedrock")
	}
	if _, _, err := net.SplitHostPort(s.Address); err != nil {
		return errors.New("address must include host and port")
	}
	if len(s.Links) > 20 {
		return errors.New("at most 20 links per server")
	}
	for _, l := range s.Links {
		if len(l.Label) > 100 || len(l.Kind) > 64 {
			return errors.New("link label or kind too long")
		}
		if err := validURL(l.URL); err != nil {
			return err
		}
	}
	for _, u := range []string{s.CoverURL, s.PackwizURL} {
		if u != "" {
			if err := validURL(u); err != nil {
				return err
			}
		}
	}
	if s.PackVersion != "" && !releasePattern.MatchString(s.PackVersion) {
		return errors.New("invalid published pack version")
	}
	return nil
}

func (a *App) server(id string, public bool) (Server, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.config.Servers {
		if s.ID == id && (!public || s.Published) {
			return s, true
		}
	}
	return Server{}, false
}

func publicServer(s Server) Server {
	s.ConnectionID, s.DaemonID, s.InstanceID, s.PackwizURL = "", "", "", ""
	return s
}

func (a *App) connection(id string) (Connection, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.config.Connections {
		if c.ID == id {
			return c.Connection, true
		}
	}
	return Connection{}, false
}

func (a *App) authorized(r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.token)) == 1
}
