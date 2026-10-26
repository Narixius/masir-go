package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/rulecheck"
)

// checkWithNewVersion asks a newly downloaded masir whether it can build every enabled
// rule with the geo data on disk, before the update replaces anything. A new version that
// can't would start without those rules, or not at all, and the proxy would be down or
// open where the rules said otherwise, so the update is refused and what runs is left as
// it is. The new version answers with its own rules code and its own Xray, which is the
// only way to know what it will accept.
func (s *Server) checkWithNewVersion(ctx context.Context, bin string) error {
	s.mu.Lock()
	req := rulecheck.Request{AssetDir: s.xray.AssetDir()}
	for _, r := range s.state.Rules {
		if !r.Disabled {
			req.Rules = append(req.Rules, rulecheck.Rule{Tag: r.Tag, Match: r.Match})
		}
	}
	s.mu.Unlock()
	if len(req.Rules) == 0 {
		return nil // nothing to hold the update back
	}
	problems, err := askBinary(ctx, bin, req)
	if err != nil {
		return fmt.Errorf("the new version couldn't be checked against your rules, so it wasn't installed: %w", err)
	}
	if len(problems) > 0 {
		var lines []string
		for _, p := range problems {
			lines = append(lines, p.Tag+": "+p.Message)
		}
		return fmt.Errorf("not installed: the new version can't build %d of your rules (%s). "+
			"Fix or turn them off (`masir rules check`), then update again; what runs now is unchanged",
			len(problems), strings.Join(lines, "; "))
	}
	return nil
}

// askBinary runs `bin daemon check` with req on stdin and reads the rules it reports.
func askBinary(ctx context.Context, bin string, req rulecheck.Request) ([]rulecheck.Report, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "daemon", "check")
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = append(os.Environ(), "MASIR_ANALYTICS=off", "XRAY_LOCATION_ASSET="+req.AssetDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var out []rulecheck.Report
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("it answered with something unreadable: %w", err)
	}
	return out, nil
}
