package isles

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type packManifest struct {
	Name     string            `toml:"name"`
	Version  string            `toml:"version"`
	Format   string            `toml:"pack-format"`
	Versions map[string]string `toml:"versions"`
	Index    struct {
		File   string `toml:"file"`
		Format string `toml:"hash-format"`
		Hash   string `toml:"hash"`
	} `toml:"index"`
}
type packIndex struct {
	Format string `toml:"hash-format"`
	Files  []struct {
		File   string `toml:"file"`
		Hash   string `toml:"hash"`
		Format string `toml:"hash-format"`
		Meta   bool   `toml:"metafile"`
		Alias  string `toml:"alias"`
	} `toml:"files"`
}
type packMeta struct {
	Name     string `toml:"name"`
	Filename string `toml:"filename"`
	Side     string `toml:"side"`
	Download struct {
		URL    string `toml:"url"`
		Format string `toml:"hash-format"`
		Hash   string `toml:"hash"`
		Mode   string `toml:"mode"`
	} `toml:"download"`
	Option struct {
		Optional bool `toml:"optional"`
		Default  bool `toml:"default"`
	} `toml:"option"`
}
type packFile struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Env       map[string]string `json:"env"`
	Downloads []string          `json:"downloads"`
	Size      int               `json:"fileSize"`
}
type modrinthIndex struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	Version       string            `json:"versionId"`
	Name          string            `json:"name"`
	Files         []packFile        `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

func (a *App) publishPack(w http.ResponseWriter, r *http.Request) {
	s, ok := a.server(r.PathValue("id"), false)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.PackwizURL == "" {
		bad(w, errors.New("configure a packwiz source first"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	data, err := a.exportPack(ctx, s.PackwizURL)
	if err != nil {
		upstreamError(w, err)
		return
	}
	h := sha256.Sum256(data)
	version := hex.EncodeToString(h[:])
	name := filepath.Join(a.dir, "packs", version+".mrpack")
	f, err := os.CreateTemp(filepath.Join(a.dir, "packs"), ".publish-*")
	if err != nil {
		failed(w)
		return
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, name)
	}
	if err != nil {
		failed(w)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	i := slices.IndexFunc(a.config.Servers, func(v Server) bool { return v.ID == s.ID })
	if i < 0 || a.config.Servers[i].PackwizURL != s.PackwizURL {
		http.Error(w, "server changed during publication", 409)
		return
	}
	next := a.config
	next.Servers = slices.Clone(next.Servers)
	next.Servers[i].PackVersion = version
	if err := a.saveLocked(next); err != nil {
		failed(w)
		return
	}
	a.audit(r, "pack.publish", s.ID)
	reply(w, map[string]string{"version": version, "path": "/api/isles/packs/" + version})
}

// This exporter deliberately rejects formats it cannot faithfully publish. It
// never silently omits CurseForge metadata-only entries or accepts unverified files.
func (a *App) exportPack(ctx context.Context, source string) ([]byte, error) {
	budget := 256 << 20
	manifestBytes, err := a.packFetch(ctx, source, &budget, 1<<20)
	if err != nil {
		return nil, err
	}
	var p packManifest
	if _, err := toml.Decode(string(manifestBytes), &p); err != nil {
		return nil, errors.New("invalid pack.toml")
	}
	if p.Format != "packwiz:1.1.0" || p.Versions["minecraft"] == "" {
		return nil, errors.New("packwiz:1.1.0 and a Minecraft version are required")
	}
	u, err := relativeFile(source, p.Index.File)
	if err != nil {
		return nil, err
	}
	indexBytes, err := a.packFetch(ctx, u, &budget, 2<<20)
	if err != nil {
		return nil, err
	}
	if err := verifyHash(indexBytes, p.Index.Format, p.Index.Hash); err != nil {
		return nil, err
	}
	var idx packIndex
	if _, err := toml.Decode(string(indexBytes), &idx); err != nil {
		return nil, errors.New("invalid index.toml")
	}
	if len(idx.Files) > 2000 {
		return nil, errors.New("at most 2000 indexed files per release")
	}
	deps := map[string]string{"minecraft": p.Versions["minecraft"]}
	for key, value := range p.Versions {
		switch key {
		case "fabric":
			deps["fabric-loader"] = value
		case "quilt":
			deps["quilt-loader"] = value
		case "forge", "neoforge":
			deps[key] = value
		case "minecraft":
		default:
			return nil, fmt.Errorf("unsupported loader %s", key)
		}
	}
	out := modrinthIndex{FormatVersion: 1, Game: "minecraft", Version: p.Version, Name: p.Name, Files: []packFile{}, Dependencies: deps}
	if out.Version == "" {
		hash := sha256.Sum256(indexBytes)
		out.Version = hex.EncodeToString(hash[:8])
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	seen := map[string]bool{}
	for _, entry := range idx.Files {
		u, err := relativeFile(source, entry.File)
		if err != nil {
			return nil, err
		}
		limit := 32 << 20
		if entry.Meta {
			limit = 1 << 20
		}
		b, err := a.packFetch(ctx, u, &budget, limit)
		if err != nil {
			return nil, err
		}
		format := entry.Format
		if format == "" {
			format = idx.Format
		}
		if err := verifyHash(b, format, entry.Hash); err != nil {
			return nil, err
		}
		if entry.Alias != "" {
			return nil, errors.New("index aliases require upstream packwiz export")
		}
		if !entry.Meta {
			if seen[entry.File] {
				return nil, errors.New("duplicate pack path")
			}
			seen[entry.File] = true
			f, err := z.Create("overrides/" + entry.File)
			if err != nil {
				return nil, err
			}
			if _, err := f.Write(b); err != nil {
				return nil, err
			}
			continue
		}
		var meta packMeta
		if _, err := toml.Decode(string(b), &meta); err != nil {
			return nil, errors.New("invalid pack metafile")
		}
		if meta.Download.Mode != "" && meta.Download.Mode != "url" {
			return nil, errors.New("metadata download mode requires upstream packwiz export")
		}
		if err := validURL(meta.Download.URL); err != nil {
			return nil, errors.New("metafile requires an absolute HTTP(S) download URL")
		}
		if !safePackPath(meta.Filename) {
			return nil, errors.New("invalid metafile destination")
		}
		dest := path.Join(path.Dir(entry.File), meta.Filename)
		if seen[dest] {
			return nil, errors.New("duplicate pack path")
		}
		seen[dest] = true
		if meta.Side != "" && meta.Side != "both" && meta.Side != "client" && meta.Side != "server" {
			return nil, errors.New("invalid physical side")
		}
		file, err := a.packFetch(ctx, meta.Download.URL, &budget, 128<<20)
		if err != nil {
			return nil, err
		}
		if err := verifyHash(file, meta.Download.Format, meta.Download.Hash); err != nil {
			return nil, err
		}
		sha1sum := sha1.Sum(file)
		sha512sum := sha512.Sum512(file)
		env := map[string]string{"client": "required", "server": "required"}
		if meta.Side == "client" {
			env["server"] = "unsupported"
		}
		if meta.Side == "server" {
			env["client"] = "unsupported"
		}
		if meta.Option.Optional {
			for side, value := range env {
				if value == "required" {
					env[side] = "optional"
				}
			}
		}
		out.Files = append(out.Files, packFile{Path: dest, Hashes: map[string]string{"sha1": hex.EncodeToString(sha1sum[:]), "sha512": hex.EncodeToString(sha512sum[:])}, Env: env, Downloads: []string{meta.Download.URL}, Size: len(file)})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	f, err := z.Create("modrinth.index.json")
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(b); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *App) packFetch(ctx context.Context, raw string, budget *int, limit int) ([]byte, error) {
	if err := validURL(raw); err != nil {
		return nil, err
	}
	client := *a.http
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return validURL(req.URL.String())
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, errors.New("invalid pack source")
	}
	req.Header.Set("User-Agent", "Mythicove-Isles/0.1")
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("pack source unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("pack source returned HTTP %d", res.StatusCode)
	}
	if *budget < limit {
		limit = *budget
	}
	if limit < 0 {
		return nil, errors.New("pack exceeds 256 MiB publication budget")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, int64(limit)+1))
	if err != nil || len(b) > limit {
		return nil, errors.New("pack file exceeds publication limit")
	}
	*budget -= len(b)
	return b, nil
}

func safePackPath(s string) bool {
	if s == "" || s != path.Clean(s) || strings.HasPrefix(s, "/") || s == ".." || strings.HasPrefix(s, "../") || strings.ContainsAny(s, "\\:\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}
func relativeFile(base, file string) (string, error) {
	if !safePackPath(file) {
		return "", errors.New("pack contains an invalid relative path")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(path.Dir(u.Path), file)
	u.RawPath = ""
	u.RawQuery = ""
	return u.String(), nil
}
func verifyHash(b []byte, format, expected string) error {
	var actual string
	switch format {
	case "sha256":
		h := sha256.Sum256(b)
		actual = hex.EncodeToString(h[:])
	case "sha512":
		h := sha512.Sum512(b)
		actual = hex.EncodeToString(h[:])
	case "sha1":
		h := sha1.Sum(b)
		actual = hex.EncodeToString(h[:])
	case "md5":
		h := md5.Sum(b)
		actual = hex.EncodeToString(h[:])
	default:
		return fmt.Errorf("unsupported hash format %s; use upstream packwiz export", format)
	}
	if !strings.EqualFold(actual, expected) {
		return errors.New("pack source hash mismatch")
	}
	return nil
}
