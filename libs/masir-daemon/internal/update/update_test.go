package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// The release workflow names the archives (.goreleaser.yaml); the updater has to ask for
// exactly those, so a tag's downloads are found.
func TestArchiveName(t *testing.T) {
	for _, c := range []struct{ tag, goos, goarch, want string }{
		{"v0.3.1", "darwin", "arm64", "masir-cli-darwin-0.3.1-arm64.tar.gz"},
		{"v0.3.1", "darwin", "amd64", "masir-cli-darwin-0.3.1-amd64.tar.gz"},
		{"v0.3.1", "linux", "amd64", "masir-cli-linux-0.3.1-amd64.tar.gz"},
		{"0.4.0", "windows", "arm64", "masir-cli-windows-0.4.0-arm64.zip"},
		{"v1.0.0-rc1", "linux", "arm64", "masir-cli-linux-1.0.0-rc1-arm64.tar.gz"},
	} {
		if got := ArchiveName(c.tag, c.goos, c.goarch); got != c.want {
			t.Errorf("ArchiveName(%q, %q, %q) = %q, want %q", c.tag, c.goos, c.goarch, got, c.want)
		}
	}
}

// fakeGitHub serves releases and their files. A file served slowly lets tests pause
// mid-download; ranges counts Range requests, which is how a resume shows.
type fakeGitHub struct {
	srv      *httptest.Server
	releases map[string]Release // repo -> latest
	files    map[string][]byte  // name -> content
	slow     map[string]bool
	ranges   atomic.Int32
	fileHits atomic.Int32
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{releases: map[string]Release{}, files: map[string][]byte{}, slow: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if repo, ok := strings.CutPrefix(r.URL.Path, "/repos/"); ok {
			rel, found := f.releases[strings.TrimSuffix(repo, "/releases/latest")]
			if !found {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(rel)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		body, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.fileHits.Add(1)
		if r.Header.Get("Range") != "" {
			f.ranges.Add(1)
		}
		var rs io.ReadSeeker = bytes.NewReader(body)
		if f.slow[name] {
			rs = &slowReader{bytes.NewReader(body)}
		}
		http.ServeContent(w, r, name, time.Time{}, rs)
	}))
	t.Cleanup(f.srv.Close)
	apiBase = f.srv.URL
	t.Cleanup(func() { apiBase = "https://api.github.com" })
	return f
}

// publish adds a release to repo with these files, each with a URL on the fake server.
func (f *fakeGitHub) publish(repo, tag string, files map[string][]byte) {
	rel := Release{Tag: tag, URL: f.srv.URL + "/release/" + tag, Published: time.Now()}
	for name, body := range files {
		f.files[name] = body
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: f.srv.URL + "/dl/" + name, Size: int64(len(body))})
	}
	f.releases[repo] = rel
}

type slowReader struct{ *bytes.Reader }

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(20 * time.Millisecond)
	return s.Reader.Read(p[:min(len(p), 1024)])
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func archive(t *testing.T, name string, files map[string]string) []byte {
	var buf bytes.Buffer
	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)
		for n, body := range files {
			w, _ := zw.Create("masir/" + n)
			w.Write([]byte(body))
		}
		zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, body := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func clients() Clients { return Clients{Direct: http.DefaultClient} }

// wait runs until the runner's job reaches a final or paused state.
func wait(t *testing.T, r *Runner) *pb.UpdateJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j := r.Snapshot()
		if Finished(j.State) || j.State == pb.UpdateState_UPDATE_STATE_PAUSED {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job didn't finish")
	return nil
}

