package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/analytics"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// maybeAnalyticsNotice shows the anonymous analytics notice once per install on the
// first interactive command. It is skipped when analytics is off.
func maybeAnalyticsNotice() {
	if !ui.Interactive() || !analyticsAllowedCLI() {
		return
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return
	}
	marker := filepath.Join(dir, "analytics", "notice")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(marker), 0o700)
	_ = os.WriteFile(marker, []byte("shown\n"), 0o600)
	ui.Note("masir sends anonymous usage and error data to improve it.\n"+
		"Turn it off with `masir settings set analytics off`.",
		ui.Yellow("▲ Anonymous analytics"))
}

// captureCLI records one command for product analytics. It never starts a daemon; when
// no daemon can take the event it leaves a spool file for the next daemon start.
func captureCLI(cmd *cobra.Command, a args, err error, took time.Duration) {
	if cmd.Hidden || !analyticsAllowedCLI() {
		return
	}
	capture, sampled := shouldCaptureCLI(cmd, a, err)
	if !capture {
		return
	}
	props := cliProps(cmd, a, err, took)
	if sampled {
		props["sampled"] = true
		props["sample_rate"] = 0.1
	}
	captureEvent("cli_command", props)
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

func shouldCaptureCLI(cmd *cobra.Command, a args, err error) (capture, sampled bool) {
	path := commandPath(cmd)
	if path == "version" || path == "help" || strings.HasPrefix(path, "completion") {
		return false, false
	}
	if err != nil {
		return true, false // failures are always useful
	}
	if ui.Interactive() {
		return true, false
	}
	if mutatingCommand(path, a) {
		return true, false
	}
	// Sample read-only scripted commands so a shell loop doesn't flood PostHog.
	return rand.IntN(10) == 0, true
}

func commandPath(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), "masir ")
}

func mutatingCommand(path string, a args) bool {
	switch {
	case path == "connect", path == "disconnect", path == "mode", path == "menu":
		return true
	case path == "use":
		return true
	case strings.HasPrefix(path, "sub add"), strings.HasPrefix(path, "sub update"),
		strings.HasPrefix(path, "sub remove"):
		return true
	case strings.HasPrefix(path, "rules add"), strings.HasPrefix(path, "rules edit"),
		strings.HasPrefix(path, "rules remove"):
		return true
	case strings.HasPrefix(path, "settings set"), strings.HasPrefix(path, "settings reset"):
		return true
	case strings.HasPrefix(path, "daemon "):
		return true
	case path == "update":
		return !a.boolean("check", false)
	case path == "update status":
		return false
	case strings.HasPrefix(path, "update "):
		return true
	}
	return false
}

func cliProps(cmd *cobra.Command, a args, err error, took time.Duration) map[string]any {
	path := commandPath(cmd)
	props := map[string]any{
		"command":       path,
		"command_group": commandGroup(path),
		"cli_version":   version,
		"interactive":   ui.Interactive(),
		"scripted":      !ui.Interactive(),
		"duration_ms":   took.Milliseconds(),
		"success":       err == nil,
		"arg_count":     len(a.pos),
	}
	if flags := usedFlags(a); len(flags) > 0 {
		props["flags"] = flags
	}
	if key := settingKey(path, a); key != "" {
		props["setting_key"] = key
	}
	if target := commandTarget(path, a); target != "" {
		props["target"] = target
	}
	if kinds := ruleMatchers(a); len(kinds) > 0 {
		props["rule_matchers"] = kinds
	}
	if kind := ruleTargetKind(a.str("outbound")); kind != "" {
		props["rule_target_kind"] = kind
	} else if a.str("via") != "" {
		props["rule_target_kind"] = "config"
	}
	if err != nil {
		for k, v := range errorProps(err) {
			props[k] = v
		}
	}
	return props
}

func commandGroup(path string) string {
	if i := strings.IndexByte(path, ' '); i >= 0 {
		return path[:i]
	}
	return path
}

func usedFlags(a args) []string {
	out := make([]string, 0, len(a.flags))
	for k := range a.flags {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func settingKey(path string, a args) string {
	if !strings.HasPrefix(path, "settings ") || a.arg(0) == "" {
		return ""
	}
	s, err := findSetting(a.arg(0))
	if err != nil {
		return ""
	}
	return s.key
}

func commandTarget(path string, a args) string {
	switch {
	case path == "connect":
		if a.boolean("proxy", false) || !a.boolean("tun", true) {
			return "proxy"
		}
		return "tun"
	case path == "use":
		if a.boolean("fastest", false) {
			return "fastest"
		}
		if a.arg(0) != "" {
			return "id"
		}
		return "interactive"
	case path == "mode":
		if a.arg(0) != "" {
			return a.arg(0)
		}
		return "interactive"
	case path == "update":
		if a.boolean("check", false) {
			return "check"
		}
		if a.arg(0) != "" {
			return a.arg(0)
		}
		return "masir"
	case strings.HasPrefix(path, "update "):
		return strings.TrimPrefix(path, "update ")
	}
	return ""
}

func ruleMatchers(a args) []string {
	set := map[string]bool{}
	for k := range a.flags {
		switch k {
		case "domain":
			set["domain"] = true
		case "ip":
			set["ip"] = true
		case "port":
			set["port"] = true
		case "network":
			set["network"] = true
		case "protocol":
			set["protocol"] = true
		case "process", "app":
			set["process"] = true
		case "source":
			set["source"] = true
		case "source-port":
			set["sourcePort"] = true
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ruleTargetKind(raw string) string {
	switch {
	case raw == "direct", raw == "block", raw == "proxy":
		return raw
	case strings.HasPrefix(raw, "config:"):
		return "config"
	case strings.HasPrefix(raw, "outbound:"):
		return "outbound"
	}
	return ""
}

func errorProps(err error) map[string]any {
	props := map[string]any{"error_kind": errorKind(err)}
	if code := status.Code(err); code != codes.OK && code != codes.Unknown {
		props["grpc_code"] = code.String()
	}
	if reason := errorReason(err); reason != "" {
		props["error_reason"] = reason
	}
	props["error_fingerprint"] = fingerprint(scrub(message(err)))
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

func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

var (
	scrubURL    = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	scrubIP     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	scrubPath   = regexp.MustCompile(`(?:/[\w.@%+-]+){2,}/?`)
	scrubDomain = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)+[a-z]{2,}\b`)
)

func scrub(s string) string {
	s = scrubURL.ReplaceAllString(s, "<url>")
	s = scrubPath.ReplaceAllString(s, "<path>")
	s = scrubDomain.ReplaceAllString(s, "<domain>")
	s = scrubIP.ReplaceAllString(s, "<ip>")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// analyticsAllowedCLI is a local gate for spooling. The daemon checks the setting again.
func analyticsAllowedCLI() bool {
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
