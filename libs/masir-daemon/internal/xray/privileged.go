package xray

import (
	"encoding/json"
	"fmt"
	"strings"
)

// localFileKeys are config keys that make Xray read or write a path on this machine:
// a TLS key log, certificate and key files, and the directory a masquerade serves.
// Names are compared in lower case, as Xray's own JSON decoding does.
var localFileKeys = map[string]string{
	"masterkeylog":    "writes TLS session keys to a file",
	"certificatefile": "reads a certificate from a file",
	"keyfile":         "reads a private key from a file",
	"dir":             "serves a directory of files",
}

// CheckNoLocalFiles refuses a config that would make Xray touch local files. A daemon
// with root rights runs configs it didn't write (a subscription's servers, custom
// inbounds and outbounds), and a path in one would be opened with those rights.
func CheckNoLocalFiles(config []byte) error {
	var v any
	if err := json.Unmarshal(config, &v); err != nil {
		return err
	}
	return walk(v, "")
}

func walk(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			here := path + "/" + k
			if why, bad := localFileKeys[strings.ToLower(k)]; bad {
				return fmt.Errorf("%s %s, which masir doesn't allow while it runs with root rights", here, why)
			}
			if strings.EqualFold(k, "listen") {
				if s, ok := child.(string); ok && strings.HasPrefix(s, "/") {
					return fmt.Errorf("%s listens on a socket file, which masir doesn't allow while it runs with root rights", here)
				}
			}
			if err := walk(child, here); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range t {
			if err := walk(child, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