func TestMasirUpdateInstallsAndRestarts(t *testing.T) {
	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir", "geosite.dat": "x"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{
		name:            arc,
		"checksums.txt": []byte(fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", sum(arc), name, strings.Repeat("0", 64))),
	})

	bin := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(bin, []byte("old masir"), 0o755)
	var restarted atomic.Bool
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	svcBin := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(svcBin, []byte("old service masir"), 0o755)
	clientBin := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(clientBin, []byte("old client masir"), 0o755)
	task := Masir{Clients: clients(), Current: "0.1.0", Binary: bin, ServiceBinary: svcBin, ClientBinary: clientBin, Privileged: true, WorkDir: r.workDir, Restart: func() { restarted.Store(true) }}
	if _, err := r.Start(task.Run); err != nil {
		t.Fatal(err)
	}
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_RESTARTING || job.Error != "" {
		t.Fatalf("job = %v (%s)", job.State, job.Error)
	}
	if got, _ := os.ReadFile(bin); string(got) != "new masir" {
		t.Errorf("the running binary = %q, want the new one", got)
	}
	if got, _ := os.ReadFile(svcBin); string(got) != "new masir" {
		t.Errorf("the service's own copy = %q, want the new one too", got)
	}
	if got, _ := os.ReadFile(clientBin); string(got) != "new masir" {
		t.Errorf("the CLI's own copy = %q, want the new one too", got)
	}
	if fi, _ := os.Stat(bin); fi.Mode().Perm()&0o100 == 0 && runtime.GOOS != "windows" {
		t.Error("the new binary must be executable")
	}
	if !restarted.Load() || job.Version != "v0.2.0" || job.BytesDone != int64(len(arc)) {
		t.Errorf("restarted=%v version=%q bytes=%d/%d", restarted.Load(), job.Version, job.BytesDone, len(arc))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "geosite.dat")); err == nil {
		t.Error("geo data is the geo job's business, not the masir update's")
	}
	if staged, _ := os.ReadFile(filepath.Join(job.StagedDir, exe("masir"))); string(staged) != "new masir" {
		t.Error("the staged CLI is where the job says")
	}

	// Already on the latest: nothing happens.
	task.Current = "v0.2.0"
	r.Start(task.Run)
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_UP_TO_DATE {
		t.Errorf("same version: %v", job.State)
	}
	// A development build refuses unless forced.
	task.Current = "dev"
	r.Start(task.Run)
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "development build") {
		t.Errorf("dev build: %v %q", job.State, job.Error)
	}
}

func TestMasirUpdateRejectsATamperedArchive(t *testing.T) {
	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "x"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{name: arc, "checksums.txt": []byte(strings.Repeat("a", 64) + "  " + name)})

	bin := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(bin, []byte("old masir"), 0o755)
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	r.Start(Masir{Clients: clients(), Current: "0.1.0", Binary: bin, WorkDir: r.workDir}.Run)
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "checksum") {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	if got, _ := os.ReadFile(bin); string(got) != "old masir" {
		t.Error("a failed verification must leave the installed binary alone")
	}
}

