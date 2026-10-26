package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	apiInboundTag = "masir-api"
	apiRule       = `{"type":"field","ruleTag":"masir-api-rule","inboundTag":["masir-api"],"outboundTag":"api"}`
)

// item is one outbound or rule. JSON is re-marshalled, so byte equality means "same config".
type item struct {
	Tag  string
	JSON json.RawMessage
}

// Profile is an Xray config split into the part that needs a restart (base)
// and the parts that can change live through the API (inbounds, outbounds, rules).
type Profile struct {
	base      map[string]json.RawMessage // everything except inbounds, outbounds and the live routing parts
	inbounds  []item
	outbounds []item
	rules     []item
	balancers json.RawMessage // the fallback balancer, retargeted live instead of applied
	fallback  fallback        // where that balancer points, i.e. the routing mode
	hash      string          // of base
}

// fallback is the catch-all's balancer and the outbound it should send traffic to.
// Switching it is one OverrideBalancerTarget call; none of the config is rewritten.
type fallback struct {
	balancer string
	target   string
}

func ParseProfile(raw []byte) (*Profile, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("config is not valid JSON (strip comments first): %w", err)
	}
	p := &Profile{base: top}

	if v, ok := top["inbounds"]; ok {
		items, err := parseItems(v, "tag", "inbound", "masir-inbound-")
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.Tag == apiInboundTag {
				return nil, fmt.Errorf("inbound tag %q is reserved", apiInboundTag)
			}
		}
		p.inbounds = items
		delete(top, "inbounds")
	}

	if v, ok := top["outbounds"]; ok {
		items, err := parseItems(v, "tag", "outbound", "")
		if err != nil {
			return nil, err
		}
		p.outbounds = items
		delete(top, "outbounds")
	}

	if v, ok := top["routing"]; ok {
		var routing map[string]json.RawMessage
		if err := json.Unmarshal(v, &routing); err != nil {
			return nil, err
		}
		if r, ok := routing["rules"]; ok {
			items, err := parseItems(r, "ruleTag", "rule", "masir-rule-")
			if err != nil {
				return nil, err
			}
			p.rules = items
			delete(routing, "rules")
		}
		// The fallback balancer can't be changed through Xray's API (it can only be
		// added, and never removed), so it rides along with the profile but outside the
		// restart hash: a mode change retargets it instead of rebuilding the config.
		if b, ok := routing["balancers"]; ok {
			p.balancers = b
			p.fallback = parseFallback(b)
			delete(routing, "balancers")
		}
		top["routing"] = marshal(routing)
	}

	sum := sha256.Sum256(marshal(top)) // map keys marshal sorted, so this is stable
	p.hash = hex.EncodeToString(sum[:])
	return p, nil
}

