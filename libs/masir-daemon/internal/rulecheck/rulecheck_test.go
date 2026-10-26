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
	d := data(t, []string{"ir", "in", "no", "de", "private"}, []string{"google", "category-ir"}) // "in" (India) exists, and is not what in-ir meant
	rules := []store.Rule{
		rule("ok", `{"domain":["geosite:google"],"ip":["geoip:de"]}`),
		rule("iran-direct", `{"ip":["geoip:ir","geoip:in-ir","geoip:blacklist-ir"]}`),
		rule("ports", `{"port":"22"}`),
		rule("gone-site", `{"domain":["geosite:category-ai-cn"]}`),
		rule("no-guess", `{"ip":["geoip:no-such-country"]}`),
	}
	got := Check(rules, d)
	if len(got) != 3 || got[0].Tag != "iran-direct" || got[1].Tag != "gone-site" || got[2].Tag != "no-guess" {
		t.Fatalf("problems = %+v", got)
	}
	if !reflect.DeepEqual(got[0].Missing, []string{"geoip:blacklist-ir", "geoip:in-ir"}) {
		t.Errorf("missing = %v", got[0].Missing)
	}
	// Existing categories that merely share a part with the code are not suggestions.
	for _, p := range got {
		if strings.Contains(p.Message, "did you mean") {
			t.Errorf("%s: a guess from the meaning of a code: %q", p.Tag, p.Message)
		}
	}
	if !strings.Contains(got[0].Message, "geoip:in-ir isn't in geoip.dat") {
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

// Suggestions only fix typing. A wrong one is worse than none: on a direct rule it could
// widen what bypasses the proxy.
func TestSuggestionsOnlyFixTypos(t *testing.T) {
	d := data(t, []string{"ir", "in", "no", "cn", "ru", "private", "abcde1", "abcde2"},
		[]string{"google", "netflix", "cn", "category-ai-cn", "category-ads-all"})
	for ref, want := range map[string]string{
		// typos: one character off, or a different case or separator
		"geoip:privat":            "geoip:private",
		"geoip:prvate":            "geoip:private",
		"geoip:privaet":           "geoip:private",
		"geoip:privatte":          "geoip:private",
		"geosite:gooogle":         "geosite:google",
		"geosite:netflx":          "geosite:netflix",
		"geosite:category_ai_cn":  "geosite:category-ai-cn",
		"geosite:category-ads-al": "geosite:category-ads-all",
		// not typos: other categories that happen to share a part, or sit close in length
		"geoip:in-ir":              "",
		"geoip:blacklist-ir":       "",
		"geoip:google-cn":          "",
		"geoip:telegram-ru":        "",
		"geoip:no-such-country":    "",
		"geosite:category-ai-chat": "",
		"geosite:category-ir":      "",
		// too short to tell, ambiguous between two, or too far
		"geoip:xx":        "",
		"geoip:de":        "",
		"geoip:abcde":     "", // abcde1 and abcde2 are equally close
		"geoip:privatez9": "",
	} {
		kind, code, _ := strings.Cut(ref, ":")
		field := "ip"
		if kind == "geosite" {
			field = "domain"
		}
		match := `{"` + field + `":["` + ref + `"]}`
		got := Check([]store.Rule{rule("r", match)}, d)
		if len(got) != 1 {
			t.Fatalf("%s: problems = %+v", ref, got)
		}
		msg := got[0].Message
		if want == "" {
			if strings.Contains(msg, "did you mean") {
				t.Errorf("%s: suggested something: %q", ref, msg)
			}
			continue
		}
		if !strings.Contains(msg, "(did you mean "+want+"?)") {
			t.Errorf("%s (%s): message = %q, want a suggestion of %s", ref, code, msg, want)
		}
	}
}

func TestOneEditApart(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"private", "privat", true}, {"privat", "private", true}, {"private", "privete", true}, {"private", "pirvate", true},
		{"private", "private", false}, {"private", "privaets", false}, {"private", "prvt", false}, {"abc", "abd", true},
		{"abcd", "abdc", true}, {"abcd", "badc", false}, {"a", "", true}, {"ab", "", false},
	} {
		if got := oneEditApart(c.a, c.b); got != c.want {
			t.Errorf("oneEditApart(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
