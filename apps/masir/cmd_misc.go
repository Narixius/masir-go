package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// cmdLogs shows the daemon's recent log, then follows it until Ctrl-C. It never
// starts a daemon: an idle daemon has nothing to say, and starting one to watch
// silence is confusing.
func cmdLogs(a args) error {
	c, ok := runningDaemon()
	if !ok {
		ui.Note(fmt.Sprintf("The masir daemon isn't running, so there are no log entries yet.\n"+
			"Start it with `masir daemon start` (or `masir connect`).\n"+
			"For its startup output, see %s.", daemonLogPath()),
			ui.Yellow("▲ Daemon not running"))
		return nil
	}
	level, err := logLevelFilter(a.str("level"))
	if err != nil {
		return err
	}
	lines, err := logLines(a.str("lines"))
	if err != nil {
		return err
	}
	follow := a.boolean("follow", true)
	ctx := appCtx // Ctrl-C is how you stop following, so it ends quietly
	stream, err := c.StreamLogs(ctx, &pb.StreamLogsRequest{Follow: follow, Lines: uint32(lines), MinLevel: level})
	if err != nil {
		return err
	}
	if follow && ui.Interactive() {
		fmt.Fprintln(os.Stderr, ui.Dim("Following the masir daemon's log. Ctrl-C to stop."))
	}
	jsonOut := a.boolean("json", false)
	shown := 0
	for {
		res, err := stream.Recv()
		if err == io.EOF || ctx.Err() != nil {
			break
		}
		if err != nil {
			return err
		}
		e := res.Entry
		shown++
		if jsonOut {
			line, _ := json.Marshal(map[string]any{
				"time":    time.UnixMilli(e.TimestampMs).Format(time.RFC3339),
				"level":   e.Level,
				"message": e.Message,
			})
			fmt.Println(string(line))
			continue
		}
		fmt.Printf("%s %s %s\n", ui.Dim(time.UnixMilli(e.TimestampMs).Format("15:04:05")), levelText(e.Level), ui.Visual(e.Message))
	}
	if !follow && shown == 0 && !jsonOut {
		ui.Info("No log entries yet.")
	}
	return nil
}

// daemonLogPath is where the daemon wrote its startup output: the service's own log
// when it's installed, otherwise the background daemon's file in the config folder.
func daemonLogPath() string {
	if service.Installed() {
		switch runtime.GOOS {
		case "darwin":
			return "/var/log/masird.log"
		case "linux":
			return "the system journal (`journalctl -u masir`)"
		case "windows":
			if dir := os.Getenv("ProgramData"); dir != "" {
				return filepath.Join(dir, "masir", "masir.log")
			}
		}
	}
	dir, _ := paths.ConfigDir()
	return filepath.Join(dir, "masird.log")
}

func logLevelFilter(level string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "":
		return "", nil
	case "debug", "info", "warn", "warning", "error":
		return strings.ToLower(strings.TrimSpace(level)), nil
	}
	return "", fail("--level is debug, info, warn, or error")
}

func logLines(lines string) (int, error) {
	if strings.TrimSpace(lines) == "" {
		return 200, nil
	}
	n, err := strconv.Atoi(lines)
	if err != nil || n < 0 {
		return 0, fail("--lines is a whole number of entries")
	}
	return n, nil
}

func levelText(level string) string {
	l := fmt.Sprintf("[%s]", strings.ToLower(level))
	switch strings.ToLower(level) {
	case "error":
		return ui.Red(l)
	case "warn", "warning":
		return ui.Yellow(l)
	case "debug":
		return ui.Gray(l)
	}
	return ui.Blue(l)
}

func cmdGetConfig(args) error {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.GetXrayConfigResponse, error) {
		return c.GetXrayConfig(ctx, &pb.GetXrayConfigRequest{})
	})
	if err != nil {
		return err
	}
	if res.Json == "" {
		return fail("xray isn't running, so there's no config yet; `masir connect` starts it")
	}
	var out bytes.Buffer
	if json.Indent(&out, []byte(res.Json), "", "  ") != nil {
		fmt.Println(res.Json)
		return nil
	}
	fmt.Println(out.String())
	return nil
}