// parseFallback reads the balancer compose builds: one balancer whose selector is the
// outbound the catch-all currently takes. Nothing else in the config is balanced.
func parseFallback(raw json.RawMessage) fallback {
	var list []struct {
		Tag      string   `json:"tag"`
		Selector []string `json:"selector"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fallback{}
	}
	for _, b := range list {
		if b.Tag != "" && len(b.Selector) > 0 {
			return fallback{balancer: b.Tag, target: b.Selector[0]}
		}
	}
	return fallback{}
}

// parseItems decodes a JSON array of objects and makes sure each has a unique tag.
// Rules from subscriptions usually have no ruleTag, and we need one to remove them later.
func parseItems(raw json.RawMessage, tagKey, what, autoPrefix string) ([]item, error) {
	var objs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &objs); err != nil {
		return nil, err
	}
	out := make([]item, 0, len(objs))
	seen := map[string]bool{}
	for i, o := range objs {
		var tag string
		if t, ok := o[tagKey]; ok {
			_ = json.Unmarshal(t, &tag)
		}
		if tag == "" {
			if autoPrefix == "" {
				return nil, fmt.Errorf("%s #%d has no %q", what, i, tagKey)
			}
			tag = fmt.Sprintf("%s%d", autoPrefix, i)
			o[tagKey] = marshal(tag)
		}
		if seen[tag] {
			return nil, fmt.Errorf("duplicate %s tag %q", what, tag)
		}
		seen[tag] = true
		out = append(out, item{Tag: tag, JSON: marshal(o)})
	}
	return out, nil
}

// needsRestart: the base changed, or the first outbound (Xray's fallback) changed.
func (p *Profile) needsRestart(old *Profile) bool {
	return old == nil || p.hash != old.hash || firstTag(p.outbounds) != firstTag(old.outbounds)
}

// bootstrap renders the full config Xray starts with: the profile plus our control channel.
func (p *Profile) bootstrap(apiPort int) ([]byte, error) {
	top := make(map[string]json.RawMessage, len(p.base)+4)
	for k, v := range p.base {
		top[k] = v
	}

	top["api"] = json.RawMessage(`{"tag":"api","services":["HandlerService","RoutingService","StatsService"]}`)
	if _, ok := top["stats"]; !ok {
		top["stats"] = json.RawMessage(`{}`)
	}

	inbounds := append(rawItems(p.inbounds), json.RawMessage(fmt.Sprintf(
		`{"tag":%q,"listen":"127.0.0.1","port":%d,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}}`,
		apiInboundTag, apiPort)))
	top["inbounds"] = marshal(inbounds)

	// Config order matters: the first outbound is the fallback when no rule matches.
	top["outbounds"] = marshal(rawItems(p.outbounds))

	// The API rule must stay first, or a catch-all user rule locks the daemon out.
	routing := map[string]json.RawMessage{}
	if v, ok := top["routing"]; ok {
		if err := json.Unmarshal(v, &routing); err != nil {
			return nil, err
		}
	}
	rules := []json.RawMessage{json.RawMessage(apiRule)}
	for _, r := range p.rules {
		rules = append(rules, r.JSON)
	}
	routing["rules"] = marshal(rules)
	if p.balancers != nil {
		routing["balancers"] = p.balancers
	}
	top["routing"] = marshal(routing)

	return json.Marshal(top)
}

// Marshal renders the profile as the user's xray.json: no API inbound, rule or service.
// ParseProfile(p.Marshal()) yields an equal profile, so writing it back never forces a restart.
func (p *Profile) Marshal() ([]byte, error) {
	top := make(map[string]json.RawMessage, len(p.base)+2)
	for k, v := range p.base {
		top[k] = v
	}
	if len(p.inbounds) > 0 {
		top["inbounds"] = marshal(rawItems(p.inbounds))
	}
	if len(p.outbounds) > 0 {
		top["outbounds"] = marshal(rawItems(p.outbounds))
	}
	if v, ok := top["routing"]; ok || len(p.rules) > 0 || p.balancers != nil {
		routing := map[string]json.RawMessage{}
		if ok {
			if err := json.Unmarshal(v, &routing); err != nil {
				return nil, err
			}
		}
		routing["rules"] = marshal(rawItems(p.rules))
		if p.balancers != nil {
			routing["balancers"] = p.balancers
		}
		top["routing"] = marshal(routing)
	}
	return json.MarshalIndent(top, "", "  ")
}

func rawItems(items []item) []json.RawMessage {
	out := make([]json.RawMessage, len(items))
	for i, it := range items {
		out[i] = it.JSON
	}
	return out
}

// newItem forces tag into the object's tagKey, so the request's tag and the JSON never disagree.
func newItem(raw []byte, tagKey, tag string) (item, error) {
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return item{}, fmt.Errorf("not a JSON object: %w", err)
	}
	if tag == "" {
		if t, ok := o[tagKey]; ok {
			_ = json.Unmarshal(t, &tag)
		}
	}
	if tag == "" {
		return item{}, fmt.Errorf("%q is required", tagKey)
	}
	o[tagKey] = marshal(tag)
	return item{Tag: tag, JSON: marshal(o)}, nil
}

func firstTag(items []item) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Tag
}

func marshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b } // inputs are already-valid JSON
