package main

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExplainCommonFailures(t *testing.T) {
	for msg, want := range map[string]string{
		"open /usr/local/bin/masir.new: permission denied": "permission",
		"read-only file system":                            "read-only",
		"no space left on device":                          "disk is full",
		"i/o timeout":                                      "timed out",
		"address already in use":                           "taken",
		"failed to load code geosite:category-nope":        "geo data",
	} {
		if got := explain(msg); !strings.Contains(strings.ToLower(got.plain()), strings.ToLower(want)) {
			t.Errorf("explain(%q) = %q; missing %q", msg, got.plain(), want)
		}
	}
	if got := explain("something ordinary"); !got.empty() {
		t.Errorf("explain of an ordinary error = %q", got.plain())
	}
}

// Commands are never buried in a sentence: they stand on lines of their own, where
// selecting the line copies exactly the command.
func TestAdviceKeepsCommandsOnTheirOwnLines(t *testing.T) {
	msgs := []string{
		"failed to add system route: file exists", "listen tcp :7890: bind: address already in use", "address already in use",
		"operation not permitted tun", "wintun.dll missing", "failed to load code geoip:x", "failed to check code IN-IR from geoip.dat",
		"geosite.dat not found", "permission denied", "read-only file system", "no space left on device", "text file busy",
		"being used by another process", "no such host", "connection refused", "i/o timeout", "certificate", "tls handshake", "403",
	}
	for _, msg := range msgs {
		adv := explain(msg)
		if adv.empty() {
			t.Errorf("explain(%q) gave no advice", msg)
		}
		if strings.Contains(adv.text, "`") {
			t.Errorf("explain(%q) has a command inside the sentence: %q", msg, adv.text)
		}
		for _, c := range adv.cmds {
			if !strings.HasPrefix(c, "masir ") && !strings.HasPrefix(c, "sudo masir ") {
				t.Errorf("explain(%q): %q isn't a command to run", msg, c)
			}
		}
		if len(adv.cmds) > 0 && !strings.HasSuffix(adv.text, ":") && !strings.HasSuffix(adv.text, "once:") {
			t.Errorf("explain(%q): the sentence before the commands should end with a colon: %q", msg, adv.text)
		}
	}
	for _, a := range []advice{staleDaemonAdvice()} {
		if strings.Contains(a.text, "`") || len(a.cmds) == 0 {
			t.Errorf("stale daemon advice = %+v", a)
		}
	}
}

func TestDescribe(t *testing.T) {
	msg, adv := describe(status.Error(codes.Unavailable, "connection error: no daemon"))
	if !strings.Contains(msg, "Can't reach") || len(adv.cmds) != 1 || adv.cmds[0] != "masir daemon start" {
		t.Fatalf("describe = %q, %+v", msg, adv)
	}
	msg, adv = describe(status.Error(codes.InvalidArgument, "background worker: permission denied"))
	if !strings.Contains(msg, "permission denied") || !strings.Contains(adv.text, "permission") {
		t.Fatalf("describe = %q, %+v", msg, adv)
	}
}

func TestUserErrorReadsAsPlainLines(t *testing.T) {
	err := &userError{
		headline: "1 rule can't run with the installed geo data.",
		details:  "iran-direct\n  geoip:in-ir isn't in geoip.dat",
		advice:   advice{"To go on, turn them off, or look at them first:", []string{"masir rules disable iran-direct", "masir rules check"}},
		note:     "Nothing was changed.",
	}
	want := "1 rule can't run with the installed geo data.\n" +
		"iran-direct\n  geoip:in-ir isn't in geoip.dat\n" +
		"To go on, turn them off, or look at them first:\n  masir rules disable iran-direct\n  masir rules check\n" +
		"Nothing was changed."
	if got := message(err); got != want {
		t.Fatalf("message =\n%s\nwant\n%s", got, want)
	}
	if msg, adv := describe(err); msg != err.headline || len(adv.cmds) != 2 {
		t.Fatalf("describe = %q, %+v", msg, adv)
	}
}

func TestAdvisedBuildsAFailureWithCommands(t *testing.T) {
	err := advised("No rules yet.", "Add one:", "masir rules add")
	ue, ok := asUserError(err)
	if !ok || ue.headline != "No rules yet." || ue.advice.cmds[0] != "masir rules add" {
		t.Fatalf("err = %#v", err)
	}
}