func TestPauseResumeStop(t *testing.T) {
	gh := newFakeGitHub(t)
	geosite := bytes.Repeat([]byte("s"), 64<<10)
	geoip := bytes.Repeat([]byte("i"), 8<<10)
	gh.publish(GeoRepo, "20261024", map[string][]byte{
		"geosite.dat": geosite, "geosite.dat.sha256sum": []byte(sum(geosite) + "  geosite.dat\n"),
		"geoip.dat": geoip, "geoip.dat.sha256sum": []byte(sum(geoip) + "  geoip.dat\n"),
	})
	gh.slow["geosite.dat"] = true

	assets := t.TempDir()
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, t.TempDir())
	var reloads atomic.Int32
	task := Geo{Clients: clients(), AssetDir: assets, WorkDir: r.workDir, Reload: func() error { reloads.Add(1); return nil }}
	r.Start(task.Run)

	waitFor := func(what string, ok func(*pb.UpdateJob) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok(r.Snapshot()) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %+v", what, r.Snapshot())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("some bytes", func(j *pb.UpdateJob) bool { return j.BytesDone > 4<<10 })

	job, err := r.Pause()
	if err != nil || job.State != pb.UpdateState_UPDATE_STATE_PAUSED {
		t.Fatalf("pause: %v %v", job.GetState(), err)
	}
	part := filepath.Join(r.workDir, "20261024", "geosite.dat")
	kept := fileSize(part)
	if kept == 0 || kept >= int64(len(geosite)) || job.BytesDone != kept {
		t.Fatalf("paused with %d bytes on disk, job says %d", kept, job.BytesDone)
	}
	if _, err := r.Pause(); err != nil {
		t.Error("pausing a paused job is a no-op, not an error")
	}

	// Resume continues from the partial file with a Range request.
	gh.slow["geosite.dat"] = false
	if _, err := r.Resume(); err != nil {
		t.Fatal(err)
	}
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_DONE {
		t.Fatalf("after resume: %v %q", job.State, job.Error)
	}
	if gh.ranges.Load() == 0 {
		t.Error("resume should ask for the rest with a Range request")
	}
	if got, _ := os.ReadFile(filepath.Join(assets, "geosite.dat")); !bytes.Equal(got, geosite) {
		t.Error("geosite.dat should be complete and correct after resuming")
	}
	if got, _ := os.ReadFile(filepath.Join(assets, "geoip.dat")); !bytes.Equal(got, geoip) {
		t.Error("geoip.dat should be complete and correct")
	}
	if reloads.Load() != 1 {
		t.Errorf("reloads = %d", reloads.Load())
	}

	// Installed files match: up to date, nothing downloaded.
	hits := gh.fileHits.Load()
	r.Start(task.Run)
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_UP_TO_DATE {
		t.Fatalf("second run: %v", job.State)
	}
	if gh.fileHits.Load()-hits != 2 { // just the two checksum files
		t.Errorf("an up-to-date check downloaded %d files", gh.fileHits.Load()-hits)
	}

	// Stop deletes the partial download.
	gh.slow["geosite.dat"] = true
	task.Force = true
	r.Start(task.Run)
	waitFor("downloading", func(j *pb.UpdateJob) bool { return j.BytesDone > 1<<10 })
	if job, err := r.Stop(); err != nil || job.State != pb.UpdateState_UPDATE_STATE_STOPPED {
		t.Fatalf("stop: %v %v", job.GetState(), err)
	}
	if _, err := os.Stat(r.workDir); !os.IsNotExist(err) {
		t.Error("stop should delete the partial download")
	}
	if _, err := r.Resume(); !errors.Is(err, ErrNotPaused) {
		t.Errorf("resume after stop: %v", err)
	}
}

