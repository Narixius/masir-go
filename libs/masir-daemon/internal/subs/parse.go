package subs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	"github.com/xtls/libxray/share"
)

// Config is one server a subscription provides.
type Config struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Protocol string          `json:"protocol"`
	Address  string          `json:"address,omitempty"`
	Port     int             `json:"port,omitempty"`
	Network  string          `json:"network,omitempty"`
	Security string          `json:"security,omitempty"`
	Outbound json.RawMessage `json:"outbound"` // untagged; whoever uses it picks the tag
	Latency  *Latency        `json:"latency,omitempty"`
}

var errNoConfigs = errors.New("subscription has no usable configs")

// utility outbounds that ride along in full configs but aren't servers.
var utilityProtocols = map[string]bool{"freedom": true, "blackhole": true, "dns": true, "loopback": true}

// Parse accepts xray JSON (an outbound, a list of outbounds, a full config, or a list of
// full configs) or share links (vless://, vmess://, trojan://, ss://, hysteria2://...,
// plain or base64). Configs Xray can't build are dropped.
func Parse(body []byte) ([]Config, error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	var named []namedOutbound
	var err error
	switch {
	case strings.HasPrefix(text, "["):
		named, err = parseJSONArray(text)
	case strings.HasPrefix(text, "{"):
		named, err = parseJSONObject(json.RawMessage(text))
	default:
		named, err = parseShareLinks(text)
	}
	if err != nil {
		return nil, err
	}

	out := make([]Config, 0, len(named))
	seen := map[string]bool{}
	for _, n := range named {
		c, err := newConfig(n)
		if err != nil || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errNoConfigs
	}
	return out, nil
}

type namedOutbound struct {
	name string
	raw  map[string]json.RawMessage
}

func parseJSONArray(text string) ([]namedOutbound, error) {
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, fmt.Errorf("subscription JSON: %w", err)
	}
	var out []namedOutbound
	for _, it := range items {
		n, err := parseJSONObject(it)
		if err != nil {
			continue // one odd entry shouldn't sink the rest
		}
		out = append(out, n...)
	}
	return out, nil
}

// parseJSONObject handles a single outbound, a full config (one server, named by
// "remarks"), or a bare {"outbounds": [...]} list where every server counts.
func parseJSONObject(raw json.RawMessage) ([]namedOutbound, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("subscription JSON: %w", err)
	}
	if _, ok := obj["protocol"]; ok {
		return []namedOutbound{{raw: obj}}, nil
	}
	outsRaw, ok := obj["outbounds"]
	if !ok {
		return nil, errors.New("subscription JSON has no outbounds")
	}
	var outs []map[string]json.RawMessage
	if err := json.Unmarshal(outsRaw, &outs); err != nil {
		return nil, fmt.Errorf("subscription JSON outbounds: %w", err)
	}

	_, hasRemarks := obj["remarks"]
	_, hasInbounds := obj["inbounds"]
	_, hasRouting := obj["routing"]
	if !hasRemarks && !hasInbounds && !hasRouting {
		var list []namedOutbound
		for _, o := range outs {
			if !utilityProtocols[str(o["protocol"])] {
				list = append(list, namedOutbound{raw: o})
			}
		}
		return list, nil
	}

	main := pickMainOutbound(outs)
	if main == nil {
		return nil, errors.New("full config has no proxy outbound")
	}
	return []namedOutbound{{name: str(obj["remarks"]), raw: main}}, nil
}

// pickMainOutbound prefers the outbound tagged "proxy", else the first real server.
func pickMainOutbound(outs []map[string]json.RawMessage) map[string]json.RawMessage {
	for _, o := range outs {
		if str(o["tag"]) == "proxy" {
			return o
		}
	}
	for _, o := range outs {
		if !utilityProtocols[str(o["protocol"])] {
			return o
		}
	}
	return nil
}

