package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
)

var version = "dev" // overridden via -ldflags "-X main.version=0.1.0"

var matcherFlags = []flagSpec{
	{names: []string{"domain"}, many: true, help: "websites: example.com, geosite:netflix, keyword:x, regexp:..."},
	{names: []string{"ip"}, many: true, help: "Addresses or CIDRs, e.g. 1.2.3.4, 10.0.0.0/8, geoip:ir"},
	{names: []string{"port"}, many: true, help: "ports or ranges: 443, 1000-2000"},
	{names: []string{"network"}, many: true, help: "Network: tcp, udp, or tcp,udp for both"},
	{names: []string{"protocol"}, many: true, help: "Detected protocols: http, tls, quic, bittorrent"},
	{names: []string{"process"}, many: true, help: "process names or paths (a path ending in / matches a folder)"},
	{names: []string{"app"}, many: true, help: "an app's name or bundle path, e.g. /Applications/Telegram.app/"},
	{names: []string{"source"}, many: true, help: "source IPs or CIDRs"},
	{names: []string{"source-port"}, many: true, help: "source ports"},
}

// exitStatus is set by commands that failed; they report their own errors (see runE).
var exitStatus int

// elevatedResult is set (--elevated-result) when masir runs elevated for another masir
// process, which can't see this one's output (see elevate on Windows): the outcome
// goes to this file. lastErr is what failed.
var elevatedResult, lastErr string

