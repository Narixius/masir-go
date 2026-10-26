package main

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// Every change on the main page shows at once, before the daemon has answered: the person
// sees what they did, and if the daemon refuses, it goes back and a toast says why. An
// opt is one such change waiting to be confirmed.

// holdAfter is how long a change that the daemon accepted stays shown on its own, for the
// status that carries it to arrive.
const holdAfter = 1500 * time.Millisecond

// opt is a value shown in place of the daemon's, from the moment it is chosen until the
// daemon's own status has caught up.
type opt[T any] struct {
	on    bool
	v     T
	until time.Time // zero while the call is in flight
}

func (o *opt[T]) set(v T) { o.on, o.v, o.until = true, v, time.Time{} }

// done is the daemon accepting it: it is held a moment more, until the status shows it.
func (o *opt[T]) done() { o.until = time.Now().Add(holdAfter) }

// revert is the daemon refusing it: the daemon's value shows again.
func (o *opt[T]) revert() { o.on = false }

// get returns the value to show in place of the daemon's, if there is one.
func (o *opt[T]) get() (T, bool) {
	var zero T
	if !o.on {
		return zero, false
	}
	if !o.until.IsZero() && time.Now().After(o.until) {
		o.on = false
		return zero, false
	}
	return o.v, true
}

// caughtUp drops the value once the daemon has accepted it and said something since.
func (o *opt[T]) caughtUp() {
	if o.on && !o.until.IsZero() {
		o.on = false
	}
}

// pending is everything shown ahead of the daemon.
type pending struct {
	fallback     opt[pb.Fallback]
	rulesEnabled opt[bool]
	active       opt[serverKey]
	alwaysOn     opt[bool]
	allowLAN     opt[bool]
	ruleEnabled  map[string]*opt[bool]
	ruleRemoved  map[string]*opt[bool]
}

func newPending() pending {
	return pending{ruleEnabled: map[string]*opt[bool]{}, ruleRemoved: map[string]*opt[bool]{}}
}

func perRule(m map[string]*opt[bool], tag string) *opt[bool] {
	o := m[tag]
	if o == nil {
		o = &opt[bool]{}
		m[tag] = o
	}
	return o
}

// caughtUp is called when the daemon says something new.
func (p *pending) caughtUp() {
	p.fallback.caughtUp()
	p.rulesEnabled.caughtUp()
	p.active.caughtUp()
	p.alwaysOn.caughtUp()
	p.allowLAN.caughtUp()
	for _, o := range p.ruleEnabled {
		o.caughtUp()
	}
	for _, o := range p.ruleRemoved {
		o.caughtUp()
	}
}

// ---- what the screen shows ----

func (a *app) fallback() pb.Fallback {
	if v, ok := a.pend.fallback.get(); ok {
		return v
	}
	return a.status.GetSettings().GetFallback()
}

func (a *app) rulesOn() bool {
	if v, ok := a.pend.rulesEnabled.get(); ok {
		return v
	}
	// A daemon from before the rules switch reports no revision and cannot turn rules off.
	return a.status == nil || a.status.Revision == 0 || a.status.Settings.GetRulesEnabled()
}

func (a *app) alwaysOnProxy() bool {
	if v, ok := a.pend.alwaysOn.get(); ok {
		return v
	}
	return a.status.GetSettings().GetAlwaysOnProxy()
}

func (a *app) allowLAN() bool {
	if v, ok := a.pend.allowLAN.get(); ok {
		return v
	}
	return a.status.GetSettings().GetAllowLan()
}

// activeKey is the selected server, as shown.
func (a *app) activeKey() (serverKey, bool) {
	if v, ok := a.pend.active.get(); ok {
		return v, true
	}
	st := a.status
	if st == nil || st.ActiveConfigId == "" {
		return serverKey{}, false
	}
	return serverKey{st.ActiveSubscriptionId, st.ActiveConfigId}, true
}