// parseShareLinks handles v2rayN-style vmess here and hands every other link to libXray.
func parseShareLinks(text string) ([]namedOutbound, error) {
	if !strings.Contains(text, "://") {
		decoded, err := decodeBase64(text)
		if err != nil || !strings.Contains(decoded, "://") {
			return nil, errors.New("unrecognized subscription format")
		}
		text = decoded
	}

	var out []namedOutbound
	var rest []string
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, ok, err := parseVMessQR(line)
		switch {
		case !ok:
			rest = append(rest, line)
		case err == nil:
			out = append(out, n)
		}
	}
	if len(rest) == 0 {
		return out, nil
	}

	raw, err := share.ConvertShareLinksToXrayJson(strings.Join(rest, "\n"), "")
	if err != nil {
		if len(out) > 0 {
			return out, nil // libXray errors only when none of its lines were usable
		}
		return nil, fmt.Errorf("unrecognized subscription format: %w", err)
	}
	var doc struct {
		Outbounds []map[string]json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for _, o := range doc.Outbounds {
		out = append(out, namedOutbound{raw: o}) // libXray puts the link's #remark in tag
	}
	return out, nil
}

func newConfig(n namedOutbound) (Config, error) {
	o := n.raw
	name := n.name
	if name == "" {
		name = str(o["tag"])
	}
	delete(o, "tag")
	delete(o, "proxySettings") // chains point at outbounds that aren't carried over
	stripDialerProxy(o)

	outbound, err := json.Marshal(o)
	if err != nil {
		return Config{}, err
	}
	if err := xray.ValidateOutbound(outbound); err != nil {
		return Config{}, err
	}

	c := Config{
		ID:       configID(outbound),
		Name:     strings.TrimSpace(name),
		Protocol: str(o["protocol"]),
		Outbound: outbound,
	}
	c.Address, c.Port = serverAddress(o["settings"])
	var stream struct {
		Network  string `json:"network"`
		Security string `json:"security"`
	}
	_ = json.Unmarshal(o["streamSettings"], &stream)
	c.Network, c.Security = stream.Network, stream.Security
	if c.Network == "" || c.Network == "raw" { // xray renamed tcp to raw; users know it as tcp
		c.Network = "tcp"
	}
	if c.Security == "" {
		c.Security = "none"
	}
	if c.Name == "" {
		c.Name = fmt.Sprintf("%s %s:%d", c.Protocol, c.Address, c.Port)
	}
	return c, nil
}

func stripDialerProxy(o map[string]json.RawMessage) {
	var stream map[string]json.RawMessage
	if json.Unmarshal(o["streamSettings"], &stream) != nil {
		return
	}
	var sockopt map[string]json.RawMessage
	if json.Unmarshal(stream["sockopt"], &sockopt) != nil {
		return
	}
	if _, ok := sockopt["dialerProxy"]; !ok {
		return
	}
	delete(sockopt, "dialerProxy")
	stream["sockopt"], _ = json.Marshal(sockopt)
	o["streamSettings"], _ = json.Marshal(stream)
}

// configID hashes the canonical outbound, so it survives refreshes and renames
// and changes only when the server's settings do.
func configID(outbound json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(outbound))
	dec.UseNumber()
	var v any
	_ = dec.Decode(&v)
	canon, _ := json.Marshal(v) // map keys marshal sorted
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:6])
}

// serverAddress finds the server in the shapes Xray accepts: vnext, servers, or flat.
func serverAddress(settings json.RawMessage) (string, int) {
	var s struct {
		Address any `json:"address"`
		Port    any `json:"port"`
		Vnext   []struct {
			Address any `json:"address"`
			Port    any `json:"port"`
		} `json:"vnext"`
		Servers []struct {
			Address any `json:"address"`
			Port    any `json:"port"`
		} `json:"servers"`
	}
	if json.Unmarshal(settings, &s) != nil {
		return "", 0
	}
	switch {
	case len(s.Vnext) > 0:
		return anyString(s.Vnext[0].Address), anyInt(s.Vnext[0].Port)
	case len(s.Servers) > 0:
		return anyString(s.Servers[0].Address), anyInt(s.Servers[0].Port)
	default:
		return anyString(s.Address), anyInt(s.Port)
	}
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func anyInt(v any) int {
	switch p := v.(type) {
	case float64:
		return int(p)
	case string:
		n, _ := strconv.Atoi(p)
		return n
	}
	return 0
}
