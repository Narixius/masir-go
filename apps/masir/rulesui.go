package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// The daemon decides whether the rules can run: before it starts or restarts Xray it
// checks every enabled rule against the installed geo data, and refuses (RULES_BROKEN)
// when one can't be built. Everything here only shows what it found, asks the user, and
// relays the answer.

// inRulesPrompt stops the calls the prompt itself makes from prompting again.
var inRulesPrompt bool

// problemText lists the rules that can't run, each with what is missing under it:
//
//	iran-direct
//	  geoip:in-ir isn't in geoip.dat
func problemText(problems []*pb.RuleProblem) string {
	var lines []string
	for _, p := range problems {
		lines = append(lines, p.RuleTag)
		for _, note := range strings.Split(p.Message, "; ") {
			lines = append(lines, "  "+note)
		}
		if p.Suspended {
			lines = append(lines, "  masir is running without this rule for now")
		}
	}
	return strings.Join(lines, "\n")
}

func problemTags(problems []*pb.RuleProblem) []string {
	tags := make([]string, len(problems))
	for i, p := range problems {
		tags[i] = p.RuleTag
	}
	return tags
}

// offerToTurnOff shows the rules that can't run and asks whether to turn them off. It
// reports whether they were turned off. Nothing is asked without a terminal.
func offerToTurnOff(problems []*pb.RuleProblem, question string) (bool, error) {
	if !ui.Interactive() {
		return false, nil
	}
	ui.Note(problemText(problems), ui.Yellow("▲ Rules that can't run"))
	ok, err := ui.Confirm(question, false)
	if err != nil || !ok {
		return false, err
	}
	return true, turnOff(problems)
}

func turnOff(problems []*pb.RuleProblem) error {
	var why []string
	for _, p := range problems {
		why = append(why, p.Missing...)
	}
	reason := "turned off because the geo data can't build it"
	if len(why) > 0 {
		reason = "turned off because the geo data lacks " + strings.Join(why, ", ")
	}
	_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetRuleEnabledResponse, error) {
		return c.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: problemTags(problems), Enabled: false, Reason: reason})
	})
	if err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Turned off %s. They stay saved; `masir rules enable <name>` brings one back once it can run.", counted(len(problems), "rule")))
	return nil
}

func counted(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ofYourRules reads "1 of your rules", "2 of your rules": the phrase doesn't take a plural.
func ofYourRules(n int) string { return fmt.Sprintf("%d of your rules", n) }

// brokenRulesAdvice is what to do about rules that can't run, with their names filled in
// so the command can be run as it is.
func brokenRulesAdvice(problems []*pb.RuleProblem) advice {
	return advice{
		text: "To go on, turn them off, or look at them first:",
		cmds: []string{"masir rules disable " + strings.Join(problemTags(problems), " "), "masir rules check"},
	}
}

func brokenRulesHeadline(n int) string {
	return fmt.Sprintf("%s can't run with the installed geo data.", capitalize(counted(n, "rule")))
}

// brokenRulesFlow answers the daemon's refusal: it asks the daemon which rules can't run,
// and offers to turn them off. It reports whether the refused call should be tried again.
func brokenRulesFlow() (retry bool, problems []*pb.RuleProblem, err error) {
	inRulesPrompt = true
	defer func() { inRulesPrompt = false }()
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.CheckRulesResponse, error) {
		return c.CheckRules(ctx, &pb.CheckRulesRequest{})
	})
	if err != nil {
		return false, nil, err
	}
	if len(res.Problems) == 0 {
		return true, nil, nil // sorted out since
	}
	retry, err = offerToTurnOff(res.Problems, "Turn these rules off and continue?")
	return retry, res.Problems, err
}

func cmdRulesCheck(a args) error {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.CheckRulesResponse, error) {
		return c.CheckRules(ctx, &pb.CheckRulesRequest{})
	})
	if err != nil {
		return err
	}
	rules, err := listRules()
	if err != nil {
		return err
	}
	enabled := 0
	for _, r := range rules {
		if !r.Disabled {
			enabled++
		}
	}
	if len(res.Problems) == 0 {
		ui.Success(fmt.Sprintf("All %s can run with the installed geo data.", counted(enabled, "enabled rule")))
		return nil
	}
	inRulesPrompt = true
	defer func() { inRulesPrompt = false }()
	if off, err := offerToTurnOff(res.Problems, "Turn these rules off?"); err != nil || off {
		return err
	}
	printFailure(fmt.Sprintf("%s of %d can't run with the installed geo data.", capitalize(counted(len(res.Problems), "rule")), enabled),
		problemText(res.Problems), brokenRulesAdvice(res.Problems), "")
	return errReported
}

func cmdRulesDisable(a args) error { return setRulesEnabled(a, false) }
func cmdRulesEnable(a args) error  { return setRulesEnabled(a, true) }

func setRulesEnabled(a args, enable bool) error {
	tags := a.pos
	if len(tags) == 0 {
		rules, err := listRules()
		if err != nil {
			return err
		}
		var opts []ui.Option[string]
		for _, r := range rules {
			if r.Disabled == enable { // the ones this would change
				hint := ""
				if r.DisabledReason != "" {
					hint = r.DisabledReason
				}
				opts = append(opts, ui.Option[string]{Label: r.RuleTag, Hint: hint, Value: r.RuleTag})
			}
		}
		if len(opts) == 0 {
			if enable {
				ui.Info("No rule is turned off.")
			} else {
				ui.Info("No rules to turn off.")
			}
			return nil
		}
		msg := "Which rules should be turned off?"
		if enable {
			msg = "Which rules should be turned on?"
		}
		picked, err := ui.MultiSelect(msg, opts, nil, true)
		if err != nil {
			return err
		}
		tags = picked
	}
	reason := a.str("reason")
	if reason == "" && !enable {
		reason = "turned off by you"
	}
	_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetRuleEnabledResponse, error) {
		return c.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: tags, Enabled: enable, Reason: reason})
	})
	if err != nil {
		return err
	}
	verb := "off"
	if enable {
		verb = "on"
	}
	ui.Success(fmt.Sprintf("Turned %s %s.", verb, counted(len(tags), "rule")))
	return nil
}

func rulesCheckCommands() []*cobra.Command {
	check := leaf("check", "Check that every enabled rule can run with the installed geo data", cmdRulesCheck, cobra.NoArgs)
	disable := leaf("disable [names...]", "Turn rules off without removing them", cmdRulesDisable, cobra.ArbitraryArgs,
		flagSpec{names: []string{"reason"}, help: "kept with the rule, so you remember why"})
	enable := leaf("enable [names...]", "Turn rules back on", cmdRulesEnable, cobra.ArbitraryArgs)
	disable.ValidArgsFunction = completeRules
	enable.ValidArgsFunction = completeRules
	return []*cobra.Command{check, disable, enable}
}
