//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// selinuxEnforce is the file whose presence means SELinux is here and whose content
// says whether it enforces. Tests point it somewhere else.
var selinuxEnforce = "/sys/fs/selinux/enforce"

// runLabelCommand runs one labeling tool. Tests replace it to see the calls.
var runLabelCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// selinuxEnforcing reports whether SELinux is on and enforcing here, which is the
// precondition for every label this file cares about.
func selinuxEnforcing() bool {
	raw, err := os.ReadFile(selinuxEnforce)
	return err == nil && strings.TrimSpace(string(raw)) == "1"
}

// labelServiceBinary makes SELinux treat the service's copy of masir as a normal
// executable, so the daemon lands in a domain that may use the network.
//
// The service installs into /usr/local/lib/masir, and a file created there carries
// the directory's default label, lib_t: a library type, not the entry point of any
// service domain. systemd then starts the daemon in the confined init_t, which the
// policy does not allow to open sockets — every connection fails with "permission
// denied" while the daemon otherwise looks healthy. Services whose executable
// carries bin_t (earlyoom, tailscaled, keyd in /usr/bin and /usr/local/bin) start in
// unconfined_service_t instead, which is where a daemon that runs the machine's
// proxy belongs. docs/selinux.md has the full story.
//
// chcon fixes the copy just written; a fcontext rule keeps the next relabel from
// undoing it. The rule is best effort: semanage lives in policycoreutils-python-utils,
// which not every machine has, and without it the label lasts until the next full
// relabel — which the next install or update re-applies anyway.
func labelServiceBinary(bin string) error {
	raw, err := os.ReadFile(selinuxEnforce)
	if err != nil {
		return nil // no SELinux here: nothing to label, nothing to fail
	}
	enforcing := strings.TrimSpace(string(raw)) == "1"
	if err := runLabelCommand("chcon", "-t", "bin_t", bin); err != nil {
		// Permissive never blocks the daemon, so a failed label is cosmetic there.
		if enforcing {
			return fmt.Errorf("SELinux enforces but would not label %s for the service: %w", bin, err)
		}
		return nil
	}
	// Best effort: fails when semanage is absent, or when the rule already exists —
	// both fine, the label is set either way.
	_ = runLabelCommand("semanage", "fcontext", "-a", "-t", "bin_t", bin)
	return nil
}
