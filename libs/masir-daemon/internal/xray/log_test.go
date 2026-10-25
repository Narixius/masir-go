package xray

import (
	"testing"

	clog "github.com/xtls/xray-core/common/log"
)

func TestLogSinkLevels(t *testing.T) {
	var gotLevel, gotMsg string
	h := &teeHandler{sink: func(level, msg string) {
		gotLevel, gotMsg = level, msg
	}}
	h.Handle(&clog.GeneralMessage{Severity: clog.Severity_Warning, Content: "there is a problem"})
	if gotLevel != "warn" || gotMsg != "there is a problem" {
		t.Fatalf("warning = %q %q", gotLevel, gotMsg)
	}
	h.Handle(&clog.GeneralMessage{Severity: clog.Severity_Error, Content: "it failed"})
	if gotLevel != "error" || gotMsg != "it failed" {
		t.Fatalf("error = %q %q", gotLevel, gotMsg)
	}
}
