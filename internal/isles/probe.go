package isles

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type Status struct {
	Reachable        bool      `json:"reachable"`
	CheckedAt        time.Time `json:"checkedAt"`
	Players          *int      `json:"players,omitempty"`
	MaxPlayers       *int      `json:"maxPlayers,omitempty"`
	Version          string    `json:"version,omitempty"`
	MOTD             string    `json:"motd,omitempty"`
	LatencyMS        int64     `json:"latencyMs"`
	Error            string    `json:"error,omitempty"`
	ProtocolVersions []string  `json:"protocolVersions,omitempty"`
}

type probeEntry struct {
	done   chan struct{}
	status Status
}

func (a *App) probe(ctx context.Context, s Server) Status {
	a.mu.Lock()
	if e, ok := a.probes[s.ID]; ok {
		select {
		case <-e.done:
			if time.Since(e.status.CheckedAt) < 20*time.Second {
				a.mu.Unlock()
				return e.status
			}
		default:
			a.mu.Unlock()
			select {
			case <-e.done:
				return e.status
			case <-ctx.Done():
				return Status{Error: "request_cancelled"}
			}
		}
	}
	e := &probeEntry{done: make(chan struct{})}
	a.probes[s.ID] = e
	a.mu.Unlock()
	// A disconnected browser must not poison a shared status observation.
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	start := time.Now()
	var st Status
	var err error
	if s.Edition == "bedrock" {
		st, err = bedrockStatus(check, s.Address)
	} else {
		st, err = javaStatus(check, s.Address)
	}
	st.CheckedAt = time.Now().UTC()
	st.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		st.Error = "probe_failed"
	} else {
		st.Reachable = true
	}
	e.status = st
	close(e.done)
	return st
}

func varint(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func readVarint(r io.Reader) (int, error) {
	n := 0
	for i := 0; i < 5; i++ {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		n |= int(b[0]&0x7f) << (7 * i)
		if b[0]&0x80 == 0 {
			return n, nil
		}
	}
	return 0, errors.New("invalid VarInt")
}

func javaStatus(ctx context.Context, address string) (Status, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return Status{}, err
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return Status{}, err
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return Status{}, err
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	var b bytes.Buffer
	b.WriteByte(0)
	b.Write(varint(767))
	b.Write(varint(len(host)))
	b.WriteString(host)
	_ = binary.Write(&b, binary.BigEndian, uint16(p))
	b.WriteByte(1)
	packet := append(varint(b.Len()), b.Bytes()...)
	packet = append(packet, 1, 0)
	if _, err := c.Write(packet); err != nil {
		return Status{}, err
	}
	length, err := readVarint(c)
	if err != nil || length < 3 || length > 64<<10 {
		return Status{}, errors.New("invalid status packet length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(c, data); err != nil {
		return Status{}, err
	}
	r := bytes.NewReader(data)
	id, err := readVarint(r)
	if err != nil || id != 0 {
		return Status{}, errors.New("invalid status packet")
	}
	size, err := readVarint(r)
	if err != nil || size != r.Len() {
		return Status{}, errors.New("invalid JSON length")
	}
	var response struct {
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
		Players struct {
			Online *int `json:"online"`
			Max    *int `json:"max"`
		} `json:"players"`
		Description json.RawMessage `json:"description"`
	}
	if err := json.NewDecoder(r).Decode(&response); err != nil {
		return Status{}, err
	}
	return Status{Version: response.Version.Name, Players: response.Players.Online, MaxPlayers: response.Players.Max, MOTD: plainText(response.Description)}, nil
}

func plainText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var c struct {
		Text  string            `json:"text"`
		Extra []json.RawMessage `json:"extra"`
	}
	if json.Unmarshal(raw, &c) == nil {
		for _, child := range c.Extra {
			c.Text += plainText(child)
		}
		return c.Text
	}
	return ""
}

var raknetMagic = []byte{0, 255, 255, 0, 254, 254, 254, 254, 253, 253, 253, 253, 18, 52, 86, 120}

func bedrockStatus(ctx context.Context, address string) (Status, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if err != nil {
		return Status{}, err
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	var b bytes.Buffer
	b.WriteByte(1)
	_ = binary.Write(&b, binary.BigEndian, time.Now().UnixMilli())
	b.Write(raknetMagic)
	_ = binary.Write(&b, binary.BigEndian, uint64(0x4d79746869636f76))
	if _, err := c.Write(b.Bytes()); err != nil {
		return Status{}, err
	}
	data := make([]byte, 4096)
	n, err := c.Read(data)
	if err != nil {
		return Status{}, err
	}
	if n < 35 || data[0] != 0x1c || !bytes.Equal(data[17:33], raknetMagic) {
		return Status{}, errors.New("invalid RakNet pong")
	}
	size := int(binary.BigEndian.Uint16(data[33:35]))
	if size > n-35 {
		return Status{}, errors.New("invalid RakNet string")
	}
	parts := strings.Split(string(data[35:35+size]), ";")
	if len(parts) < 6 || parts[0] != "MCPE" {
		return Status{}, errors.New("invalid Bedrock status")
	}
	players, e1 := strconv.Atoi(parts[4])
	max, e2 := strconv.Atoi(parts[5])
	if e1 != nil || e2 != nil {
		return Status{}, errors.New("invalid player count")
	}
	return Status{MOTD: parts[1], Version: parts[3], Players: &players, MaxPlayers: &max}, nil
}
