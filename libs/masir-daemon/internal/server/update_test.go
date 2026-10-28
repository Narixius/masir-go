package server

import (
	"context"
	"testing"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/update"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// stubRelease makes the daemon's GitHub check answer without a network. It serves the
// version in tag, answers 304 while the tag is unchanged and the caller sent it back, and
// counts the checks that reached GitHub.
func stubRelease(t *testing.T, calls *int, tag *string) {
	t.Helper()
	old := latestRelease
	latestRelease = func(_ context.Context, _ update.Clients, _ string, etag string) (*update.Release, string, bool, error) {
		(*calls)++
		if etag != "" && etag == *tag {
			return nil, etag, true, nil
		}
		return &update.Release{Tag: *tag, URL: "https://example.test/release", Published: time.Now()}, *tag, false, nil
	}
	t.Cleanup(func() { latestRelease = old })
}

func TestUpdateCheckIsCachedForOneDaysAndForcedOnDemand(t *testing.T) {
	s := newTestServer(t)
	s.upd = &updates{}
	calls := 0
	tag := "v9.9.9"
	stubRelease(t, &calls, &tag)

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

// A UI that opens its panel wants an answer newer than a day, and says so. The daemon
// refreshes then, conditionally: an unchanged release is a 304 and only its check time
// moves. No client can make the daemon trust a release more than a day old.
func TestUpdateCheckHonoursTheCallersMaxAge(t *testing.T) {
	s := newTestServer(t)
	s.upd = &updates{}
	calls := 0
	tag := "v9.9.9"
	stubRelease(t, &calls, &tag)

	if _, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{MaxAgeMs: (10 * time.Minute).Milliseconds()}); err != nil {
		t.Fatal(err)
	}
	checkedAt := s.upd.release.CheckedAtMs
	if calls != 1 {
		t.Fatalf("the first check asked GitHub %d times", calls)
	}
	// Inside the ten minutes the caller accepts: no request at all.
	if _, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{MaxAgeMs: (10 * time.Minute).Milliseconds()}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("a check inside the caller's max age asked GitHub %d times", calls)
	}
	// A caller that wants an answer fresher than the cache: asked, and unchanged, so the
	// release stays and only when it was confirmed moves.
	s.upd.release.CheckedAtMs = time.Now().Add(-time.Minute).UnixMilli()
	stale := s.upd.release.CheckedAtMs
	again, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{MaxAgeMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("a stale-by-request check asked GitHub %d times", calls)
	}
	if again.GetRelease().GetCheckedAtMs() <= stale || again.GetRelease().GetCheckedAtMs() < checkedAt {
		t.Error("a confirmed release must report when it was confirmed")
	}
	if again.GetRelease().GetLatestVersion() != "v9.9.9" || again.GetRelease().GetIgnoredVersion() != "" {
		t.Fatalf("a not-modified check changed the release: %v", again.GetRelease())
	}

	// Even a caller asking to accept a week-old answer gets a day, as documented.
	s.upd.release.CheckedAtMs = time.Now().Add(-25 * time.Hour).UnixMilli()
	if _, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{MaxAgeMs: (7 * 24 * time.Hour).Milliseconds()}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("a week-long max age skipped the daily refresh: %d calls", calls)
	}
}

func TestIgnoringAnUpdateLastsUntilANewerRelease(t *testing.T) {
	was := Version
	Version = "0.1.0"
	t.Cleanup(func() { Version = was })
	s := newTestServer(t)
	s.upd = &updates{}
	calls := 0
	tag := "v9.9.9"
	stubRelease(t, &calls, &tag)
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

	// A UI checking every few minutes must not bring an ignored prompt back: a fresh
	// check that finds the same release keeps it hidden.
	fresh, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetRelease().GetIgnoredVersion() != "v9.9.9" {
		t.Fatalf("a fresh check of the same release cleared the ignore: %q", fresh.GetRelease().GetIgnoredVersion())
	}

	// A newer release is news again.
	tag = "v9.9.10"
	newer, err := s.CheckUpdate(context.Background(), &pb.CheckUpdateRequest{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if newer.GetRelease().GetIgnoredVersion() != "" {
		t.Fatalf("a newer release kept the old ignore: %q", newer.GetRelease().GetIgnoredVersion())
	}
	if !newer.GetRelease().GetAvailable() || newer.GetRelease().GetLatestVersion() != "v9.9.10" {
		t.Fatalf("newer release = %v", newer.GetRelease())
	}
}

func TestIgnoreNeedsAReleaseCheck(t *testing.T) {
	s := newTestServer(t)
	s.upd = &updates{}
	if _, err := s.IgnoreUpdate(context.Background(), &pb.IgnoreUpdateRequest{}); err == nil {
		t.Fatal("an ignore before any check was accepted")
	}
}
