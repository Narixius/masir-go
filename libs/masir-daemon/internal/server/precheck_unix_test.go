//go:build !windows

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMasir writes a "new version" that answers `daemon check` with stdout, or fails.
func fakeMasir(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestUpdateIsRefusedWhenTheNewVersionCantBuildTheRules(t *testing.T) {
	ctx := context.Background()
	s, _ := geoServer(t, []string{"ir"}, []string{"google"})
	addRule(t, s, "ssh", `{"port":"22"}`, "direct")

	for name, c := range map[string]struct {
		script string
		want   string // "" accepted
	}{
		"builds them all":   {`cat >/dev/null; echo '[]'`, ""},
		"can't build one":   {`cat >/dev/null; echo '[{"tag":"ssh","message":"port syntax changed"}]'`, "can't build 1 of your rules (ssh: port syntax changed)"},
		"crashes":           {`echo 'illegal instruction' >&2; exit 4`, "couldn't be checked against your rules, so it wasn't installed: illegal instruction"},
		"answers with junk": {`cat >/dev/null; echo 'hello'`, "unreadable"},
		"isn't a masir":     {`exit 0`, "unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			err := s.checkWithNewVersion(ctx, fakeMasir(t, c.script))
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestTheNewVersionIsSentTheEnabledRulesAndTheGeoDir(t *testing.T) {
	ctx := context.Background()
	s, geoDir := geoServer(t, []string{"ir"}, []string{"google"})
	addRule(t, s, "on", `{"port":"22"}`, "direct")
	addRule(t, s, "off", `{"port":"23"}`, "direct")
	if err := s.state.SetRuleEnabled("off", false, "x"); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(t.TempDir(), "stdin.json")
	bin := fakeMasir(t, `cat > '`+got+`'; echo '[]'`)
	if err := s.checkWithNewVersion(ctx, bin); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(got)
	if !strings.Contains(string(raw), `"tag":"on"`) || strings.Contains(string(raw), `"tag":"off"`) || !strings.Contains(string(raw), geoDir) {
		t.Fatalf("the new version was sent %s", raw)
	}
}

func TestNoRulesNothingToCheck(t *testing.T) {
	s, _ := geoServer(t, []string{"ir"}, []string{"google"})
	if err := s.checkWithNewVersion(context.Background(), "/nonexistent/masir"); err != nil {
		t.Fatalf("with no rules the new version isn't even run: %v", err)
	}
}
