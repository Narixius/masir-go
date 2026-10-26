package rulecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	xgeo "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
)

// data writes small geoip.dat and geosite.dat files with these codes.
func data(t *testing.T, ips, sites []string) Data {
	t.Helper()
	dir := t.TempDir()
	var ipl xgeo.GeoIPList
	for _, c := range ips {
		ipl.Entry = append(ipl.Entry, &xgeo.GeoIP{Code: strings.ToUpper(c), Cidr: []*xgeo.CIDR{{Ip: []byte{1, 2, 3, 0}, Prefix: 24}}})
	}
	var sl xgeo.GeoSiteList
	for _, c := range sites {
		sl.Entry = append(sl.Entry, &xgeo.GeoSite{Code: strings.ToUpper(c), Domain: []*xgeo.Domain{{Value: "a.com"}}})
	}
	d := Data{GeoIP: filepath.Join(dir, "geoip.dat"), GeoSite: filepath.Join(dir, "geosite.dat")}
	for path, m := range map[string]proto.Message{d.GeoIP: &ipl, d.GeoSite: &sl} {
		b, _ := proto.Marshal(m)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func rule(tag, match string) store.Rule {
	return store.Rule{Tag: tag, Match: json.RawMessage(match)}
}

func TestRefs(t *testing.T) {
	for name, c := range map[string]struct {
		match string
		want  []string
	}{
		"none":                {`{"port":"22","process":["Telegram"]}`, nil},
		"geoip and geosite":   {`{"domain":["geosite:google","full:a.com"],"ip":["geoip:ir","1.2.3.4"]}`, []string{"geoip:ir", "geosite:google"}},
		"negation, attribute": {`{"ip":["geoip:!cn"],"domain":["geosite:google@ads"]}`, []string{"geoip:cn", "geosite:google"}},
		"upper case":          {`{"ip":["GeoIP:IR"]}`, []string{"geoip:ir"}},
		"source":              {`{"source":["geoip:private"]}`, []string{"geoip:private"}},
		"ext is skipped":      {`{"domain":["ext:my.dat:ads"],"ip":["ext-ip:my.dat:x"]}`, nil},
		"a single string":     {`{"ip":"geoip:de"}`, []string{"geoip:de"}},
		"repeated":            {`{"domain":["geosite:a","geosite:a"]}`, []string{"geosite:a"}},
	} {
		t.Run(name, func(t *testing.T) {
			var got []string
			for _, r := range Refs(json.RawMessage(c.match)) {
				got = append(got, r.String())
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Refs = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCheckFindsRulesUsingMissingCategories(t *testing.T) {
	d := data(t, []string{"ir", "de", "private"}, []string{"google", "category-ir"})
	rules := []store.Rule{
		rule("ok", `{"domain":["geosite:google"],"ip":["geoip:de"]}`),
		rule("iran-direct", `{"ip":["geoip:ir","geoip:in-ir","geoip:blacklist-ir"]}`),
		rule("ports", `{"port":"22"}`),
		rule("gone-site", `{"domain":["geosite:category-ai-cn"]}`),
	}
	got := Check(rules, d)
	if len(got) != 2 || got[0].Tag != "iran-direct" || got[1].Tag != "gone-site" {
		t.Fatalf("problems = %+v", got)
	}
	if !reflect.DeepEqual(got[0].Missing, []string{"geoip:blacklist-ir", "geoip:in-ir"}) {
		t.Errorf("missing = %v", got[0].Missing)
	}
	if !strings.Contains(got[0].Message, "geoip:in-ir isn't in geoip.dat (similar: ir)") {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestCheckSkipsDisabledRules(t *testing.T) {
	d := data(t, []string{"de"}, []string{"google"})
	r := rule("old", `{"ip":["geoip:in-ir"]}`)
	r.Disabled = true
	if got := Check([]store.Rule{r}, d); len(got) != 0 {
		t.Fatalf("a disabled rule was checked: %+v", got)
	}
}

func TestCheckWithUnreadableData(t *testing.T) {
	d := Data{GeoIP: filepath.Join(t.TempDir(), "missing.dat"), GeoSite: filepath.Join(t.TempDir(), "missing2.dat")}
	got := Check([]store.Rule{rule("a", `{"ip":["geoip:de"]}`), rule("b", `{"port":"22"}`)}, d)
	if len(got) != 1 || got[0].Tag != "a" || !strings.Contains(got[0].Message, "geoip.dat can't be read") {
		t.Fatalf("problems = %+v", got)
	}
}
