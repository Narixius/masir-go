package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

type ruleDraft struct {
	name   string
	match  ruleMatch
	target string
	index  int
}

func cmdRulesEdit(a args) error {
	rules, err := listRules()
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return fail("no rules yet; add one with `masir rules add`")
	}
	name := a.arg(0)
	if name == "" {
		if !ui.Interactive() {
			return fail("name the rule: masir rules edit <name>")
		}
		opts := make([]ui.Option[string], len(rules))
		for i, r := range rules {
			opts[i] = ui.Option[string]{Label: r.RuleTag, Hint: "→ " + stripANSI(targetText(r.Target, r.ResolvedOutboundTag, r.FellBack, nil)), Value: r.RuleTag}
		}
		if name, err = ui.Select("Which rule?", opts, ""); err != nil {
			return err
		}
	}
	idx := slices.IndexFunc(rules, func(r *pb.RoutingRule) bool { return r.RuleTag == name })
	if idx < 0 {
		return fail("no rule named %q", name)
	}
	r := rules[idx]
	m, err := parseMatch(r.RuleJson)
	if err != nil {
		return err
	}
	orig := ruleDraft{name: r.RuleTag, match: m, target: r.Target, index: idx}
	all := loadNodesQuiet()

	if flagEdits(a) {
		draft, err := draftFromFlags(a, orig, all)
		if err != nil {
			return err
		}
		return saveRule(orig, draft, rules, all)
	}
	if !ui.Interactive() {
		return fail("pass changes as flags (see `masir rules edit --help`), or run in a terminal for the menu")
	}
	return editMenu(orig, rules, all)
}

func flagEdits(a args) bool {
	for _, f := range []string{"outbound", "via", "rename", "json", "unset"} {
		if a.has(f) {
			return true
		}
	}
	for f := range flagKeys {
		if a.has(f) {
			return true
		}
	}
	return false
}

func draftFromFlags(a args, orig ruleDraft, all []subNodes) (ruleDraft, error) {
	d := orig
	d.match = orig.match.clone()
	if a.has("json") {
		m, err := parseMatch(a.str("json"))
		if err != nil {
			return d, err
		}
		d.match = m
	}
	for _, k := range a.list("unset") {
		delete(d.match, k)
	}
	if _, err := matchFromFlags(a, d.match, a.boolean("append", false)); err != nil {
		return d, err
	}
	if t, ok, err := targetFromFlags(a, all); err != nil {
		return d, err
	} else if ok {
		d.target = t
	}
	if a.has("rename") {
		d.name = a.str("rename")
	}
	return d, nil
}

func editMenu(orig ruleDraft, rules []*pb.RoutingRule, all []subNodes) error {
	d := orig
	d.match = orig.match.clone()
	for {
		dirty := !sameDraft(orig, d)
		title := "Editing"
		if dirty {
			title = "Editing (unsaved changes)"
		}
		ui.Note(ui.Visual(draftSummary(d, len(rules), all)), title)

		opts := []ui.Option[string]{}
		if dirty {
			opts = append(opts, ui.Option[string]{Label: "Save changes", Value: "save"})
		}
		opts = append(opts,
			ui.Option[string]{Label: "What it matches", Value: "match"},
			ui.Option[string]{Label: "Where traffic goes", Value: "target"},
			ui.Option[string]{Label: "Its position", Hint: fmt.Sprintf("#%d of %d", d.index+1, len(rules)), Value: "position"},
			ui.Option[string]{Label: "Its name", Value: "name"},
			ui.Option[string]{Label: "Edit as JSON", Hint: "opens $EDITOR", Value: "json"},
			ui.Option[string]{Label: "Cancel", Value: "cancel"},
		)
		choice, err := ui.Select("What do you want to change?", opts, "")
		if err != nil {
			return err
		}
		switch choice {
		case "save":
			return saveRule(orig, d, rules, all)
		case "cancel":
			if dirty {
				discard, err := ui.Confirm("Discard your changes?", false)
				if err != nil || !discard {
					continue
				}
			}
			ui.Info("Nothing changed.")
			return nil
		case "match":
			m, err := pickMatch(d.match)
			if ui.IsCancel(err) {
				continue
			}
			if err != nil {
				return err
			}
			d.match = m
		case "target":
			t, err := pickTarget(all, d.target)
			if ui.IsCancel(err) {
				continue
			}
			if err != nil {
				return err
			}
			d.target = t
		case "position":
			i, err := pickPosition(d, rules)
			if ui.IsCancel(err) {
				continue
			}
			if err != nil {
				return err
			}
			d.index = i
		case "name":
			n, err := ui.Text("Rule name", d.name, validateRuleName(rules, orig.name))
			if ui.IsCancel(err) {
				continue
			}
			if err != nil {
				return err
			}
			d.name = n
		case "json":
			m, err := editJSON(d.match)
			if err != nil {
				ui.Error(ui.Red(err.Error()))
				continue
			}
			d.match = m
		}
	}
}

