package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/atomicfile"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
)

var (
	ErrInvalid  = errors.New("invalid argument")
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrInUse    = errors.New("in use")
)

// Tags the composer generates. User tags may not take them, or anything starting with "masir-".
var (
	reservedOutbounds = []string{"direct", "block", "proxy", "dns-out", "loopback-dns", "api"}
	reservedInbounds  = []string{"tun-in", "loopback-dns-in", "dns-bootstrap"}
)

const reservedPrefix = "masir-"

// Item is a custom inbound or outbound; Config always carries Tag as its "tag".
type Item struct {
	Tag    string          `json:"tag"`
	Config json.RawMessage `json:"config"`
}

type Rule struct {
	Tag    string          `json:"tag"`
	Match  json.RawMessage `json:"match"` // an xray field rule minus ruleTag/outboundTag/balancerTag
	Target Target          `json:"target"`
}

type State struct {
	Settings  Settings
	Rules     []Rule
	Inbounds  []Item
	Outbounds []Item
}

// Clone copies the slices; their elements are never modified in place.
func (s State) Clone() State {
	s.Settings.DNSServers = slices.Clone(s.Settings.DNSServers)
	s.Settings.TunDNS = slices.Clone(s.Settings.TunDNS)
	s.Rules = slices.Clone(s.Rules)
	s.Inbounds = slices.Clone(s.Inbounds)
	s.Outbounds = slices.Clone(s.Outbounds)
	return s
}

// AddRule validates the rule and puts it last, or first when prepend is set.
func (s *State) AddRule(tag string, match []byte, target Target, prepend bool) error {
	r, err := s.newRule(tag, match, target)
	if err != nil {
		return err
	}
	if s.ruleIndex(r.Tag) >= 0 {
		return fmt.Errorf("rule %q: %w", r.Tag, ErrExists)
	}
	if prepend {
		s.Rules = append([]Rule{r}, s.Rules...)
	} else {
		s.Rules = append(s.Rules, r)
	}
	return nil
}

// AlterRule changes the match and/or target (nil leaves it), keeping the rule's place.
func (s *State) AlterRule(tag string, match []byte, target *Target) error {
	i := s.ruleIndex(tag)
	if i < 0 {
		return fmt.Errorf("rule %q: %w", tag, ErrNotFound)
	}
	cur := s.Rules[i]
	if match == nil {
		match = cur.Match
	}
	t := cur.Target
	if target != nil {
		t = *target
	}
	r, err := s.newRule(tag, match, t)
	if err != nil {
		return err
	}
	s.Rules[i] = r
	return nil
}

func (s *State) RenameRule(tag, newTag string) error {
	i := s.ruleIndex(tag)
	if i < 0 {
		return fmt.Errorf("rule %q: %w", tag, ErrNotFound)
	}
	if newTag == tag {
		return nil
	}
	if err := checkTag("rule", newTag, nil); err != nil {
		return err
	}
	if s.ruleIndex(newTag) >= 0 {
		return fmt.Errorf("rule %q: %w", newTag, ErrExists)
	}
	s.Rules[i].Tag = newTag
	return nil
}

// MoveRule puts the rule at index in the resulting order; past the end means last.
func (s *State) MoveRule(tag string, index int) error {
	i := s.ruleIndex(tag)
	if i < 0 {
		return fmt.Errorf("rule %q: %w", tag, ErrNotFound)
	}
	r := s.Rules[i]
	s.Rules = slices.Delete(s.Rules, i, i+1)
	index = min(max(index, 0), len(s.Rules))
	s.Rules = slices.Insert(s.Rules, index, r)
	return nil
}

func (s *State) RemoveRule(tag string) error {
	i := s.ruleIndex(tag)
	if i < 0 {
		return fmt.Errorf("rule %q: %w", tag, ErrNotFound)
	}
	s.Rules = slices.Delete(s.Rules, i, i+1)
	return nil
}

func (s *State) AddOutbound(tag string, raw []byte) error {
	it, err := newItem("outbound", raw, tag, reservedOutbounds, xray.ValidateOutbound)
	if err != nil {
		return err
	}
	if strings.HasPrefix(it.Tag, "sub-") {
		return fmt.Errorf("outbound tag %q: the sub- prefix is reserved: %w", it.Tag, ErrInvalid)
	}
	return add(&s.Outbounds, "outbound", it)
}

func (s *State) AlterOutbound(tag string, raw []byte) error {
	it, err := newItem("outbound", raw, tag, reservedOutbounds, xray.ValidateOutbound)
	if err != nil {
		return err
	}
	return replace(s.Outbounds, "outbound", it)
}

// RemoveOutbound refuses while a rule still targets the outbound.
func (s *State) RemoveOutbound(tag string) error {
	for _, r := range s.Rules {
		if r.Target.Kind == TargetOutbound && r.Target.Tag == tag {
			return fmt.Errorf("outbound %q is the target of rule %q: %w", tag, r.Tag, ErrInUse)
		}
	}
	return remove(&s.Outbounds, "outbound", tag)
}

func (s *State) AddInbound(tag string, raw []byte) error {
	it, err := newItem("inbound", raw, tag, reservedInbounds, xray.ValidateInbound)
	if err != nil {
		return err
	}
	return add(&s.Inbounds, "inbound", it)
}

func (s *State) AlterInbound(tag string, raw []byte) error {
	it, err := newItem("inbound", raw, tag, reservedInbounds, xray.ValidateInbound)
	if err != nil {
		return err
	}
	return replace(s.Inbounds, "inbound", it)
}

