package rulecheck

import (
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
)

// Request is what a running daemon hands a newly downloaded masir, over stdin, to ask
// "would you start with these rules and this geo data?" before the update is installed.
type Request struct {
	AssetDir string `json:"assetDir"` // where geoip.dat and geosite.dat are
	Rules    []Rule `json:"rules"`    // the enabled rules
}

type Rule struct {
	Tag   string          `json:"tag"`
	Match json.RawMessage `json:"match"`
}

// Report is a rule this version can't build.
type Report struct {
	Tag     string   `json:"tag"`
	Missing []string `json:"missing,omitempty"`
	Message string   `json:"message"`
}

// Dry reads a Request and writes the rules this build can't build, as a JSON
// array (empty when all can be). It touches no files but the geo data, and changes
// nothing: it is the dry run of what the daemon does before it starts Xray, and as the
// new version's own code it also catches what a newer Xray no longer accepts.
func Dry(in io.Reader, out io.Writer) error {
	var req Request
	if err := json.NewDecoder(io.LimitReader(in, 8<<20)).Decode(&req); err != nil {
		return err
	}
	_ = os.Setenv("XRAY_LOCATION_ASSET", req.AssetDir)
	rules := make([]store.Rule, len(req.Rules))
	for i, r := range req.Rules {
		rules[i] = store.Rule{Tag: r.Tag, Match: r.Match}
	}
	data := Data{GeoIP: filepath.Join(req.AssetDir, "geoip.dat"), GeoSite: filepath.Join(req.AssetDir, "geosite.dat")}

	problems := []Report{}
	broken := map[string]bool{}
	for _, p := range Check(rules, data) {
		broken[p.Tag] = true
		problems = append(problems, Report{Tag: p.Tag, Missing: p.Missing, Message: p.Message})
	}
	// What a category lookup can't see: this Xray refusing the rule outright.
	for _, r := range rules {
		if broken[r.Tag] {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(r.Match, &m); err != nil {
			problems = append(problems, Report{Tag: r.Tag, Message: "not a JSON object"})
			continue
		}
		m = maps.Clone(m)
		m["outboundTag"] = json.RawMessage(`"direct"`)
		full, _ := json.Marshal(m)
		if err := xray.ValidateRule(full); err != nil {
			problems = append(problems, Report{Tag: r.Tag, Message: err.Error()})
		}
	}
	return json.NewEncoder(out).Encode(problems)
}
