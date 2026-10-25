package main

import (
	"testing"
)

func TestRuleMatchersOnlyUsesNames(t *testing.T) {
	a := args{flags: map[string][]string{
		"domain":      {"secret.example.com"},
		"ip":          {"10.1.2.3"},
		"app":         {"/Applications/Telegram.app/"},
		"source-port": {"5000"},
	}}
	got := ruleMatchers(a)
	want := map[string]bool{"domain": true, "ip": true, "process": true, "sourcePort": true}
	if len(got) != len(want) {
		t.Fatalf("ruleMatchers = %v", got)
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("unexpected matcher %q", k)
		}
	}
	for _, v := range got {
		if v == "secret.example.com" || v == "10.1.2.3" {
			t.Fatal("ruleMatchers leaked a value")
		}
	}
}

func TestSettingKeyOnlyUsesName(t *testing.T) {
	got := settingKey("settings set", args{pos: []string{"mixedPort", "7891"}})
	if got != "mixedPort" {
		t.Fatalf("settingKey = %q", got)
	}
	if got := settingKey("settings set", args{pos: []string{"not-a-setting", "x"}}); got != "" {
		t.Fatalf("unknown setting = %q", got)
	}
}

func TestCommandTargets(t *testing.T) {
	for _, tc := range []struct {
		path string
		a    args
		want string
	}{
		{"connect", args{flags: map[string][]string{"proxy": {"true"}}}, "proxy"},
		{"connect", args{flags: map[string][]string{}}, "tun"},
		{"use", args{flags: map[string][]string{"fastest": {"true"}}}, "fastest"},
		{"use", args{pos: []string{"abc"}}, "id"},
		{"use", args{}, "interactive"},
		{"mode", args{pos: []string{"proxy"}}, "proxy"},
		{"update", args{flags: map[string][]string{"check": {"true"}}}, "check"},
		{"update status", args{}, "status"},
	} {
		if got := commandTarget(tc.path, tc.a); got != tc.want {
			t.Errorf("commandTarget(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestErrorPropsNoRawMessage(t *testing.T) {
	props := errorProps(fail("secret.example.com: permission denied"))
	if props["error_kind"] != "permission_denied" {
		t.Fatalf("kind = %v", props["error_kind"])
	}
	if _, ok := props["error_message"]; ok {
		t.Fatal("CLI errors must not send raw messages")
	}
}
