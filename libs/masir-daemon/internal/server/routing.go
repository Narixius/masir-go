package server

import (
	"context"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func (s *Server) ListRoutingRules(ctx context.Context, _ *pb.ListRoutingRulesRequest) (*pb.ListRoutingRulesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*pb.RoutingRule, len(s.resolved))
	for i, rr := range s.resolved {
		out[i] = &pb.RoutingRule{
			RuleTag:             rr.Rule.Tag,
			RuleJson:            string(rr.Rule.Match),
			Target:              rr.Rule.Target.String(),
			ResolvedOutboundTag: rr.OutboundTag,
			FellBack:            rr.FellBack,
		}
	}
	return &pb.ListRoutingRulesResponse{Rules: out}, nil
}

func (s *Server) AddRouting(ctx context.Context, req *pb.AddRoutingRequest) (*pb.AddRoutingResponse, error) {
	t, err := s.parseTarget(req.Target)
	if err != nil {
		return nil, err
	}
	err = s.change(ctx, "rule added: "+req.RuleTag, func(st *store.State) error {
		return st.AddRule(req.RuleTag, []byte(req.RuleJson), t, req.Prepend)
	})
	if err != nil {
		return nil, err
	}
	s.track("rule_added", map[string]any{
		"matchers":    matcherKinds(req.RuleJson),
		"target_kind": targetKind(req.Target),
		"prepend":     req.Prepend,
	})
	return &pb.AddRoutingResponse{}, nil
}

func (s *Server) RemoveRouting(ctx context.Context, req *pb.RemoveRoutingRequest) (*pb.RemoveRoutingResponse, error) {
	err := s.change(ctx, "rule removed: "+req.RuleTag, func(st *store.State) error {
		return st.RemoveRule(req.RuleTag)
	})
	if err != nil {
		return nil, err
	}
	s.track("rule_removed", map[string]any{})
	return &pb.RemoveRoutingResponse{}, nil
}

func (s *Server) AlterRouting(ctx context.Context, req *pb.AlterRoutingRequest) (*pb.AlterRoutingResponse, error) {
	var target *store.Target
	if req.Target != nil {
		t, err := s.parseTarget(*req.Target)
		if err != nil {
			return nil, err
		}
		target = &t
	}
	var match []byte
	if req.RuleJson != nil {
		match = []byte(*req.RuleJson)
	}
	err := s.change(ctx, "rule altered: "+req.RuleTag, func(st *store.State) error {
		if err := st.AlterRule(req.RuleTag, match, target); err != nil {
			return err
		}
		if req.NewRuleTag != nil {
			return st.RenameRule(req.RuleTag, *req.NewRuleTag)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	props := map[string]any{"renamed": req.NewRuleTag != nil}
	if req.RuleJson != nil {
		props["matchers"] = matcherKinds(*req.RuleJson)
	}
	if req.Target != nil {
		props["target_kind"] = targetKind(*req.Target)
	}
	s.track("rule_altered", props)
	return &pb.AlterRoutingResponse{}, nil
}

func (s *Server) MoveRouting(ctx context.Context, req *pb.MoveRoutingRequest) (*pb.MoveRoutingResponse, error) {
	err := s.change(ctx, "rule moved: "+req.RuleTag, func(st *store.State) error {
		return st.MoveRule(req.RuleTag, int(req.Index))
	})
	if err != nil {
		return nil, err
	}
	s.track("rule_moved", map[string]any{"index": req.Index})
	return &pb.MoveRoutingResponse{}, nil
}

// parseTarget also checks that a pinned config exists, and records its name so the
// pin can follow the config when its provider rotates the server's settings.
func (s *Server) parseTarget(raw string) (store.Target, error) {
	t, err := store.ParseTarget(raw)
	if err != nil {
		return store.Target{}, toStatus(err)
	}
	if t.Kind == store.TargetConfig {
		c, err := s.subs.Config(t.SubscriptionID, t.ConfigID)
		if err != nil {
			return store.Target{}, toStatus(err)
		}
		t.Name = c.Name
	}
	return t, nil
}

func (s *Server) TestRouting(ctx context.Context, req *pb.TestRoutingRequest) (*pb.TestRoutingResponse, error) {
	res, err := s.xray.TestRoute(ctx, xray.RouteQuery{
		Domain:     req.Domain,
		IP:         req.Ip,
		Port:       req.Port,
		Network:    req.Network,
		InboundTag: req.InboundTag,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.TestRoutingResponse{OutboundTag: res.OutboundTag, Matched: res.Matched}, nil
}
