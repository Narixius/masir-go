// Package rulecheck verifies, before Xray is started or restarted, that every enabled
// rule can be built with the geo data on disk. Xray builds a whole config or none, so
// one rule using a category the data lacks would stop the proxy; this finds which rules,
// and which categories, without starting anything.
package rulecheck

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/geodata"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
)

// Data names the geo files to check rules against.
type Data struct {
	GeoIP, GeoSite string // paths of geoip.dat and geosite.dat
}

// Problem is an enabled rule that can't be built.
type Problem struct {
	Tag     string
	Missing []string // the references that can't be resolved, e.g. "geoip:in-ir"
	Message string   // what is wrong and, where known, what to use instead
}

// Ref is one geoip: or geosite: reference in a rule.
type Ref struct {
	Kind string // "geoip" or "geosite"
	Code string // lower case; without a leading "!" or a trailing "@attribute"
	Raw  string // as written in the rule
}

func (r Ref) String() string { return r.Kind + ":" + r.Code }

// Refs lists the geoip: and geosite: references in a rule's match. References Xray
// loads from other files (ext:, ext-ip:, ext-domain:) are not checked here.
func Refs(match json.RawMessage) []Ref {
	var m map[string]json.RawMessage
	if json.Unmarshal(match, &m) != nil {
		return nil
	}
	var refs []Ref
	for key, raw := range m {
		var kind string
		switch strings.ToLower(key) {
		case "domain", "domains":
			kind = "geosite"
		case "ip", "source", "sourceip":
			kind = "geoip"
		default:
			continue
		}
		for _, item := range stringList(raw) {
			rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(item)), kind+":")
			if !ok {
				continue
			}
			rest = strings.TrimPrefix(rest, "!")
			if i := strings.IndexByte(rest, '@'); i >= 0 {
				rest = rest[:i]
			}
			if rest != "" {
				refs = append(refs, Ref{Kind: kind, Code: rest, Raw: item})
			}
		}
	}
	slices.SortFunc(refs, func(a, b Ref) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(refs)
}

func stringList(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	return nil
}

// Check returns the enabled rules that use a category the data doesn't have, in rule
// order. Disabled rules are skipped: they aren't part of any config.
func Check(rules []store.Rule, d Data) []Problem {
	var problems []Problem
	var ip, site catalog
	for _, r := range rules {
		if r.Disabled {
			continue
		}
		refs := Refs(r.Match)
		if len(refs) == 0 {
			continue
		}
		var missing []string
		var notes []string
		for _, ref := range refs {
			cat := &ip
			path, file := d.GeoIP, "geoip.dat"
			if ref.Kind == "geosite" {
				cat, path, file = &site, d.GeoSite, "geosite.dat"
			}
			if err := cat.load(path); err != nil {
				missing = append(missing, ref.String())
				notes = append(notes, fmt.Sprintf("%s can't be read: %v", file, err))
				continue
			}
			if !cat.has(ref.Code) {
				missing = append(missing, ref.String())
				note := fmt.Sprintf("%s isn't in %s", ref, file)
				if similar := cat.similar(ref.Code); len(similar) > 0 {
					note += " (similar: " + strings.Join(similar, ", ") + ")"
				}
				notes = append(notes, note)
			}
		}
		if len(missing) > 0 {
			problems = append(problems, Problem{Tag: r.Tag, Missing: missing, Message: strings.Join(dedupe(notes), "; ")})
		}
	}
	return problems
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// catalog is the set of codes in one geo file, read on first use.
type catalog struct {
	loaded bool
	err    error
	codes  map[string]bool
}

func (c *catalog) load(path string) error {
	if c.loaded {
		return c.err
	}
	c.loaded = true
	cats, err := geodata.Categories(path)
	if err != nil {
		c.err = err
		return err
	}
	c.codes = make(map[string]bool, len(cats))
	for _, cat := range cats {
		c.codes[strings.ToLower(cat.Code)] = true
	}
	return nil
}

func (c *catalog) has(code string) bool { return c.codes[code] }

// similar suggests codes the user may have meant: the parts of a compound code
// ("in-ir" → "ir"), and codes that contain it.
func (c *catalog) similar(code string) []string {
	var out []string
	for _, part := range strings.Split(code, "-") {
		if part != "" && part != code && c.codes[part] && !slices.Contains(out, part) {
			out = append(out, part)
		}
	}
	return out
}
