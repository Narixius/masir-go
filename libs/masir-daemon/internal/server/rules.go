package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/rulecheck"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// geoData is where the daemon's Xray reads geoip.dat and geosite.dat.
func (s *Server) geoData() rulecheck.Data {
	dir := s.xray.AssetDir()
	return rulecheck.Data{GeoIP: filepath.Join(dir, "geoip.dat"), GeoSite: filepath.Join(dir, "geosite.dat")}
}

// gate is the step before Xray is started, restarted or changed: it checks that every
// enabled rule can be built with the geo data on disk. Xray builds a config whole or not
// at all, so one rule using a category the data lacks would otherwise stop the proxy,
// and traffic would leave unprotected. Expects s.mu held.
//
// With a caller waiting (attended), a broken set is refused, with the problems for the
// caller to put to the user: whoever turns masir on decides what happens to the rules.
// With nobody to ask, what runs is left alone, and a daemon with nothing running (just
// started) suspends the broken rules only when that can't send their traffic anywhere
// less protected than the rule did. A suspended rule is not disabled: it comes back by
// itself once the data can build it.
func (s *Server) gate(st store.State, hasProxy, runs, attended bool) (skip map[string]bool, err error) {
	problems := rulecheck.Check(st.Rules, s.geoData())
	s.ruleProblems, s.suspended = problems, nil
	if len(problems) == 0 || !runs {
		return nil, nil // nothing to hold back, or nothing is starting
	}
	if attended {
		return nil, rulesBroken(problems, "")
	}
	if s.xray.Running() {
		return nil, rulesBroken(problems, "what runs was left as it is")
	}
	proxyFallback := st.Settings.Fallback == store.FallbackProxy && hasProxy
	skip = map[string]bool{}
	for _, p := range problems {
		for _, r := range st.Rules {
			if r.Tag == p.Tag && !safeToSuspend(r, proxyFallback) {
				return nil, rulesBroken(problems, fmt.Sprintf("not started: dropping %q could send its traffic out unprotected", r.Tag))
			}
		}
		skip[p.Tag] = true
	}
	s.suspended = skip
	s.Log("warn", fmt.Sprintf("started without %d rule(s) the geo data can't build: %s", len(skip), tags(problems)))
	return skip, nil
}

// safeToSuspend reports whether leaving a rule out puts its traffic on a path at least as
// protected as the rule gave it. A rule to the proxy, a pinned server or a custom outbound
// falls through to the default route, which is only as protected when that is the proxy; a
// block rule's traffic would simply be let through.
func safeToSuspend(r store.Rule, proxyFallback bool) bool {
	switch r.Target.Kind {
	case store.TargetDirect:
		return true
	case store.TargetBlock:
		return false
	default:
		return proxyFallback
	}
}

func tags(problems []rulecheck.Problem) string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Tag)
	}
	return strings.Join(out, ", ")
}

// rulesBroken is the refusal: FailedPrecondition with reason RULES_BROKEN, which clients
// answer by asking CheckRules for the details.
func rulesBroken(problems []rulecheck.Problem, note string) error {
	msg := fmt.Sprintf("%d rule(s) can't be built with the installed geo data: %s", len(problems), tags(problems))
	if note != "" {
		msg += " (" + note + ")"
	}
	st := status.New(codes.FailedPrecondition, msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Domain: "masir", Reason: "RULES_BROKEN"}); err == nil {
		return d.Err()
	}
	return st.Err()
}

func toPBProblems(problems []rulecheck.Problem, suspended map[string]bool) []*pb.RuleProblem {
	out := make([]*pb.RuleProblem, len(problems))
	for i, p := range problems {
		out[i] = &pb.RuleProblem{RuleTag: p.Tag, Missing: p.Missing, Message: p.Message, Suspended: suspended[p.Tag]}
	}
	return out
}

func (s *Server) CheckRules(ctx context.Context, _ *pb.CheckRulesRequest) (*pb.CheckRulesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.CheckRulesResponse{Problems: toPBProblems(rulecheck.Check(s.state.Rules, s.geoData()), s.suspended)}, nil
}

func (s *Server) SetRuleEnabled(ctx context.Context, req *pb.SetRuleEnabledRequest) (*pb.SetRuleEnabledResponse, error) {
	if len(req.RuleTags) == 0 {
		return nil, status.Error(codes.InvalidArgument, "name at least one rule")
	}
	if req.Enabled {
		// Say which categories are missing, rather than Xray's own message.
		s.mu.Lock()
		var picked []store.Rule
		for _, r := range s.state.Rules {
			for _, tag := range req.RuleTags {
				if r.Tag == tag {
					r.Disabled = false
					picked = append(picked, r)
				}
			}
		}
		problems := rulecheck.Check(picked, s.geoData())
		s.mu.Unlock()
		if len(problems) > 0 {
			var lines []string
			for _, p := range problems {
				lines = append(lines, fmt.Sprintf("%s: %s", p.Tag, p.Message))
			}
			return nil, status.Error(codes.FailedPrecondition, "can't be enabled: "+strings.Join(lines, "; "))
		}
	}
	verb := "disabled"
	if req.Enabled {
		verb = "enabled"
	}
	err := s.change(ctx, "rules "+verb+": "+strings.Join(req.RuleTags, ", "), func(st *store.State) error {
		for _, tag := range req.RuleTags {
			if err := st.SetRuleEnabled(tag, req.Enabled, req.Reason); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SetRuleEnabledResponse{}, nil
}
