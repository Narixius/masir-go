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
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/update"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// checkWithNewVersion asks a newly downloaded masir whether it can build every enabled
// rule with the geo data on disk, before the update replaces anything. A new version that
// can't would start without those rules, or not at all, and the proxy would be down or
// open where the rules said otherwise, so the update is refused and what runs is left as
// it is. The new version answers with its own rules code and its own Xray, which is the
// only way to know what it will accept.
func (s *Server) checkWithNewVersion(ctx context.Context, bin string) error {
	req := rulecheck.Request{AssetDir: s.xray.AssetDir()}
	for _, r := range s.rulesNextStartWillLoad() {
		if !r.Disabled {
			req.Rules = append(req.Rules, rulecheck.Rule{Tag: r.Tag, Match: r.Match})
		}
	}
	if len(req.Rules) == 0 {
		return nil // nothing to hold the update back
	}
	problems, err := askBinary(ctx, bin, req)
	if err != nil {
		return fmt.Errorf("the new version couldn't be checked against your rules, so nothing was installed: %w", err)
	}
	if len(problems) > 0 {
		out := &update.RulesError{}
		for _, p := range problems {
			out.Problems = append(out.Problems, &pb.RuleProblem{RuleTag: p.Tag, Missing: p.Missing, Message: p.Message})
		}
		return out
	}
	return nil
}

// rulesNextStartWillLoad is what the new version will read when it starts: the saved
// rules, which are the daemon's own unless someone edited the file by hand while it ran
// (the daemon doesn't re-read it). Checking the saved file means a hand edit can't slip
// past the check and surface only after the restart. The daemon's own copy is the
// fallback if the file can't be read.
func (s *Server) rulesNextStartWillLoad() []store.Rule {
	s.mu.Lock()
	dir, mine := s.dir, s.state.Rules
	s.mu.Unlock()
	if dir == "" {
		return mine
	}
	saved, err := store.Load(dir)
	if err != nil {
		return mine
	}
	return saved.Rules
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
