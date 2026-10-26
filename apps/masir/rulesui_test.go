package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// rulesDaemon answers the few calls the rules prompt makes; any other call panics on the
// nil embedded client, which is how a test notices an unexpected one.
type rulesDaemon struct {
	pb.DaemonServiceClient
	powerErrs  []error // returned by successive SetPower calls; then success
	powerCalls int
	problems   []*pb.RuleProblem
	disabled   []*pb.SetRuleEnabledRequest
	checks     int
	statuses   int
}

func (f *rulesDaemon) SetPower(context.Context, *pb.SetPowerRequest, ...grpc.CallOption) (*pb.SetPowerResponse, error) {
	f.powerCalls++
	if len(f.powerErrs) > 0 {
		err := f.powerErrs[0]
		f.powerErrs = f.powerErrs[1:]
		return nil, err
	}
	return &pb.SetPowerResponse{}, nil
}

func (f *rulesDaemon) GetStatus(context.Context, *pb.GetStatusRequest, ...grpc.CallOption) (*pb.GetStatusResponse, error) {
	f.statuses++
	return &pb.GetStatusResponse{Status: &pb.Status{RuleProblems: f.problems}}, nil
}

func (f *rulesDaemon) CheckRules(context.Context, *pb.CheckRulesRequest, ...grpc.CallOption) (*pb.CheckRulesResponse, error) {
	f.checks++
	return &pb.CheckRulesResponse{Problems: f.problems}, nil
}

func (f *rulesDaemon) SetRuleEnabled(_ context.Context, in *pb.SetRuleEnabledRequest, _ ...grpc.CallOption) (*pb.SetRuleEnabledResponse, error) {
	f.disabled = append(f.disabled, in)
	return &pb.SetRuleEnabledResponse{}, nil
}

func useFake(t *testing.T, f *rulesDaemon) {
	t.Helper()
	old := cachedClient
	cachedClient = f
	t.Cleanup(func() { cachedClient = old; inRulesPrompt = false })
}

func brokenErr(t *testing.T) error {
	t.Helper()
	st, err := status.New(codes.FailedPrecondition, "1 rule(s) can't be built with the installed geo data: iran-direct").
		WithDetails(&errdetails.ErrorInfo{Domain: "masir", Reason: "RULES_BROKEN"})
	if err != nil {
		t.Fatal(err)
	}
	return st.Err()
}

var iranProblem = &pb.RuleProblem{RuleTag: "iran-direct", Missing: []string{"geoip:in-ir"}, Message: "geoip:in-ir isn't in geoip.dat"}

func setPower(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetPowerResponse, error) {
	return c.SetPower(ctx, &pb.SetPowerRequest{On: true})
}

func TestRefusalWithoutATerminalSaysWhatToDoAndChangesNothing(t *testing.T) {
	f := &rulesDaemon{powerErrs: []error{brokenErr(t)}, problems: []*pb.RuleProblem{iranProblem}}
	useFake(t, f)
	_, err := call(setPower)
	ue, ok := asUserError(err)
	if !ok || !strings.Contains(ue.details, "iran-direct") || ue.advice.cmds[0] != "masir rules disable iran-direct" || ue.note == "" {
		t.Fatalf("err = %#v", err)
	}
	if f.powerCalls != 1 || len(f.disabled) != 0 {
		t.Errorf("power calls = %d, rules turned off = %v: with no terminal nothing is decided for the user", f.powerCalls, f.disabled)
	}
}

func TestRefusalIsRetriedOnceTheRulesCanRunAgain(t *testing.T) {
	f := &rulesDaemon{powerErrs: []error{brokenErr(t)}} // the daemon's own check now finds nothing
	useFake(t, f)
	if _, err := call(setPower); err != nil {
		t.Fatal(err)
	}
	if f.powerCalls != 2 || f.checks != 1 {
		t.Errorf("power calls = %d, checks = %d", f.powerCalls, f.checks)
	}
}

