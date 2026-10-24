package store

import (
	"fmt"
	"strings"
)

type TargetKind string

const (
	TargetDirect   TargetKind = "direct"
	TargetBlock    TargetKind = "block"
	TargetProxy    TargetKind = "proxy"
	TargetConfig   TargetKind = "config"   // a config pinned from a subscription
	TargetOutbound TargetKind = "outbound" // a custom outbound
)

// Target is where a rule sends its traffic. For a pinned config, Name is the config's
// name when it was pinned, so a provider rotating the server's settings can be followed.
type Target struct {
	Kind           TargetKind `json:"kind"`
	SubscriptionID string     `json:"subscriptionId,omitempty"`
	ConfigID       string     `json:"configId,omitempty"`
	Name           string     `json:"name,omitempty"`
	Tag            string     `json:"tag,omitempty"`
}

// ParseTarget reads "direct", "block", "proxy", "config:<sub>/<cfg>" or "outbound:<tag>".
func ParseTarget(s string) (Target, error) {
	switch s {
	case "direct", "block", "proxy":
		return Target{Kind: TargetKind(s)}, nil
	}
	if rest, ok := strings.CutPrefix(s, "config:"); ok {
		sub, cfg, ok := strings.Cut(rest, "/")
		if ok && sub != "" && cfg != "" {
			return Target{Kind: TargetConfig, SubscriptionID: sub, ConfigID: cfg}, nil
		}
	}
	if tag, ok := strings.CutPrefix(s, "outbound:"); ok && tag != "" {
		return Target{Kind: TargetOutbound, Tag: tag}, nil
	}
	return Target{}, fmt.Errorf("target %q: want direct, block, proxy, config:<subscription>/<config> or outbound:<tag>: %w", s, ErrInvalid)
}

func (t Target) String() string {
	switch t.Kind {
	case TargetConfig:
		return "config:" + t.SubscriptionID + "/" + t.ConfigID
	case TargetOutbound:
		return "outbound:" + t.Tag
	default:
		return string(t.Kind)
	}
}

func (t Target) validate() error {
	_, err := ParseTarget(t.String())
	return err
}
