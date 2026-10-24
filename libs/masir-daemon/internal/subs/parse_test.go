package subs

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

const (
	vlessReality = "vless://b831381d-6324-4d53-ad4f-8cda48b30811@de.example.com:443?encryption=none&security=reality" +
		"&sni=www.microsoft.com&fp=chrome&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=6ba85179e30d4fc2&type=tcp" +
		"&flow=xtls-rprx-vision#DE%20Reality"
	trojanWS = "trojan://secret@tr.example.com:443?security=tls&sni=tr.example.com&type=ws&path=%2Fws#Trojan%20WS"
)

func ssLink() string {
	return "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@ss.example.com:8388#SS"
}

func vmessLink() string {
	j := `{"v":"2","ps":"VMess WS","add":"vm.example.com","port":"443","id":"b831381d-6324-4d53-ad4f-8cda48b30811",` +
		`"aid":"0","net":"ws","type":"none","host":"vm.example.com","path":"/vm","tls":"tls"}`
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(j))
}

func links() string {
	return strings.Join([]string{vlessReality, trojanWS, ssLink(), vmessLink()}, "\n")
}

func byName(t *testing.T, cs []Config) map[string]Config {
	t.Helper()
	m := map[string]Config{}
	for _, c := range cs {
		m[c.Name] = c
	}
	return m
}

func TestParseShareLinks(t *testing.T) {
	for name, body := range map[string]string{
		"plain":  links(),
		"base64": base64.StdEncoding.EncodeToString([]byte(links())),
		"crlf":   strings.ReplaceAll(links(), "\n", "\r\n") + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			cs, err := Parse([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			got := byName(t, cs)
			want := map[string]struct {
				protocol, address, network, security string
				port                                 int
			}{
				"DE Reality": {"vless", "de.example.com", "tcp", "reality", 443},
				"Trojan WS":  {"trojan", "tr.example.com", "ws", "tls", 443},
				"SS":         {"shadowsocks", "ss.example.com", "tcp", "none", 8388},
				"VMess WS":   {"vmess", "vm.example.com", "ws", "tls", 443},
			}
			if len(got) != len(want) {
				t.Fatalf("got %d configs %v, want %d", len(got), keys(got), len(want))
			}
			for n, w := range want {
				c, ok := got[n]
				if !ok {
					t.Fatalf("missing %q in %v", n, keys(got))
				}
				if c.Protocol != w.protocol || c.Address != w.address || c.Port != w.port ||
					c.Network != w.network || c.Security != w.security {
					t.Errorf("%s = %+v, want %+v", n, c, w)
				}
				if strings.Contains(string(c.Outbound), `"tag"`) {
					t.Errorf("%s outbound still has a tag: %s", n, c.Outbound)
				}
			}
		})
	}
}

func TestParseJSONFullConfigs(t *testing.T) {
	// Marzban's v2ray-json: a list of full configs, each with a chained fragment outbound.
	body := `[
	  {"remarks":"Full A","outbounds":[
	    {"tag":"proxy","protocol":"trojan","settings":{"servers":[{"address":"a.example.com","port":443,"password":"x"}]},
	     "streamSettings":{"network":"tcp","security":"tls","sockopt":{"dialerProxy":"fragment"}}},
	    {"tag":"fragment","protocol":"freedom"},
	    {"tag":"direct","protocol":"freedom"}]},
	  {"remarks":"Full B","outbounds":[
	    {"tag":"direct","protocol":"freedom"},
	    {"protocol":"trojan","settings":{"servers":[{"address":"b.example.com","port":8443,"password":"y"}]},"streamSettings":{"security":"tls"}}]}
	]`
	cs, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := byName(t, cs)
	if got["Full A"].Address != "a.example.com" || got["Full B"].Port != 8443 || len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(string(got["Full A"].Outbound), "dialerProxy") {
		t.Fatal("dialerProxy to an outbound we don't carry should be stripped")
	}
}

func TestParseJSONOutbounds(t *testing.T) {
	body := `{"outbounds":[
	  {"tag":"one","protocol":"trojan","settings":{"servers":[{"address":"1.example.com","port":443,"password":"x"}]},"streamSettings":{"security":"tls"}},
	  {"tag":"two","protocol":"trojan","settings":{"servers":[{"address":"2.example.com","port":443,"password":"x"}]},"streamSettings":{"security":"tls"}},
	  {"tag":"direct","protocol":"freedom"}]}`
	cs, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(byName(t, cs)); len(got) != 2 {
		t.Fatalf("want both servers and no utility outbound, got %v", got)
	}
}

func TestParseDropsBadAndDuplicates(t *testing.T) {
	body := strings.Join([]string{trojanWS, trojanWS, "vless://not a link", "# comment"}, "\n")
	cs, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("got %d configs, want 1", len(cs))
	}
	if _, err := Parse([]byte("<html>blocked</html>")); err == nil {
		t.Fatal("expected an error for a block page")
	}
}

func TestConfigIDStable(t *testing.T) {
	a, _ := Parse([]byte(trojanWS))
	renamed, _ := Parse([]byte(strings.Replace(trojanWS, "#Trojan%20WS", "#Renamed", 1)))
	changed, _ := Parse([]byte(strings.Replace(trojanWS, "secret", "rotated", 1)))
	if a[0].ID != renamed[0].ID {
		t.Error("renaming a server changed its ID")
	}
	if a[0].ID == changed[0].ID {
		t.Error("changing a server's credentials kept its ID")
	}
}

func TestProviderHeaders(t *testing.T) {
	u := parseUsage("upload=100; download=2.5e3;total=10737418240; expire=1767225600")
	if u == nil || u.Upload != 100 || u.Download != 2500 || u.Total != 10737418240 ||
		!u.Expire.Equal(time.Unix(1767225600, 0)) {
		t.Fatalf("usage = %+v", u)
	}
	if parseUsage("") != nil || parseUsage("garbage") != nil {
		t.Fatal("expected nil usage for missing/garbage header")
	}
	if d := parseInterval("24"); d != 24*time.Hour {
		t.Fatalf("interval = %v", d)
	}
	if got := parseTitle("base64:" + base64.StdEncoding.EncodeToString([]byte("My VPN"))); got != "My VPN" {
		t.Fatalf("title = %q", got)
	}
}

func keys(m map[string]Config) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