// pickPosition returns the rule's index after moving: first, last, or before another rule.
func pickPosition(d ruleDraft, rules []*pb.RoutingRule) (int, error) {
	var others []*pb.RoutingRule
	for _, r := range rules {
		if r.RuleTag != d.name && r.RuleTag != rules[d.index].RuleTag {
			others = append(others, r)
		}
	}
	opts := []ui.Option[int]{{Label: "First", Hint: "checked before every other rule", Value: 0}, {Label: "Last", Value: len(others)}}
	for i, r := range others {
		opts = append(opts, ui.Option[int]{Label: "Before " + r.RuleTag, Value: i})
	}
	return ui.Select("Where should it go? (top to bottom, the first match wins)", opts, -1)
}

func saveRule(orig, d ruleDraft, rules []*pb.RoutingRule, all []subNodes) error {
	if sameDraft(orig, d) {
		ui.Info("Nothing changed.")
		return nil
	}
	if d.name != orig.name {
		if err := validateRuleName(rules, orig.name)(d.name); err != nil {
			return err
		}
	}
	req := &pb.AlterRoutingRequest{RuleTag: orig.name}
	if d.match.json() != orig.match.json() {
		j := d.match.json()
		req.RuleJson = &j
	}
	if d.target != orig.target {
		req.Target = &d.target
	}
	if d.name != orig.name {
		req.NewRuleTag = &d.name
	}
	sp := ui.Spin("Checking the rule with xray")
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.AlterRoutingResponse, error) {
		return c.AlterRouting(ctx, req)
	}); err != nil {
		sp.Fail(message(err))
		return errReported
	}
	if d.index != orig.index {
		if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.MoveRoutingResponse, error) {
			return c.MoveRouting(ctx, &pb.MoveRoutingRequest{RuleTag: d.name, Index: uint32(d.index)})
		}); err != nil {
			sp.Fail(message(err))
			return errReported
		}
	}
	sp.Stop(ui.Visual(fmt.Sprintf("Rule %q updated.", d.name)))

	var diff []string
	line := func(label, before, after string) {
		if before != after {
			diff = append(diff, ui.Dim(label), ui.Red("- "+before), ui.Green("+ "+after))
		}
	}
	line("name", orig.name, d.name)
	line("goes to", stripANSI(targetText(orig.target, "", false, all)), stripANSI(targetText(d.target, "", false, all)))
	line("position", fmt.Sprintf("#%d", orig.index+1), fmt.Sprintf("#%d", d.index+1))
	line("matches", orig.match.json(), d.match.json())
	ui.Note(ui.Visual(strings.Join(diff, "\n")), "Rule updated")
	return nil
}

func sameDraft(a, b ruleDraft) bool {
	return a.name == b.name && a.target == b.target && a.index == b.index && a.match.json() == b.match.json()
}

func draftSummary(d ruleDraft, total int, all []subNodes) string {
	rows := [][2]string{
		{"Name", d.name},
		{"Goes to", targetText(d.target, "", false, all)},
		{"Position", fmt.Sprintf("#%d of %d", d.index+1, total)},
	}
	rows = append(rows, d.match.describe()...)
	var lines []string
	w := 0
	for _, r := range rows {
		w = max(w, ui.Width(r[0]))
	}
	for _, r := range rows {
		lines = append(lines, ui.Dim(ui.Pad(r[0], w))+"  "+r[1])
	}
	return strings.Join(lines, "\n")
}

// editJSON opens the match in $VISUAL/$EDITOR and reads it back.
func editJSON(m ruleMatch) (ruleMatch, error) {
	f, err := os.CreateTemp("", "masir-rule-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	b, _ := json.MarshalIndent(m, "", "  ")
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], f.Name())...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fail("editor: %v", err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		return nil, err
	}
	out, err := parseMatch(string(raw))
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"outboundTag", "balancerTag", "ruleTag"} {
		if _, ok := out[k]; ok {
			return nil, fail("%s can't be set in the JSON; use \"Where traffic goes\" or \"Its name\"", k)
		}
	}
	if len(out) == 0 {
		return nil, fail("a rule needs at least one thing to match")
	}
	return out, nil
}
