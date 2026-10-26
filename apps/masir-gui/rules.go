package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// ruleSection is a titled group of rules in the list.
type ruleSection struct {
	title string
	rules []*pb.RoutingRule
}

// info is how a rule is drawn: its title, the icon or flag of its tile, and a note.
type info struct {
	title, summary, flag string
	icon                 *ui.SVG
	section              string
	appPath              string // the application's bundle, for an application rule
}

// ruleInfo reads what a rule matches out of its xray match: its first process, domain,
// address or port.
func ruleInfo(r *pb.RoutingRule) info {
	out := info{title: r.RuleTag, icon: iconGlobe, section: "Domains"}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(r.RuleJson), &m) != nil {
		return out
	}
	list := func(key string) []string {
		raw, ok := m[key]
		if !ok {
			return nil
		}
		var l []string
		if json.Unmarshal(raw, &l) == nil {
			return l
		}
		var one string
		if json.Unmarshal(raw, &one) == nil && one != "" {
			return strings.Split(one, ",")
		}
		return nil
	}
	more := func(l []string) string {
		if len(l) > 1 {
			return fmt.Sprintf(" +%d", len(l)-1)
		}
		return ""
	}
	switch {
	case len(list("process")) > 0:
		l := list("process")
		out.title, out.icon, out.section = strings.TrimSuffix(strings.TrimPrefix(l[0], "self/"), "/")+more(l), iconApp, "Applications"
		if strings.HasSuffix(strings.TrimSuffix(l[0], "/"), ".app") {
			out.appPath = l[0]
			out.title = fallbackName(l[0]) + more(l)
		}
	case len(list("domain")) > 0:
		l := list("domain")
		first := l[0]
		out.section = "Domains"
		if strings.HasPrefix(first, "geosite:") {
			out.section = "Website Categories"
		}
		for _, prefix := range []string{"geosite:", "full:", "domain:", "regexp:"} {
			first = strings.TrimPrefix(first, prefix)
		}
		out.title = first + more(l)
	case len(list("ip")) > 0:
		l := list("ip")
		out.section, out.icon = "IP Ranges & Ports", iconLayers
		first := strings.TrimPrefix(l[0], "geoip:")
		if code := strings.TrimPrefix(l[0], "geoip:"); strings.HasPrefix(l[0], "geoip:") && len(code) == 2 {
			out.flag = flagFor(code)
		}
		out.title = first + more(l)
	case len(list("port")) > 0:
		l := list("port")
		out.section, out.icon = "IP Ranges & Ports", iconHash
		out.title = l[0] + more(l)
	}
	if n := list("network"); len(n) > 0 {
		out.summary = strings.ToUpper(strings.Join(n, "/"))
	}
	if out.section == "Applications" || out.section == "Domains" || out.section == "Website Categories" {
		if ip := list("ip"); len(ip) > 0 && out.section != "IP Ranges & Ports" {
			out.summary = strings.TrimSpace(out.summary + " " + ip[0])
		}
	}
	return out
}

// flagFor is the flag emoji of a two-letter country code.
func flagFor(code string) string {
	code = strings.ToUpper(code)
	return string([]rune{rune(code[0]-'A') + 0x1F1E6, rune(code[1]-'A') + 0x1F1E6})
}

// groupRules gathers rules into the sections the list shows, in a fixed order.
func groupRules(rules []*pb.RoutingRule) []ruleSection {
	order := []string{"Applications", "Website Categories", "Domains", "IP Ranges & Ports"}
	by := map[string][]*pb.RoutingRule{}
	for _, r := range rules {
		s := ruleInfo(r).section
		by[s] = append(by[s], r)
	}
	var out []ruleSection
	for _, title := range order {
		if len(by[title]) > 0 {
			out = append(out, ruleSection{title: title, rules: by[title]})
		}
	}
	return out
}
