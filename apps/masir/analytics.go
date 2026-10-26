package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/analytics"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// maybeAnalyticsNotice shows the anonymous analytics notice once per install on the
// first interactive command. It is skipped when analytics is off. The daemon sends
// nothing until this has been shown, so this is what switches analytics on.
func maybeAnalyticsNotice() {
	if !ui.Interactive() || !analyticsEnabledCLI() {
		return
	}
	dir, err := paths.ConfigDir()
	if err != nil || analytics.NoticeShown(dir) {
		return
	}
	if analytics.MarkNoticeShown(dir) != nil {
		return // better silent than tracked without having told the user
	}
	ui.Note("masir sends anonymous usage and error data to improve it.\n"+
		"Turn it off with `masir settings set analytics off`.",
		ui.Yellow("▲ Anonymous analytics"))
}

// captureEvent sends an event through a running daemon, or spools it for later.
func captureEvent(name string, props map[string]any) {
	if !analyticsAllowedCLI() {
		return
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return
	}
	if c, ok := runningDaemon(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, callErr := c.CaptureEvent(ctx, &pb.CaptureEventRequest{
			Event:          name,
			PropertiesJson: string(raw),
			TimestampMs:    time.Now().UnixMilli(),
		})
		cancel()
		if callErr == nil {
			return
		}
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return
	}
	_ = analytics.WriteSpool(dir, analytics.Event{
		Event:      name,
		Properties: props,
		CreatedAt:  time.Now(),
	})
}

func errorProps(err error) map[string]any {
	props := map[string]any{"error_kind": errorKind(err)}
	if code := status.Code(err); code != codes.OK && code != codes.Unknown {
		props["grpc_code"] = code.String()
	}
	if reason := errorReason(err); reason != "" {
		props["error_reason"] = reason
	}
	props["error_fingerprint"] = analytics.Fingerprint(message(err))
	return props
}

func errorKind(err error) string {
	if err == nil {
		return ""
	}
	if err == errReported {
		return "reported"
	}
	switch status.Code(err) {
	case codes.InvalidArgument:
		return "invalid"
	case codes.NotFound:
		return "not_found"
	case codes.AlreadyExists:
		return "already_exists"
	case codes.PermissionDenied:
		return "permission_denied"
	case codes.FailedPrecondition:
		return "precondition"
	case codes.Unavailable:
		return "unavailable"
	case codes.DeadlineExceeded:
		return "timeout"
	case codes.Canceled:
		return "cancelled"
	case codes.Internal:
		return "internal"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied") || strings.Contains(msg, "operation not permitted"):
		return "permission_denied"
	case strings.Contains(msg, "address already in use"):
		return "port_in_use"
	case strings.Contains(msg, "no such host"):
		return "dns"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return "timeout"
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate"):
		return "tls"
	}
	return "other"
}

// analyticsAllowedCLI is a local gate for spooling: analytics is on and the user has been
// told. The daemon checks both again.
func analyticsAllowedCLI() bool {
	if !analyticsEnabledCLI() {
		return false
	}
	dir, err := paths.ConfigDir()
	return err == nil && analytics.NoticeShown(dir)
}

// analyticsEnabledCLI reports whether the setting and environment allow analytics.
func analyticsEnabledCLI() bool {
	if v := os.Getenv("DO_NOT_TRACK"); v != "" && v != "0" {
		return false
	}
	// Read the user's setting first: an explicit opt-out always wins.
	setting := true
	if dir, err := paths.ConfigDir(); err == nil {
		if raw, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
			var s struct {
				Analytics *bool `json:"analytics"`
			}
			if json.Unmarshal(raw, &s) == nil && s.Analytics != nil {
				setting = *s.Analytics
			}
		}
	}
	if !setting {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MASIR_ANALYTICS"))) {
	case "off", "0", "false", "no":
		return false
	case "on", "1", "true", "yes":
		return true
	}
	return version != "dev"
}