func (s *State) RemoveInbound(tag string) error {
	return remove(&s.Inbounds, "inbound", tag)
}

func (s *State) newRule(tag string, raw []byte, target Target) (Rule, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return Rule{}, fmt.Errorf("rule is not a JSON object: %w", ErrInvalid)
	}
	for _, k := range []string{"outboundTag", "balancerTag"} {
		if _, ok := m[k]; ok {
			return Rule{}, fmt.Errorf("rule JSON can't set %s; use the target: %w", k, ErrInvalid)
		}
	}
	if t, ok := m["ruleTag"]; ok {
		if tag == "" {
			_ = json.Unmarshal(t, &tag)
		}
		delete(m, "ruleTag")
	}
	if err := checkTag("rule", tag, nil); err != nil {
		return Rule{}, err
	}
	if err := target.validate(); err != nil {
		return Rule{}, err
	}
	if target.Kind == TargetOutbound && index(s.Outbounds, target.Tag) < 0 {
		return Rule{}, fmt.Errorf("target outbound %q: %w", target.Tag, ErrNotFound)
	}

	match, _ := json.Marshal(m)
	check := maps.Clone(m)
	check["outboundTag"] = json.RawMessage(`"direct"`)
	full, _ := json.Marshal(check)
	if err := xray.ValidateRule(full); err != nil {
		return Rule{}, fmt.Errorf("rule %q: %v: %w", tag, err, ErrInvalid)
	}
	return Rule{Tag: tag, Match: match, Target: target}, nil
}

func (s *State) ruleIndex(tag string) int {
	return slices.IndexFunc(s.Rules, func(r Rule) bool { return r.Tag == tag })
}

func newItem(what string, raw []byte, tag string, reserved []string, validate func(json.RawMessage) error) (Item, error) {
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return Item{}, fmt.Errorf("%s is not a JSON object: %w", what, ErrInvalid)
	}
	if tag == "" {
		if t, ok := o["tag"]; ok {
			_ = json.Unmarshal(t, &tag)
		}
	}
	if err := checkTag(what, tag, reserved); err != nil {
		return Item{}, err
	}
	o["tag"], _ = json.Marshal(tag)
	cfg, _ := json.Marshal(o)
	if err := validate(cfg); err != nil {
		return Item{}, fmt.Errorf("%s %q: %v: %w", what, tag, err, ErrInvalid)
	}
	return Item{Tag: tag, Config: cfg}, nil
}

func checkTag(what, tag string, reserved []string) error {
	switch {
	case tag == "":
		return fmt.Errorf("%s tag is required: %w", what, ErrInvalid)
	case strings.HasPrefix(tag, reservedPrefix) || slices.Contains(reserved, tag):
		return fmt.Errorf("%s tag %q is reserved: %w", what, tag, ErrInvalid)
	}
	return nil
}

func add(items *[]Item, what string, it Item) error {
	if index(*items, it.Tag) >= 0 {
		return fmt.Errorf("%s %q: %w", what, it.Tag, ErrExists)
	}
	*items = append(*items, it)
	return nil
}

func replace(items []Item, what string, it Item) error {
	i := index(items, it.Tag)
	if i < 0 {
		return fmt.Errorf("%s %q: %w", what, it.Tag, ErrNotFound)
	}
	items[i] = it
	return nil
}

func remove(items *[]Item, what, tag string) error {
	i := index(*items, tag)
	if i < 0 {
		return fmt.Errorf("%s %q: %w", what, tag, ErrNotFound)
	}
	*items = slices.Delete(*items, i, i+1)
	return nil
}

func index(items []Item, tag string) int {
	return slices.IndexFunc(items, func(it Item) bool { return it.Tag == tag })
}

const (
	settingsFile  = "settings.json"
	routingFile   = "routing.json"
	inboundsFile  = "inbounds.json"
	outboundsFile = "outbounds.json"
)

// Load reads the state from dir. Missing files mean defaults; missing settings keep their defaults.
func Load(dir string) (State, error) {
	st := State{Settings: DefaultSettings()}
	var routing struct {
		Rules []Rule `json:"rules"`
	}
	for _, f := range []struct {
		name string
		into any
	}{
		{settingsFile, &st.Settings},
		{routingFile, &routing},
		{inboundsFile, &st.Inbounds},
		{outboundsFile, &st.Outbounds},
	} {
		raw, err := os.ReadFile(filepath.Join(dir, f.name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return State{}, err
		}
		if err := json.Unmarshal(raw, f.into); err != nil {
			return State{}, fmt.Errorf("%s: %w", f.name, err)
		}
	}
	st.Rules = routing.Rules
	if err := st.Settings.Validate(); err != nil {
		return State{}, fmt.Errorf("%s: %w", settingsFile, err)
	}
	return st, nil
}

// Save writes every part. Each file is replaced atomically.
func Save(dir string, st State) error {
	rules := st.Rules
	if rules == nil {
		rules = []Rule{}
	}
	for _, f := range []struct {
		name string
		v    any
	}{
		{settingsFile, st.Settings},
		{routingFile, map[string]any{"rules": rules}},
		{inboundsFile, nonNil(st.Inbounds)},
		{outboundsFile, nonNil(st.Outbounds)},
	} {
		raw, err := json.MarshalIndent(f.v, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicfile.Write(filepath.Join(dir, f.name), raw); err != nil {
			return err
		}
	}
	return nil
}

func nonNil(items []Item) []Item {
	if items == nil {
		return []Item{}
	}
	return items
}