// ruleList is the rules as shown: the daemon's, less those being removed, with those
// being switched on or off showing it already.
func (a *app) ruleList() []*pb.RoutingRule {
	out := make([]*pb.RoutingRule, 0, len(a.rules))
	for _, r := range a.rules {
		if o := a.pend.ruleRemoved[r.RuleTag]; o != nil {
			if gone, ok := o.get(); ok && gone {
				continue
			}
		}
		if o := a.pend.ruleEnabled[r.RuleTag]; o != nil {
			if on, ok := o.get(); ok && r.Disabled == on {
				c := proto.Clone(r).(*pb.RoutingRule) // a copy: the daemon's rule is not touched
				c.Disabled = !on
				r = c
			}
		}
		out = append(out, r)
	}
	return out
}

// ---- changes ----

// mutate asks the daemon for a change that already shows. If the daemon accepts it, the
// change is held until the status carries it; if not, revert puts the screen back.
func (a *app) mutate(failPrefix string, settle, revert func(), call func(ctx context.Context) error) {
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		err := call(ctx)
		a.post(func() {
			if err != nil {
				revert()
				a.fail(failPrefix, err)
				return
			}
			settle()
			a.refreshLists()
		})
	})
}

func (a *app) setFallback(f pb.Fallback) {
	a.pend.fallback.set(f)
	a.mutate("Couldn't change the mode: ", a.pend.fallback.done, a.pend.fallback.revert, func(ctx context.Context) error {
		_, err := a.d.SetFallback(ctx, &pb.SetFallbackRequest{Fallback: f})
		return err
	})
}

func (a *app) selectServer(s server) {
	a.open = sectionNone
	a.pend.active.set(s.key)
	a.mutate("Couldn't select that server: ", a.pend.active.done, a.pend.active.revert, func(ctx context.Context) error {
		_, err := a.d.SelectConfig(ctx, &pb.SelectConfigRequest{SubscriptionId: s.key.sub, ConfigId: s.key.cfg, Reason: "interactive"})
		return err
	})
}

func (a *app) setRulesEnabled(on bool) {
	a.pend.rulesEnabled.set(on)
	a.mutate("Couldn't change the rules: ", a.pend.rulesEnabled.done, a.pend.rulesEnabled.revert, func(ctx context.Context) error {
		_, err := a.d.UpdateSettings(ctx, &pb.UpdateSettingsRequest{RulesEnabled: &on})
		return err
	})
}

func (a *app) setRuleEnabled(tag string, on bool) {
	o := perRule(a.pend.ruleEnabled, tag)
	o.set(on)
	a.mutate("Couldn't change that rule: ", o.done, o.revert, func(ctx context.Context) error {
		_, err := a.d.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: []string{tag}, Enabled: on})
		return err
	})
}

func (a *app) removeRule(tag string) {
	o := perRule(a.pend.ruleRemoved, tag)
	o.set(true)
	delete(a.swipes, tag)
	a.mutate("Couldn't remove that rule: ", o.done, o.revert, func(ctx context.Context) error {
		_, err := a.d.RemoveRouting(ctx, &pb.RemoveRoutingRequest{RuleTag: tag})
		return err
	})
}

func (a *app) setAlwaysOnProxy(on bool) {
	a.pend.alwaysOn.set(on)
	a.mutate("Couldn't save the setting: ", a.pend.alwaysOn.done, a.pend.alwaysOn.revert, func(ctx context.Context) error {
		_, err := a.d.UpdateSettings(ctx, &pb.UpdateSettingsRequest{AlwaysOnProxy: &on})
		return err
	})
}

func (a *app) setAllowLAN(on bool) {
	a.pend.allowLAN.set(on)
	a.mutate("Couldn't save the setting: ", a.pend.allowLAN.done, a.pend.allowLAN.revert, func(ctx context.Context) error {
		_, err := a.d.UpdateSettings(ctx, &pb.UpdateSettingsRequest{AllowLan: &on})
		return err
	})
}