func TestOtherErrorsAreNotTouched(t *testing.T) {
	other := status.Error(codes.Internal, "boom")
	f := &rulesDaemon{powerErrs: []error{other}, problems: []*pb.RuleProblem{iranProblem}}
	useFake(t, f)
	if _, err := call(setPower); err != other {
		t.Fatalf("err = %v", err)
	}
	if f.checks != 0 || f.powerCalls != 1 {
		t.Errorf("checks = %d, power calls = %d", f.checks, f.powerCalls)
	}
}

func TestTurnOffNamesEveryRuleAndSaysWhy(t *testing.T) {
	f := &rulesDaemon{}
	useFake(t, f)
	err := turnOff([]*pb.RuleProblem{iranProblem, {RuleTag: "ai", Missing: []string{"geosite:category-ai-cn"}}})
	if err != nil || len(f.disabled) != 1 {
		t.Fatalf("err = %v, calls = %v", err, f.disabled)
	}
	req := f.disabled[0]
	if req.Enabled || strings.Join(req.RuleTags, ",") != "iran-direct,ai" ||
		!strings.Contains(req.Reason, "geoip:in-ir") || !strings.Contains(req.Reason, "geosite:category-ai-cn") {
		t.Fatalf("request = %+v", req)
	}
}

func TestProblemText(t *testing.T) {
	got := problemText([]*pb.RuleProblem{
		{RuleTag: "iran-direct", Message: "geoip:blacklist-ir isn't in geoip.dat; geoip:in-ir isn't in geoip.dat"},
		{RuleTag: "ssh", Message: "x", Suspended: true},
	})
	want := "iran-direct\n" +
		"  geoip:blacklist-ir isn't in geoip.dat\n" +
		"  geoip:in-ir isn't in geoip.dat\n" +
		"ssh\n  x\n  masir is running without this rule for now"
	if got != want {
		t.Fatalf("problemText =\n%s\nwant\n%s", got, want)
	}
}

func TestBrokenRulesAdviceIsRunnableAsItStands(t *testing.T) {
	adv := brokenRulesAdvice([]*pb.RuleProblem{{RuleTag: "iran-direct"}, {RuleTag: "ai"}})
	if len(adv.cmds) != 2 || adv.cmds[0] != "masir rules disable iran-direct ai" || adv.cmds[1] != "masir rules check" || strings.Contains(adv.text, "`") {
		t.Fatalf("advice = %+v", adv)
	}
}

func TestBlockedGeoUpdateWithoutATerminalFailsAndNamesTheFlag(t *testing.T) {
	job := &pb.UpdateJob{State: pb.UpdateState_UPDATE_STATE_BLOCKED, BlockedRules: []*pb.RuleProblem{iranProblem}}
	if err := blockedGeoUpdate(job); err != errReported {
		t.Fatalf("err = %v, want errReported (a non-zero exit)", err)
	}
}

func TestPhrasesDoNotDoublePlurals(t *testing.T) {
	for n, want := range map[int]string{1: "1 of your rules", 2: "2 of your rules"} {
		if got := ofYourRules(n); got != want {
			t.Errorf("ofYourRules(%d) = %q, want %q", n, got, want)
		}
	}
	if got := counted(2, "rule"); got != "2 rules" {
		t.Errorf("counted = %q", got)
	}
}

func TestAnUpdateReportsRulesTheNewDaemonIsRunningWithout(t *testing.T) {
	f := &rulesDaemon{problems: []*pb.RuleProblem{{RuleTag: "iran-direct", Message: "geoip:in-ir isn't in geoip.dat", Suspended: true}}}
	useFake(t, f)
	warnIfRulesSuspended()
	if f.statuses != 1 {
		t.Fatalf("status calls = %d: the daemon decides which rules are suspended, the CLI asks it", f.statuses)
	}
	f2 := &rulesDaemon{}
	useFake(t, f2)
	warnIfRulesSuspended() // nothing suspended: quiet
	if f2.statuses != 1 {
		t.Fatalf("status calls = %d", f2.statuses)
	}
}
