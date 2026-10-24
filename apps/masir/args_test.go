package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// parse runs argv through cobra the way a command receives it.
func parse(t *testing.T, argv []string, specs ...flagSpec) args {
	t.Helper()
	c := &cobra.Command{Use: "x"}
	addFlags(c, specs)
	if err := c.ParseFlags(argv); err != nil {
		t.Fatal(err)
	}
	return argsFrom(c, c.Flags().Args())
}

func TestArgsFromCobra(t *testing.T) {
	a := parse(t, []string{"https://x", "--name", "My Sub", "-p", "--no-check", "--domain=a.com,b.com", "--domain", "c.com", "--", "--literal"},
		flagSpec{names: []string{"name"}}, flagSpec{names: []string{"proxy", "p"}, bool: true},
		flagSpec{names: []string{"check"}, bool: true, def: true}, flagSpec{names: []string{"no-check"}, bool: true},
		flagSpec{names: []string{"domain"}, many: true})
	if !slices.Equal(a.pos, []string{"https://x", "--literal"}) {
		t.Errorf("pos = %v", a.pos)
	}
	if a.str("name") != "My Sub" || !a.boolean("proxy", false) || a.boolean("check", true) {
		t.Errorf("flags = %v", a.flags)
	}
	if got := a.list("domain"); !slices.Equal(got, []string{"a.com", "b.com", "c.com"}) {
		t.Errorf("domain = %v", got)
	}
	if a.has("missing") || a.boolean("tun", true) != true {
		t.Error("flags that weren't given should be absent and fall back to their default")
	}
}

func TestCommandTree(t *testing.T) {
	root := rootCmd()
	for _, path := range [][]string{
		{"subs", "ls"}, {"subscription", "rm"}, {"start"}, {"stop"}, {"config"},
		{"rules", "ls"}, {"rules", "rm"}, {"daemon", "install"}, {"daemon", "reload"}, {"settings", "ls"},
	} {
		c, _, err := root.Find(path)
		if err != nil || c == root {
			t.Errorf("masir %s doesn't resolve: %v", strings.Join(path, " "), err)
		}
	}
}

func TestSlugAndNames(t *testing.T) {
	if got := slug("🇮🇷 Iranian websites-direct"); got != "iranian-websites-direct" {
		t.Errorf("slug = %q", got)
	}
	if got := slug("masir-x"); got != "rule-masir-x" {
		t.Errorf("reserved prefix: %q", got)
	}
}

func TestRuleMatchFlags(t *testing.T) {
	a := parse(t, []string{"--domain", "geosite:netflix,example.com", "--port", "443", "--process", "/Applications/My App.app/"}, matcherFlags...)
	m := ruleMatch{}
	if _, err := matchFromFlags(a, m, false); err != nil {
		t.Fatal(err)
	}
	if got := m.values("domain"); !slices.Equal(got, []string{"geosite:netflix", "example.com"}) {
		t.Errorf("domain = %v", got)
	}
	if m["port"] != "443" {
		t.Errorf("port should stay a string in xray's format: %#v", m["port"])
	}
	if got := m.values("process"); !slices.Equal(got, []string{"/Applications/My App.app/"}) {
		t.Errorf("process = %v", got)
	}

	bad := parse(t, []string{"--port", "99999"}, matcherFlags...)
	if _, err := matchFromFlags(bad, ruleMatch{}, false); err == nil {
		t.Error("an out-of-range port should fail")
	}
}
