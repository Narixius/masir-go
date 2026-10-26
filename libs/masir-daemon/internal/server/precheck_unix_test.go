//go:build !windows

package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/update"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
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
		"can't build one":   {`cat >/dev/null; echo '[{"tag":"ssh","message":"port syntax changed"}]'`, "can't build 1 of your rules"},
		"crashes":           {`echo 'illegal instruction' >&2; exit 4`, "couldn't be checked against your rules, so nothing was installed: illegal instruction"},
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
			// The rules come back as data, for the client to list, not only as text.
			var re *update.RulesError
			if c.want == "can't build 1 of your rules" {
				if !errors.As(err, &re) || len(re.Problems) != 1 || re.Problems[0].RuleTag != "ssh" || re.Problems[0].Message != "port syntax changed" {
					t.Fatalf("err = %#v, want a RulesError naming ssh", err)
				}
			} else if errors.As(err, &re) {
				t.Fatalf("err = %v is a RulesError, but no rule was named", err)
			}
		})
	}
}

func TestTheNewVersionIsSentTheEnabledRulesAndTheGeoDir(t *testing.T) {
	ctx := context.Background()
	s, geoDir := geoServer(t, []string{"ir"}, []string{"google"})
	addRule(t, s, "on", `{"port":"22"}`, "direct")
	addRule(t, s, "off", `{"port":"23"}`, "direct")
	if _, err := s.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: []string{"off"}, Enabled: false, Reason: "x"}); err != nil {
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

// A rule added by editing routing.json while the daemon runs isn't in its memory, but it is
// what the new version will load when it starts, so it is what the new version is asked about.
func TestUpdateChecksTheSavedRulesNotOnlyTheDaemonsCopy(t *testing.T) {
	ctx := context.Background()
	s, _ := geoServer(t, []string{"ir"}, []string{"google"})
	addRule(t, s, "ssh", `{"port":"22"}`, "direct")

	saved, err := store.Load(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.AddRule("hand-edited", []byte(`{"port":"23"}`), store.Target{Kind: store.TargetDirect}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(s.dir, saved); err != nil { // as if nano had written it
		t.Fatal(err)
	}
	if len(s.state.Rules) != 1 {
		t.Fatalf("setup: the daemon should not know about the edit, it has %d rules", len(s.state.Rules))
	}

	bin := fakeMasir(t, `if grep -q hand-edited; then echo '[{"tag":"hand-edited","message":"unreadable"}]'; else echo '[]'; fi`)
	var re *update.RulesError
	err = s.checkWithNewVersion(ctx, bin)
	if !errors.As(err, &re) || len(re.Problems) != 1 || re.Problems[0].RuleTag != "hand-edited" {
		t.Fatalf("err = %v, want the hand-edited rule named", err)
	}
}
