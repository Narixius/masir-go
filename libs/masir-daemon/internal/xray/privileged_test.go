package xray

import (
	"strings"
	"testing"
)

func TestCheckNoLocalFiles(t *testing.T) {
	for name, c := range map[string]struct {
		config string
		bad    string // "" means accepted
	}{
		"plain config":     {`{"inbounds":[{"listen":"127.0.0.1","port":1080}],"outbounds":[{"protocol":"vless","streamSettings":{"security":"tls","tlsSettings":{"serverName":"a.com"}}}]}`, ""},
		"abstract socket":  {`{"inbounds":[{"listen":"@masir"}]}`, ""},
		"tls key log":      {`{"outbounds":[{"streamSettings":{"tlsSettings":{"masterKeyLog":"/tmp/keys"}}}]}`, "masterKeyLog"},
		"reality key log":  {`{"outbounds":[{"streamSettings":{"realitySettings":{"masterKeyLog":"/etc/x"}}}]}`, "masterKeyLog"},
		"cert file":        {`{"inbounds":[{"streamSettings":{"tlsSettings":{"certificates":[{"certificateFile":"/etc/ssl/a.pem"}]}}}]}`, "certificateFile"},
		"key file":         {`{"inbounds":[{"streamSettings":{"tlsSettings":{"certificates":[{"keyFile":"/etc/ssl/a.key"}]}}}]}`, "keyFile"},
		"masquerade dir":   {`{"inbounds":[{"streamSettings":{"masquerade":{"type":"file","dir":"/"}}}]}`, "dir"},
		"other casing":     {`{"outbounds":[{"tls":{"MasterKeyLog":"/tmp/k"}}]}`, "MasterKeyLog"},
		"socket file":      {`{"inbounds":[{"listen":"/var/run/x.sock"}]}`, "socket file"},
		"nested in a list": {`{"a":[{"b":[{"keyfile":"x"}]}]}`, "keyfile"},
	} {
		t.Run(name, func(t *testing.T) {
			err := CheckNoLocalFiles([]byte(c.config))
			switch {
			case c.bad == "" && err != nil:
				t.Fatalf("refused a harmless config: %v", err)
			case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
				t.Fatalf("err = %v, want one mentioning %q", err, c.bad)
			}
		})
	}
}
