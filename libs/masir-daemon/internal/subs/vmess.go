package subs

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// vmessQR is the v2rayN "vmess://<base64 JSON>" format. libXray only parses the
// URL-style vmess link, but this one is still what most subscriptions serve.
type vmessQR struct {
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port any    `json:"port"`
	ID   string `json:"id"`
	Scy  string `json:"scy"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	ALPN string `json:"alpn"`
	FP   string `json:"fp"`
}

// parseVMessQR returns ok=false when line isn't this format, so it can go to libXray instead.
func parseVMessQR(line string) (namedOutbound, bool, error) {
	payload, found := strings.CutPrefix(line, "vmess://")
	if !found {
		return namedOutbound{}, false, nil
	}
	decoded, err := decodeBase64(payload)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(decoded), "{") {
		return namedOutbound{}, false, nil
	}
	var q vmessQR
	if err := json.Unmarshal([]byte(decoded), &q); err != nil {
		return namedOutbound{}, true, err
	}
	if q.Add == "" || q.ID == "" {
		return namedOutbound{}, true, errors.New("vmess link without address or id")
	}

	stream := map[string]any{"network": cmp.Or(q.Net, "tcp")}
	switch q.Net {
	case "ws":
		stream["wsSettings"] = map[string]any{"path": q.Path, "host": q.Host}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{"path": q.Path, "host": q.Host}
	case "xhttp", "splithttp":
		stream["network"] = "xhttp"
		stream["xhttpSettings"] = map[string]any{"path": q.Path, "host": q.Host, "mode": cmp.Or(q.Type, "auto")}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": q.Path, "multiMode": q.Type == "multi"}
	case "", "tcp":
		if q.Type == "http" {
			req := map[string]any{"path": []string{cmp.Or(q.Path, "/")}}
			if q.Host != "" {
				req["headers"] = map[string]any{"Host": strings.Split(q.Host, ",")}
			}
			stream["tcpSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	}
	if q.TLS == "tls" {
		tls := map[string]any{"serverName": cmp.Or(q.SNI, q.Host)}
		if q.ALPN != "" {
			tls["alpn"] = strings.Split(q.ALPN, ",")
		}
		if q.FP != "" {
			tls["fingerprint"] = q.FP
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = tls
	}

	out := map[string]any{
		"protocol": "vmess",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": q.Add,
			"port":    anyInt(q.Port),
			"users":   []any{map[string]any{"id": q.ID, "security": cmp.Or(q.Scy, "auto")}},
		}}},
		"streamSettings": stream,
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return namedOutbound{}, true, err
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	return namedOutbound{name: q.PS, raw: obj}, true, nil
}

// decodeBase64 accepts standard or URL-safe base64, padded or not.
func decodeBase64(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", errors.New("not base64")
}