func TestGeoRollsBackWhenXrayRejectsTheData(t *testing.T) {
	gh := newFakeGitHub(t)
	geosite, geoip := []byte("new site"), []byte("new ip")
	gh.publish(GeoRepo, "20261024", map[string][]byte{
		"geosite.dat": geosite, "geosite.dat.sha256sum": []byte(sum(geosite) + "  geosite.dat"),
		"geoip.dat": geoip, "geoip.dat.sha256sum": []byte(sum(geoip)),
	})
	assets := t.TempDir()
	os.WriteFile(filepath.Join(assets, "geosite.dat"), []byte("old site"), 0o644)
	os.WriteFile(filepath.Join(assets, "geoip.dat"), []byte("old ip"), 0o644)

	calls := 0
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, t.TempDir())
	r.Start(Geo{Clients: clients(), AssetDir: assets, WorkDir: r.workDir, Reload: func() error {
		calls++
		if calls == 1 {
			return errors.New("bad data")
		}
		return nil
	}}.Run)
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "put back") {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	for f, want := range map[string]string{"geosite.dat": "old site", "geoip.dat": "old ip"} {
		if got, _ := os.ReadFile(filepath.Join(assets, f)); string(got) != want {
			t.Errorf("%s = %q, want the old data back", f, got)
		}
	}
	if calls != 2 {
		t.Errorf("Xray should reload the restored files too; reloads = %d", calls)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "0.1.0", true}, {"v0.2.0", "v0.2.0", false}, {"v0.1.9", "v0.2.0", false},
		{"v1.0.0", "dev", false}, {"v1.0.0", "v1.0.0-rc.1", true},
	} {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

func TestDownloadFallsBackToTheProxy(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "data") }))
	defer ok.Close()
	broken := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("filtered") })}
	path := filepath.Join(t.TempDir(), "f")
	c := Clients{Direct: broken, Proxy: http.DefaultClient}
	if err := download(context.Background(), c, ok.URL, path, func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "data" {
		t.Errorf("got %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A geo update that would leave enabled rules unbuildable installs nothing until the user
// agrees to turn those rules off; the verified download is kept for the second go.
func TestGeoUpdateIsBlockedByRulesTheNewDataCantBuild(t *testing.T) {
	gh := newFakeGitHub(t)
	geosite, geoip := []byte("new site"), []byte("new ip")
	gh.publish(GeoRepo, "20261024", map[string][]byte{
		"geosite.dat": geosite, "geosite.dat.sha256sum": []byte(sum(geosite) + "  geosite.dat"),
		"geoip.dat": geoip, "geoip.dat.sha256sum": []byte(sum(geoip)),
	})
	assets := t.TempDir()
	os.WriteFile(filepath.Join(assets, "geosite.dat"), []byte("old site"), 0o644)
	os.WriteFile(filepath.Join(assets, "geoip.dat"), []byte("old ip"), 0o644)
	work := t.TempDir()

	var seen []string // the files the pre-flight was shown
	var disabled []*pb.RuleProblem
	var order []string
	task := func(disable bool) Geo {
		return Geo{
			Clients: clients(), AssetDir: assets, WorkDir: filepath.Join(work, "geo"), DisableBroken: disable,
			Preflight: func(ip, site string) []*pb.RuleProblem {
				a, _ := os.ReadFile(ip)
				b, _ := os.ReadFile(site)
				seen = append(seen, string(a)+"/"+string(b))
				return []*pb.RuleProblem{{RuleTag: "iran-direct", Missing: []string{"geoip:in-ir"}}}
			},
			Disable: func(_ context.Context, p []*pb.RuleProblem) error {
				disabled = p
				order = append(order, "disable")
				return nil
			},
			Reload: func() error { order = append(order, "reload"); return nil },
		}
	}

	// Without the user's agreement: blocked, nothing installed, nothing disabled.
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, work)
	r.Start(task(false).Run)
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_BLOCKED || len(job.BlockedRules) != 1 || job.BlockedRules[0].RuleTag != "iran-direct" || job.Error != "" {
		t.Fatalf("job = %v %q %+v", job.State, job.Error, job.BlockedRules)
	}
	if len(seen) != 1 || seen[0] != "new ip/new site" {
		t.Errorf("the pre-flight should see the verified new files, saw %v", seen)
	}
	for f, want := range map[string]string{"geosite.dat": "old site", "geoip.dat": "old ip"} {
		if got, _ := os.ReadFile(filepath.Join(assets, f)); string(got) != want {
			t.Errorf("%s = %q: a blocked update must leave the installed data alone", f, got)
		}
	}
	if disabled != nil || len(order) != 0 {
		t.Errorf("a blocked update disabled rules or reloaded: %v %v", disabled, order)
	}
	if !Finished(job.State) {
		t.Error("blocked is a final state: the job is over and a new one can start")
	}

	// With it: the rules are turned off first, then the data is installed and reloaded.
	r2 := NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, work)
	r2.Start(task(true).Run)
	job = wait(t, r2)
	if job.State != pb.UpdateState_UPDATE_STATE_DONE || job.Error != "" {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	if strings.Join(order, ",") != "disable,reload" || len(disabled) != 1 {
		t.Errorf("order = %v, disabled = %v", order, disabled)
	}
	for f, want := range map[string]string{"geosite.dat": "new site", "geoip.dat": "new ip"} {
		if got, _ := os.ReadFile(filepath.Join(assets, f)); string(got) != want {
			t.Errorf("%s = %q, want the new data", f, got)
		}
	}
}

func TestGeoUpdateFailsIfTheRulesCantBeTurnedOff(t *testing.T) {
	gh := newFakeGitHub(t)
	geosite, geoip := []byte("new site"), []byte("new ip")
	gh.publish(GeoRepo, "20261024", map[string][]byte{
		"geosite.dat": geosite, "geosite.dat.sha256sum": []byte(sum(geosite)),
		"geoip.dat": geoip, "geoip.dat.sha256sum": []byte(sum(geoip)),
	})
	assets := t.TempDir()
	os.WriteFile(filepath.Join(assets, "geoip.dat"), []byte("old ip"), 0o644)
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, t.TempDir())
	r.Start(Geo{Clients: clients(), AssetDir: assets, WorkDir: r.workDir, DisableBroken: true,
		Preflight: func(string, string) []*pb.RuleProblem { return []*pb.RuleProblem{{RuleTag: "x"}} },
		Disable:   func(context.Context, []*pb.RuleProblem) error { return errors.New("disk full") },
	}.Run)
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "disk full") {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(assets, "geoip.dat")); string(got) != "old ip" {
		t.Errorf("geoip.dat = %q: nothing is installed if the rules couldn't be turned off", got)
	}
}

