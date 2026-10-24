package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// Completions ask a daemon that's already running and never start one: pressing Tab
// shouldn't launch a background process.

func completeRules(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	c, ok := runningDaemon()
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := rpc()
	defer cancel()
	res, err := c.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, r := range res.Rules {
		out = append(out, r.RuleTag+"\t→ "+stripANSI(targetText(r.Target, r.ResolvedOutboundTag, r.FellBack, nil)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeSubs(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	c, ok := runningDaemon()
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := rpc()
	defer cancel()
	res, err := c.ListSubscriptions(ctx, &pb.ListSubscriptionsRequest{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, s := range res.Subscriptions {
		out = append(out, s.Name+"\t"+stripANSI(subHint(s)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeNodes offers config ids, described by name and subscription.
func completeNodes(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	c, ok := runningDaemon()
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(appCtx, rpcTimeoutShort)
	defer cancel()
	list, err := c.ListSubscriptions(ctx, &pb.ListSubscriptionsRequest{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, s := range list.Subscriptions {
		got, err := c.GetSubscription(ctx, &pb.GetSubscriptionRequest{Id: s.Id})
		if err != nil {
			continue
		}
		for _, cfg := range got.Configs {
			out = append(out, cfg.Id+"\t"+strings.TrimSpace(cfg.Name)+" ("+s.Name+")")
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeSettingKeys(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, s := range settingsTable {
		out = append(out, s.key+"\t"+s.desc)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
