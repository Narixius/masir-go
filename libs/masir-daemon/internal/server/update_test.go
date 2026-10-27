package server

import (
	"context"
	"testing"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/update"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// stubRelease makes the daemon's GitHub check answer without a network.
func stubRelease(t *testing.T, calls *int) {
	t.Helper()
	old := latestRelease
	latestRelease = func(context.Context, update.Clients, string) (*update.Release, error) {
		(*calls)++
		return &update.Release{Tag: "v9.9.9", URL: "https://example.test/release", Published: time.Now()}, nil
	}
	t.Cleanup(func() { latestRelease = old })
}

func TestUpdateCheckIsCachedForTwoDaysAndForcedOnDemand(t *testing.T) {
	s := newTestServer(t)
	s.upd = &updates{}
	calls := 0
	stubRelease(t, &calls)

	first, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || first.GetRelease().GetLatestVersion() != "v9.9.9" {
		t.Fatalf("first check: calls=%d release=%v", calls, first.GetRelease())
	}
	if _, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("a cached check asked GitHub %d times", calls)
	}
	if _, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{Force: true}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("a forced check asked GitHub %d times", calls)
	}
}

func TestIgnoringAnUpdateLastsUntilAFreshCheck(t *testing.T) {
	was := Version
	Version = "0.1.0"
	t.Cleanup(func() { Version = was })
	s := newTestServer(t)
	s.upd = &updates{}
	calls := 0
	stubRelease(t, &calls)
	res, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetRelease().GetAvailable() {
		t.Fatalf("release isn't available: %v", res.GetRelease())
	}

	ignored, err := s.IgnoreUpdate(context.Background(), &pb.IgnoreUpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if ignored.GetRelease().GetIgnoredVersion() != "v9.9.9" {
		t.Fatalf("ignored version = %q", ignored.GetRelease().GetIgnoredVersion())
	}
	// The update remains available for the CLI; only the prompt is hidden.
	if !ignored.GetRelease().GetAvailable() {
		t.Fatal("ignoring made the update unavailable")
	}

	again, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if again.GetRelease().GetIgnoredVersion() != "v9.9.9" {
		t.Fatalf("a cached check cleared the ignore: %q", again.GetRelease().GetIgnoredVersion())
	}

	fresh, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetRelease().GetIgnoredVersion() != "" {
		t.Fatalf("a fresh check kept the ignore: %q", fresh.GetRelease().GetIgnoredVersion())
	}
}

func TestIgnoreNeedsAReleaseCheck(t *testing.T) {
	s := newTestServer(t)
	s.upd = &updates{}
	if _, err := s.IgnoreUpdate(context.Background(), &pb.IgnoreUpdateRequest{}); err == nil {
		t.Fatal("an ignore before any check was accepted")
	}
}