// The new binary is asked before anything is replaced; a refusal leaves every copy alone.
func TestMasirUpdateStopsBeforeInstallingWhenThePreflightRefuses(t *testing.T) {
	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{
		name: arc, "checksums.txt": []byte(fmt.Sprintf("%s  %s\n", sum(arc), name)),
	})
	bin := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(bin, []byte("old masir"), 0o755)
	svc := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(svc, []byte("old service masir"), 0o755)

	var asked string
	var restarted bool
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	r.Start(Masir{Clients: clients(), Current: "0.1.0", Binary: bin, ServiceBinary: svc, WorkDir: r.workDir,
		Preflight: func(_ context.Context, staged string) error {
			b, _ := os.ReadFile(staged)
			asked = string(b)
			return errors.New("not installed: the new version can't build 1 of your rules")
		},
		Restart: func() { restarted = true },
	}.Run)
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "can't build 1 of your rules") {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	if asked != "new masir" {
		t.Errorf("the pre-flight should be given the new, unpacked binary, got %q", asked)
	}
	if restarted {
		t.Error("restarted into a version that was refused")
	}
	for path, want := range map[string]string{bin: "old masir", svc: "old service masir"} {
		if got, _ := os.ReadFile(path); string(got) != want {
			t.Errorf("%s = %q: a refused update replaced a binary", path, got)
		}
	}
}

func TestMasirUpdateRecordsTheRulesThePreflightNamed(t *testing.T) {
	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir"})
	gh.publish(ReleaseRepo, "v0.2.0", map[string][]byte{
		name: arc, "checksums.txt": []byte(fmt.Sprintf("%s  %s\n", sum(arc), name)),
	})
	bin := filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(bin, []byte("old masir"), 0o755)
	r := NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	r.Start(Masir{Clients: clients(), Current: "0.1.0", Binary: bin, WorkDir: r.workDir,
		Preflight: func(context.Context, string) error {
			return &RulesError{Problems: []*pb.RuleProblem{{RuleTag: "iran-direct", Missing: []string{"geoip:in-ir"}, Message: "geoip:in-ir isn't in geoip.dat"}}}
		},
	}.Run)
	job := wait(t, r)
	if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "can't build 1 of your rules") {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	if len(job.BlockedRules) != 1 || job.BlockedRules[0].RuleTag != "iran-direct" || job.BlockedRules[0].Missing[0] != "geoip:in-ir" {
		t.Fatalf("the job should carry the rules: %+v", job.BlockedRules)
	}
	if got, _ := os.ReadFile(bin); string(got) != "old masir" {
		t.Errorf("binary = %q", got)
	}
}
