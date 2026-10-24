package server

import (
	"context"

	"github.com/narixius/masir-go/apps/masird/internal/store"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func (s *Server) ListOutbounds(ctx context.Context, _ *pb.ListOutboundsRequest) (*pb.ListOutboundsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListOutboundsResponse{Outbounds: toPBItems(s.state.Outbounds)}, nil
}

func (s *Server) AddOutbound(ctx context.Context, req *pb.AddOutboundRequest) (*pb.AddOutboundResponse, error) {
	err := s.change(ctx, "outbound added: "+req.Tag, func(st *store.State) error {
		return st.AddOutbound(req.Tag, []byte(req.OutboundJson))
	})
	if err != nil {
		return nil, err
	}
	return &pb.AddOutboundResponse{}, nil
}

func (s *Server) RemoveOutbound(ctx context.Context, req *pb.RemoveOutboundRequest) (*pb.RemoveOutboundResponse, error) {
	err := s.change(ctx, "outbound removed: "+req.Tag, func(st *store.State) error {
		return st.RemoveOutbound(req.Tag)
	})
	if err != nil {
		return nil, err
	}
	return &pb.RemoveOutboundResponse{}, nil
}

func (s *Server) AlterOutbound(ctx context.Context, req *pb.AlterOutboundRequest) (*pb.AlterOutboundResponse, error) {
	err := s.change(ctx, "outbound altered: "+req.Tag, func(st *store.State) error {
		return st.AlterOutbound(req.Tag, []byte(req.OutboundJson))
	})
	if err != nil {
		return nil, err
	}
	return &pb.AlterOutboundResponse{}, nil
}