func main() {
	ui.SetupBidi()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	appCtx = ctx
	ui.OnInterrupt = stop // a spinner catching Ctrl-C also ends the app's work
	// fang parses arguments and renders help; errors that reach it are cobra's usage
	// errors (unknown command, bad flag), which handleError prints in masir's style.
	err := fang.Execute(ctx, rootCmd(), fang.WithVersion(version), fang.WithErrorHandler(handleError))
	stop()
	if elevatedResult != "" {
		writeElevatedResult(err == nil && exitStatus == 0)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(exitStatus)
}

// writeElevatedResult writes "ok" or the error, atomically so the waiting process
// never reads half of it.
func writeElevatedResult(ok bool) {
	msg := "ok"
	if !ok {
		msg = lastErr
		if msg == "" {
			msg = "the elevated masir failed"
		}
	}
	tmp := elevatedResult + ".tmp"
	if os.WriteFile(tmp, []byte(msg), 0o600) == nil {
		_ = os.Rename(tmp, elevatedResult)
	}
}

func handleError(_ io.Writer, _ fang.Styles, err error) {
	lastErr = err.Error()
	ui.Error(ui.Red(capitalize(err.Error())) + "\n" + ui.Dim("Run `masir --help` for usage."))
}

// report prints a command's error the clack way and returns the exit code: 130 when
// the user quit, 1 otherwise.
func report(err error) int {
	switch {
	case cancelled(err):
		lastErr = "cancelled"
		ui.Cancelled()
		return 130
	case errors.Is(err, errReported):
		return 1
	default:
		lastErr = message(err)
		ui.Error(ui.Red(lastErr))
		return 1
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "masir",
		Short: "Proxy manager for Xray",
		Long:  "Proxy manager for Xray: subscriptions, routing rules, and a system-wide TUN, run by its daemon (`masir daemon`).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "masir %s\n", cmd.Root().Version)
			return cmd.Help()
		},
	}
	// picocolors reads these from os.Args itself; declaring them keeps cobra from rejecting them.
	root.PersistentFlags().Bool("color", false, "force colored output")
	root.PersistentFlags().Bool("no-color", false, "disable colored output")
	root.PersistentFlags().StringVar(&elevatedResult, "elevated-result", "", "file for an elevated run's outcome")
	_ = root.PersistentFlags().MarkHidden("elevated-result")
	_ = root.PersistentFlags().MarkHidden("color")
	_ = root.PersistentFlags().MarkHidden("no-color")

	status := leaf("status", "Show the daemon, Xray and connectivity status", cmdStatus, cobra.NoArgs,
		flagSpec{names: []string{"check"}, bool: true, def: true, help: "test connectivity"},
		flagSpec{names: []string{"no-check"}, bool: true, help: "skip the connectivity test"},
		flagSpec{names: []string{"json"}, bool: true, help: "machine-readable output"})

	connect := leaf("connect", "Turn masir on: TUN mode unless --proxy", cmdConnect, cobra.NoArgs,
		flagSpec{names: []string{"proxy", "p"}, bool: true, help: "proxy only, no TUN"},
		flagSpec{names: []string{"tun", "t"}, bool: true, def: true, help: "system-wide TUN"})
	connect.Aliases = []string{"start"}
	connect.Example = "# Route all system traffic through masir (TUN)\n" +
		"masir connect\n" +
		"# Only apps set to use the proxy port\n" +
		"masir connect --proxy"

	disconnect := leaf("disconnect", "Turn masir off; the daemon keeps running", cmdDisconnect, cobra.NoArgs)
	disconnect.Aliases = []string{"stop"}

	useFlags := flagSpec{names: []string{"fastest"}, bool: true, help: "test every proxy and use the fastest"}
	use := leaf("use [id]", "Switch the active proxy", cmdUse, cobra.MaximumNArgs(1), useFlags)
	use.ValidArgsFunction = completeNodes
	use.Example = "# Pick from a list, with live latency\n" +
		"masir use\n" +
		"# Test every proxy, use the quickest\n" +
		"masir use --fastest\n" +
		"# Use a proxy by its id (from `masir sub list --nodes`)\n" +
		"masir use 737e3ea15f56"

	mode := leaf("mode [proxy|direct]", "Where traffic no rule matches goes", cmdMode, cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs))
	mode.ValidArgs = []string{"proxy\tunmatched traffic goes through the active proxy", "direct\tunmatched traffic skips the proxy"}
	mode.Example = "# Everything not matched by a rule uses the proxy\n" +
		"masir mode proxy\n" +
		"# Print the current mode\n" +
		"masir mode"

	subUse := leaf("use [id]", "Same as `masir use`", cmdUse, cobra.MaximumNArgs(1), useFlags)
	subUse.ValidArgsFunction = completeNodes
	subUpdate := leaf("update [name]", "Refresh one subscription, or all", cmdSubUpdate, cobra.MaximumNArgs(1),
		flagSpec{names: []string{"name"}, help: "only this subscription"})
	subUpdate.ValidArgsFunction = completeSubs
	subRemove := leaf("remove [names...]", "Remove subscriptions", cmdSubRemove, cobra.ArbitraryArgs)
	subRemove.Aliases = []string{"rm"}
	subRemove.ValidArgsFunction = completeSubs
	subList := leaf("list", "List subscriptions with traffic and expiry", cmdSubList, cobra.NoArgs,
		flagSpec{names: []string{"nodes"}, bool: true, help: "also list each config with its id"})
	subList.Aliases = []string{"ls"}
	subAdd := leaf("add [url]", "Add a subscription", cmdSubAdd, cobra.MaximumNArgs(1),
		flagSpec{names: []string{"name"}, help: "a name; default: the provider's title"},
		flagSpec{names: []string{"interval"}, help: "refresh every N minutes; default: the provider's, else 12h"})
	subAdd.Example = "# Add a subscription; its name comes from the provider\n" +
		"masir sub add https://example.com/sub\n" +
		"# Name it, and refresh it every 6 hours\n" +
		"masir sub add https://example.com/sub --name Work --interval 360"
	sub := group("sub", "List and manage subscriptions", subAdd, subList, subUpdate, subRemove, subUse)
	listByDefault(sub, cmdSubList, flagSpec{names: []string{"nodes"}, bool: true, help: "also list each config with its id"})
	sub.Aliases = []string{"subscription", "subs"}

	targetFlags := []flagSpec{
		{names: []string{"outbound", "o"}, help: "proxy, direct, block, a proxy id, or outbound:<tag>"},
		{names: []string{"via"}, help: "pin to a proxy id"},
	}
	rulesAdd := leaf("add", "Add a rule (a wizard in a terminal, or flags)", cmdRulesAdd, cobra.NoArgs,
		append(append(clone(matcherFlags), targetFlags...),
			flagSpec{names: []string{"name"}, help: "the rule's name; default: generated"},
			flagSpec{names: []string{"json"}, help: "the rule's match as xray JSON"})...)
	rulesEdit := leaf("edit [name]", "Change a rule (a menu in a terminal, or flags)", cmdRulesEdit, cobra.MaximumNArgs(1),
		append(append(clone(matcherFlags), targetFlags...),
			flagSpec{names: []string{"rename"}, help: "a new name"},
			flagSpec{names: []string{"json"}, help: "replace the match with xray JSON"},
			flagSpec{names: []string{"append"}, bool: true, help: "add matcher values instead of replacing them"},
			flagSpec{names: []string{"unset"}, many: true, help: "drop a matcher: domain, ip, port, ..."})...)
	rulesEdit.ValidArgsFunction = completeRules
	rulesEdit.Example = "# Change a rule from a menu\n" +
		"masir rules edit netflix\n" +
		"# Add another website to it\n" +
		"masir rules edit netflix --domain hulu.com --append\n" +
		"# Rename it and send its traffic through the active proxy\n" +
		"masir rules edit netflix --rename streaming -o proxy"
	rulesAdd.Example = "# Build a rule step by step\n" +
		"masir rules add\n" +
		"# Send Iranian websites direct\n" +
		"masir rules add --domain geosite:category-ir -o direct\n" +
		"# Send Telegram, with its helper processes, through the proxy\n" +
		"masir rules add --app /Applications/Telegram.app/ -o proxy\n" +
		"# Always use one specific proxy for Netflix\n" +
		"masir rules add --domain netflix.com --via 737e3ea15f56"
	rulesList := leaf("list", "List rules in match order", cmdRulesList, cobra.NoArgs,
		flagSpec{names: []string{"json"}, bool: true, help: "machine-readable output"})
	rulesList.Aliases = []string{"ls"}
	rulesRemove := leaf("remove [names...]", "Remove rules", cmdRulesRemove, cobra.ArbitraryArgs)
	rulesRemove.Aliases = []string{"rm"}
	rulesRemove.ValidArgsFunction = completeRules
	rulesTest := leaf("test <domain|ip>", "Show where traffic to a destination goes", cmdRulesTest, cobra.ExactArgs(1),
		flagSpec{names: []string{"port"}, help: "Destination port (default 443)"},
		flagSpec{names: []string{"network"}, help: "Network: tcp (default) or udp"})
	rulesTest.Example = "# Where does Netflix go?\n" +
		"masir rules test netflix.com\n" +
		"# Check a UDP destination\n" +
		"masir rules test 8.8.8.8 --port 53 --network udp"
	rules := group("rules", "List and manage routing rules", rulesAdd, rulesList, rulesEdit, rulesTest, rulesRemove)
	listByDefault(rules, cmdRulesList, flagSpec{names: []string{"json"}, bool: true, help: "machine-readable output"})

	settingsList := leaf("list", "Show every setting", cmdSettingsList, cobra.NoArgs)
	settingsList.Aliases = []string{"ls"}
	settingsGet := leaf("get <key>", "Print one setting", cmdSettingsGet, cobra.ExactArgs(1))
	settingsSet := leaf("set <key> <value...>", "Change a setting", cmdSettingsSet, cobra.MinimumNArgs(2))
	settingsSet.Example = "# Move the proxy to another port\n" +
		"masir settings set mixedPort 7891\n" +
		"# Stop sharing the proxy with other devices\n" +
		"masir settings set allowLan off\n" +
		"# Several values for a list setting\n" +
		"masir settings set dnsServers 1.1.1.1 9.9.9.9"
	settingsReset := leaf("reset <key>", "Put a setting back to its default", cmdSettingsReset, cobra.ExactArgs(1))
	for _, c := range []*cobra.Command{settingsGet, settingsSet, settingsReset} {
		c.ValidArgsFunction = completeSettingKeys
	}
	settings := group("settings", "View or change settings (a menu in a terminal)",
		settingsList, settingsGet, settingsSet, settingsReset)
	settings.Args = cobra.NoArgs
	settings.RunE = runE(cmdSettings)

	getConfig := leaf("get-config", "Print the Xray config masir runs", cmdGetConfig, cobra.NoArgs)
	getConfig.Aliases = []string{"config"}

	restart := leaf("restart", "Restart the daemon", daemonRestart, cobra.NoArgs)
	restart.Aliases = []string{"reload"}
	enable := leaf("enable", "Install as a root service (needed for TUN); asks for sudo", daemonEnable, cobra.NoArgs,
		flagSpec{names: []string{"user"}, help: "The uid to serve (default: you)"})
	enable.Aliases = []string{"install"}
	disable := leaf("disable", "Remove the root service; asks for sudo", daemonDisable, cobra.NoArgs)
	disable.Aliases = []string{"uninstall"}
	runDaemon := leaf("run", "Run the daemon in the foreground", daemonRun, cobra.NoArgs,
		flagSpec{names: []string{"user"}, help: "As root: the user to serve (name or uid); default SUDO_UID"},
		flagSpec{names: []string{"idle"}, bool: true, help: "Start with masir off and Xray down until it's turned on"})
	runDaemon.Hidden = true // the background daemon and the service run this
	syncSvc := leaf("sync-service", "Copy a new masir binary into the system service's location", daemonSyncService, cobra.NoArgs,
		flagSpec{names: []string{"from"}, help: "The new masir binary"})
	syncSvc.Hidden = true // masir update runs it with root rights
	daemonGroup := group("daemon", "Manage the masir daemon",
		leaf("start", "Start the daemon in the background", daemonStart, cobra.NoArgs),
		leaf("stop", "Stop the daemon", daemonStop, cobra.NoArgs),
		restart, enable, disable, runDaemon, syncSvc)

	targetArg := cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs)
	targets := []string{"masir\tthe app itself", "geo\tgeosite.dat and geoip.dat"}
	control := func(use, short string, fn func(args) error) *cobra.Command {
		c := leaf(use+" [masir|geo]", short, fn, targetArg)
		c.ValidArgs = targets
		return c
	}
	updateGeo := leaf("geo", "Download the latest geosite.dat and geoip.dat from Loyalsoldier/v2ray-rules-dat", cmdUpdateGeo, cobra.NoArgs,
		flagSpec{names: []string{"force"}, bool: true, help: "download even if the installed files are current"})
	updateStatus := control("status", "Show update jobs, following one that's running", cmdUpdateStatus)
	updateCmd := group("update", "Update masir to the latest release, or refresh geo data",
		updateGeo,
		control("pause", "Pause a download; it resumes where it stopped", cmdUpdatePause),
		control("resume", "Resume a paused update", cmdUpdateResume),
		control("stop", "Stop an update and delete what it downloaded", cmdUpdateStop),
		updateStatus)
	updateCmd.Args = cobra.NoArgs
	updateCmd.RunE = runE(cmdUpdate)
	addFlags(updateCmd, []flagSpec{
		{names: []string{"check"}, bool: true, help: "only report whether a newer version exists"},
		{names: []string{"yes", "y"}, bool: true, help: "install without asking"},
		{names: []string{"force"}, bool: true, help: "install even if not newer, or over a development build"},
	})
	updateCmd.Example = "# Install the latest release\n" +
		"masir update\n" +
		"# Is there something newer?\n" +
		"masir update --check\n" +
		"# Refresh geosite.dat and geoip.dat\n" +
		"masir update geo\n" +
		"# Control a download that's in progress\n" +
		"masir update pause\n" +
		"masir update resume"

	// Help lists commands in this order, most used first; cobra appends completion and help.
	cobra.EnableCommandSorting = false
	root.AddCommand(connect, disconnect, status, use, mode, sub, rules, settings,
		leaf("logs", "Follow the daemon's log", cmdLogs, cobra.NoArgs),
		daemonGroup, updateCmd, getConfig,
		leaf("menu", "Pick a command from an interactive menu", func(args) error { return menu() }, cobra.NoArgs),
		leaf("version", "Print the versions of masir, its daemon and Xray", cmdVersion, cobra.NoArgs))
	return root
}

