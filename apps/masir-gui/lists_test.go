package main

import (
	"fmt"
	"testing"
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// manyRules makes n rules across the sections, each with its own JSON.
func manyRules(n int) []*pb.RoutingRule {
	rules := make([]*pb.RoutingRule, n)
	for i := range rules {
		switch i % 4 {
		case 0:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"domain":["full:site%d.test"]}`, i), Target: "proxy"}
		case 1:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"domain":["geosite:cat%d"]}`, i), Target: "proxy"}
		case 2:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"ip":["10.%d.0.0/16"]}`, i%256), Target: "direct"}
		default:
			rules[i] = &pb.RoutingRule{RuleTag: fmt.Sprintf("r%d", i), RuleJson: fmt.Sprintf(`{"port":["%d"]}`, 1000+i), Target: "block"}
		}
	}
	return rules
}

// manyConfigs makes n configurations for a subscription.
func manyConfigs(n int) []*pb.SubscriptionConfig {
	cfgs := make([]*pb.SubscriptionConfig, n)
	for i := range cfgs {
		cfgs[i] = &pb.SubscriptionConfig{Id: fmt.Sprintf("x%d", i), Name: fmt.Sprintf("Node %d", i), Protocol: "vless"}
	}
	return cfgs
}

// TestRulesListBuildsOnlyVisibleRows checks that opening the rules list doesn't build every
// rule: the list builds what it shows and a few around it, and still reaches the last one.
func TestRulesListBuildsOnlyVisibleRows(t *testing.T) {
	f := ukServers()
	f.rules = manyRules(200)
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("RULES"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	first, last := a.rulesList.Visible()
	if first < 0 || last < 0 {
		t.Fatalf("the rules list wasn't laid out: %d..%d", first, last)
	}
	if built := last - first + 1; built > 24 {
		t.Fatalf("built %d rows of 200; want only the visible rows", built)
	}

	a.rulesList.ScrollToEnd()
	tt.Advance(time.Second)
	if !tt.HasText("1199") {
		t.Fatalf("the list lost its last rule: %q", tt.Texts())
	}
}

// TestServerPickerBuildsOnlyVisibleRows checks the servers picker the same way.
func TestServerPickerBuildsOnlyVisibleRows(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = append(f.configs["s1"], manyConfigs(160)...)
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click(ltr("United Kingdom")); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	first, last := a.serversList.Visible()
	if first < 0 || last < 0 {
		t.Fatalf("the servers list wasn't laid out: %d..%d", first, last)
	}
	if built := last - first + 1; built > 24 {
		t.Fatalf("built %d rows of 160; want only the visible rows", built)
	}
}

// TestSettingsServerListBuildsOnlyVisibleRows checks the subscription card in settings,
// which lists a subscription's configurations.
func TestSettingsServerListBuildsOnlyVisibleRows(t *testing.T) {
	f := ukServers()
	f.configs["s1"] = manyConfigs(160)
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	st := a.subLists["s1"]
	if st == nil {
		t.Fatal("the subscription's list is missing")
	}
	first, last := st.Visible()
	if first < 0 || last < 0 {
		t.Fatalf("the subscription list wasn't laid out: %d..%d", first, last)
	}
	if built := last - first + 1; built > 24 {
		t.Fatalf("built %d rows of 160; want only the visible rows", built)
	}
}
