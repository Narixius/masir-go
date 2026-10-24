package server

import (
	"context"

	"github.com/narixius/masir-go/apps/masird/internal/store"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func (s *Server) ListInbounds(ctx context.Context, _ *pb.ListInboundsRequest) (*pb.ListInboundsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListInboundsResponse{Inbounds: toPBItems(s.state.Inbounds)}, nil
}

func (s *Server) AddInbound(ctx context.Context, req *pb.AddInboundRequest) (*pb.AddInboundResponse, error) {
	err := s.change(ctx, "inbound added: "+req.Tag, func(st *store.State) error {
		return st.AddInbound(req.Tag, []byte(req.InboundJson))
	})
	if err != nil {
		return nil, err
	}
	return &pb.AddInboundResponse{}, nil
}

func (s *Server) RemoveInbound(ctx context.Context, req *pb.RemoveInboundRequest) (*pb.RemoveInboundResponse, error) {
	err := s.change(ctx, "inbound removed: "+req.Tag, func(st *store.State) error {
		return st.RemoveInbound(req.Tag)
	})
	if err != nil {
		return nil, err
	}
	return &pb.RemoveInboundResponse{}, nil
}

func (s *Server) AlterInbound(ctx context.Context, req *pb.AlterInboundRequest) (*pb.AlterInboundResponse, error) {
	err := s.change(ctx, "inbound altered: "+req.Tag, func(st *store.State) error {
		return st.AlterInbound(req.Tag, []byte(req.InboundJson))
	})
	if err != nil {
		return nil, err
	}
	return &pb.AlterInboundResponse{}, nil
}

func toPBItems(items []store.Item) []*pb.TaggedConfig {
	out := make([]*pb.TaggedConfig, len(items))
	for i, it := range items {
		out[i] = &pb.TaggedConfig{Tag: it.Tag, Json: string(it.Config)}
	}
	return out
}
