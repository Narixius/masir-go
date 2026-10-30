//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A machine without SELinux must be a no-op: no commands run, no error, so the
// service install never depends on labeling tools it doesn't have.
func TestLabelServiceBinarySkipsWithoutSELinux(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	selinuxEnforce = filepath.Join(t.TempDir(), "not-there")
	var calls []string
	runLabelCommand = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if err := labelServiceBinary("/opt/masir/masir"); err != nil {
		t.Fatalf("label without SELinux: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("ran %v with no SELinux", calls)
	}
}

// The label itself (chcon) and the persistent rule (semanage fcontext) both go out,
// in that order — chcon is what takes effect now, the rule survives a relabel.
func TestLabelServiceBinaryLabelsAndPersists(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	var calls []string
	runLabelCommand = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if err := labelServiceBinary("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("label: %v", err)
	}
	want := []string{
		"chcon -t bin_t /usr/local/lib/masir/masir",
		"semanage fcontext -a -t bin_t /usr/local/lib/masir/masir",
	}
	if len(calls) != len(want) {
		t.Fatalf("ran %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}

// semanage not being installed must not fail the install: the label is already
// applied by chcon, and the persistent rule is only the belt to those braces.
func TestLabelServiceBinaryPersistsBestEffort(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	runLabelCommand = func(name string, _ ...string) error {
		if name == "semanage" {
			return errors.New("executable file not found in $PATH")
		}
		return nil
	}
	if err := labelServiceBinary("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("label with no semanage: %v", err)
	}
}

// Enforcing with chcon itself failing means the daemon would start confined and
// every connection would be denied: that install must fail loudly, not ship broken.
func TestLabelServiceBinaryFailsWhenEnforcedLabelFails(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "1")
	runLabelCommand = func(name string, _ ...string) error {
		if name == "chcon" {
			return errors.New("permission denied")
		}
		return nil
	}
	if err := labelServiceBinary("/usr/local/lib/masir/masir"); err == nil {
		t.Fatal("label failed while enforcing; want an error")
	}
}

// Permissive never blocks a connection, so a failed label there is a shrug:
// the install proceeds.
func TestLabelServiceBinaryPermissiveStaysQuiet(t *testing.T) {
	oldEnforce, oldRun := selinuxEnforce, runLabelCommand
	t.Cleanup(func() { selinuxEnforce, runLabelCommand = oldEnforce, oldRun })
	writeEnforce(t, "0")
	runLabelCommand = func(string, ...string) error {
		return errors.New("permission denied")
	}
	if err := labelServiceBinary("/usr/local/lib/masir/masir"); err != nil {
		t.Fatalf("permissive label error: %v", err)
	}
}

func writeEnforce(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "enforce")
	if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selinuxEnforce = p
}

// The error that sent this whole investigation astray — a denied dial reported as
// "permission denied" — must point at SELinux, not at the generic file-permission
// advice. While SELinux enforces, anyway; without it the generic advice stands.
func TestExplainDialDenialNamesSELinux(t *testing.T) {
	oldEnforce := selinuxEnforce
	t.Cleanup(func() { selinuxEnforce = oldEnforce })

	msg := `transport: Error while dialing: dial tcp 127.0.0.1:40489: connect: permission denied`

	writeEnforce(t, "1")
	adv := explain(msg)
	if !strings.Contains(adv.text, "SELinux") {
		t.Fatalf("enforcing advice = %q; want it to name SELinux", adv.text)
	}
	if len(adv.cmds) != 1 || adv.cmds[0] != "masir daemon enable" {
		t.Fatalf("enforcing advice cmds = %v; want [masir daemon enable]", adv.cmds)
	}

	writeEnforce(t, "0")
	if adv := explain(msg); strings.Contains(adv.text, "SELinux") {
		t.Fatalf("permissive advice = %q; SELinux blocks nothing there", adv.text)
	}
}
