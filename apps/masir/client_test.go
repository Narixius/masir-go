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
		if got := explain(msg); !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("explain(%q) = %q; missing %q", msg, got, want)
		}
	}
	if got := explain("something ordinary"); got != "" {
		t.Errorf("explain of an ordinary error = %q", got)
	}
}

func TestDescribe(t *testing.T) {
	msg, hint := describe(status.Error(codes.Unavailable, "connection error: no daemon"))
	if !strings.Contains(msg, "Can't reach") || !strings.Contains(hint, "daemon start") {
		t.Fatalf("describe = %q, %q", msg, hint)
	}
	msg, hint = describe(status.Error(codes.InvalidArgument, "background worker: permission denied"))
	if !strings.Contains(msg, "permission denied") || !strings.Contains(hint, "permission") {
		t.Fatalf("describe = %q, %q", msg, hint)
	}
}
