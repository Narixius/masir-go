package main

import (
	"fmt"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

type menuItem struct {
	label, hint string
	run         func() error
	sub         []menuItem
}

// menu is the interactive home for a bare `masir`. Errors are shown and the menu
// comes back; Esc in a step returns here, Esc here quits.
func menu() error {
	ui.Intro(ui.Bold(" masir "))
	last := ""
	for {
		st, err := currentStatus()
		if err != nil {
			return err
		}
		items := mainMenu(st)
		ui.Message(statusLine(st))
		choice, err := chooseMenu("What do you want to do?", items, last)
		if ui.IsCancel(err) || (err == nil && choice == nil) {
			ui.Outro("Bye")
			return nil
		}
		if err != nil {
			return err
		}
		last = choice.label
		item := choice
		if len(choice.sub) > 0 {
			sub, err := chooseMenu(choice.label, append(choice.sub, menuItem{label: "Back"}), "")
			if ui.IsCancel(err) || sub == nil || sub.run == nil {
				continue
			}
			item = sub
		}
		if err := item.run(); err != nil && !ui.IsCancel(err) && err != errReported {
			ui.Error(ui.Red(message(err)))
		}
	}
}

func chooseMenu(msg string, items []menuItem, initial string) (*menuItem, error) {
	opts := make([]ui.Option[string], len(items))
	for i, it := range items {
		opts[i] = ui.Option[string]{Label: it.label, Hint: it.hint, Value: it.label}
	}
	v, err := ui.Select(msg, opts, initial)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].label == v {
			if items[i].run == nil && len(items[i].sub) == 0 {
				return nil, nil // Quit / Back
			}
			return &items[i], nil
		}
	}
	return nil, nil
}

func mainMenu(st *pb.Status) []menuItem {
	none := args{flags: map[string][]string{}}
	with := func(fn func(args) error) func() error { return func() error { return fn(none) } }

	power := menuItem{label: "Connect", hint: "turn masir on (TUN mode)", run: with(cmdConnect)}
	if st.Settings.GetPower() {
		power = menuItem{label: "Disconnect", hint: "turn masir off", run: with(cmdDisconnect)}
	}
	active := "none selected"
	if st.ActiveConfigName != "" {
		active = st.ActiveConfigName // a menu option hint: go-clack orders it
	}
	mode := "direct"
	if st.Settings.GetFallback() == pb.Fallback_FALLBACK_PROXY {
		mode = "proxy"
	}
	return []menuItem{
		power,
		{label: "Switch proxy", hint: active, run: with(cmdUse)},
		{label: "Routing mode", hint: "unmatched traffic goes " + mode, run: with(cmdMode)},
		{label: "Subscriptions", hint: "add, list, update, remove", sub: []menuItem{
			{label: "Add a subscription", run: with(cmdSubAdd)},
			{label: "List subscriptions", run: with(cmdSubList)},
			{label: "Update subscriptions", hint: "re-fetch all", run: with(cmdSubUpdate)},
			{label: "Use the fastest proxy", hint: "test every proxy", run: func() error {
				return cmdUse(args{flags: map[string][]string{"fastest": {"true"}}})
			}},
			{label: "Remove subscriptions", run: with(cmdSubRemove)},
		}},
		{label: "Rules", hint: "send apps, sites and IPs somewhere specific", sub: []menuItem{
			{label: "Add a rule", run: with(cmdRulesAdd)},
			{label: "List rules", run: with(cmdRulesList)},
			{label: "Edit a rule", run: with(cmdRulesEdit)},
			{label: "Remove rules", run: with(cmdRulesRemove)},
			{label: "Test a destination", hint: "which rule a site or IP hits", run: func() error {
				dest, err := ui.Text("Domain or IP to test", "", func(v string) error {
					if v == "" {
						return fail("enter a domain or IP")
					}
					return nil
				})
				if err != nil {
					return err
				}
				return cmdRulesTest(args{pos: []string{dest}, flags: map[string][]string{}})
			}},
		}},
		{label: "Settings", hint: "ports, LAN, DNS", run: with(cmdSettings)},
		{label: "Status", hint: "daemon, xray and connectivity", run: with(cmdStatus)},
		{label: "Quit"},
	}
}

// statusLine is the one-line state shown above the menu.
func statusLine(st *pb.Status) string {
	state := ui.Gray("○ off")
	switch {
	case st.TunRunning:
		state = ui.Green("● on") + ui.Dim(" · TUN + proxy "+st.ProxyAddress)
	case st.Settings.GetPower() && st.ProxyRunning:
		state = ui.Green("● on") + ui.Dim(" · proxy "+st.ProxyAddress)
	case st.ProxyRunning:
		state = ui.Gray("○ off") + ui.Dim(" · always-on proxy "+st.ProxyAddress)
	}
	if st.LastError != "" {
		state += "  " + ui.Red(ui.Visual(st.LastError))
	}
	if st.ActiveConfigName != "" {
		state += fmt.Sprintf("  %s %s", ui.Dim("via"), ui.Cyan(ui.Visual(st.ActiveConfigName)))
	}
	return state
}