// leaf is a command that runs fn with what cobra parsed.
func leaf(use, short string, fn func(args) error, positional cobra.PositionalArgs, flags ...flagSpec) *cobra.Command {
	c := &cobra.Command{Use: use, Short: short, Args: positional, RunE: runE(fn)}
	addFlags(c, flags)
	return c
}

// listByDefault makes a bare group command list its items, as `masir sub` and
// `masir rules` do, taking the same flags as its list subcommand.
func listByDefault(c *cobra.Command, list func(args) error, flags ...flagSpec) {
	c.Args = cobra.NoArgs
	c.RunE = runE(list)
	addFlags(c, flags)
}

// group is a command that only holds subcommands.
func group(use, short string, subs ...*cobra.Command) *cobra.Command {
	c := &cobra.Command{Use: use, Short: short}
	c.AddCommand(subs...)
	return c
}

// runE runs fn and reports its error itself, so fang's error path is left to cobra's
// usage errors and command failures always look like the rest of masir.
func runE(fn func(args) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, pos []string) error {
		if err := fn(argsFrom(cmd, pos)); err != nil {
			exitStatus = report(err)
		}
		return nil
	}
}

func clone(specs []flagSpec) []flagSpec { return append([]flagSpec(nil), specs...) }

func cmdVersion(args) error {
	fmt.Printf("masir %s\n", version)
	if c, ok := runningDaemon(); ok {
		ctx, cancel := rpc()
		defer cancel()
		if res, err := c.GetStatus(ctx, nil); err == nil {
			fmt.Printf("daemon %s\nxray-core %s\n", res.Status.Version, res.Status.XrayVersion)
		}
	}
	return nil
}
