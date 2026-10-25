package server

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/geodata"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func (s *Server) ListGeoCategories(_ context.Context, req *pb.ListGeoCategoriesRequest) (*pb.ListGeoCategoriesResponse, error) {
	var file string
	switch req.Kind {
	case pb.GeoKind_GEO_KIND_SITE:
		file = "geosite.dat"
	case pb.GeoKind_GEO_KIND_IP:
		file = "geoip.dat"
	default:
		return nil, status.Error(codes.InvalidArgument, "kind must be site or ip")
	}
	cats, err := geodata.Categories(filepath.Join(s.xray.AssetDir(), file))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, status.Errorf(codes.FailedPrecondition, "%s isn't installed; `masir update geo` downloads it", file)
	case err != nil:
		return nil, status.Errorf(codes.Internal, "read %s: %v", file, err)
	}
	out := make([]*pb.GeoCategory, len(cats))
	for i, c := range cats {
		out[i] = &pb.GeoCategory{Code: c.Code, Entries: uint32(c.Entries)}
	}
	return &pb.ListGeoCategoriesResponse{Categories: out}, nil
}
