package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// Masir is the job that installs a new masir release.
type Masir struct {
	Clients Clients
	Current string // the running version
	Force   bool   // install even when not newer
	Binary  string // the running masir executable, replaced by the new one
	// ServiceBinary is the system service's own copy of masir, when it's a different
	// file that this (root) daemon may write; it's updated too, so the service doesn't
	// start an old version at the next boot.
	ServiceBinary string
	// ClientBinary is the CLI's own copy, when this daemon is privileged enough to
	// replace it. It's best effort: the CLI finishes the job if this fails.
	ClientBinary string
	// Privileged reports whether this daemon runs with root/Administrator rights, so it
	// may write a client binary owned by that account.
	Privileged bool
	WorkDir    string
	// Preflight is given the new, verified binary before anything is replaced. An error
	// stops the update with the running version, and what it runs, untouched: the daemon
	// uses it to ask the new version whether it can start with the user's rules.
	Preflight func(ctx context.Context, stagedBinary string) error
	// Restart is called once the new binary is in place; the daemon then exits and
	// starts the new version.
	Restart func()
}

func (m Masir) Run(ctx context.Context, r *Reporter) error {
	r.State(pb.UpdateState_UPDATE_STATE_CHECKING)
	rel, err := Latest(ctx, m.Clients, ReleaseRepo)
	if err != nil {
		return err
	}
	r.Version(rel.Tag)
	if !m.Force {
		if !IsRelease(m.Current) {
			return errors.New("this is a development build; install a release, or force the update")
		}
		if !Newer(rel.Tag, m.Current) {
			r.State(pb.UpdateState_UPDATE_STATE_UP_TO_DATE)
			return nil
		}
	}

	name := ArchiveName(rel.Tag, runtime.GOOS, runtime.GOARCH)
	archive, ok := rel.Asset(name)
	if !ok {
		return fmt.Errorf("release %s has no build for %s/%s (%s)", rel.Tag, runtime.GOOS, runtime.GOARCH, name)
	}
	sums, ok := rel.Asset("checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}

	dir := filepath.Join(m.WorkDir, rel.Tag)
	if err := prepare(m.WorkDir, rel.Tag); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	r.State(pb.UpdateState_UPDATE_STATE_DOWNLOADING)
	r.File(name)
	r.Bytes(fileSize(path), archive.Size)
	list, err := fetchSmall(ctx, m.Clients, sums.URL)
	if err != nil {
		return err
	}
	if err := checkSignature(ctx, m.Clients, rel, list); err != nil {
		return err
	}
	want, err := checksum(list, name)
	if err != nil {
		return err
	}
	if err := download(ctx, m.Clients, archive.URL, path, r.Add); err != nil {
		return err
	}

	r.State(pb.UpdateState_UPDATE_STATE_VERIFYING)
	if err := verify(path, want); err != nil {
		os.Remove(path) // a fresh download next time
		return err
	}

	r.State(pb.UpdateState_UPDATE_STATE_INSTALLING)
	staged := filepath.Join(dir, "staged")
	if err := unpack(path, staged); err != nil {
		return err
	}
	r.StagedDir(staged)
	if m.Preflight != nil {
		if err := m.Preflight(ctx, filepath.Join(staged, exe("masir"))); err != nil {
			var re *RulesError
			if errors.As(err, &re) {
				r.Rules(re.Problems)
			}
			return err
		}
	}
	// A CLI installed elsewhere (the service runs its own copy) updates itself from
	// the staged binary.
	if err := replaceFile(filepath.Join(staged, exe("masir")), m.Binary); err != nil {
		return fmt.Errorf("install %s: %w", m.Binary, err)
	}
	if m.ServiceBinary != "" && m.ServiceBinary != m.Binary {
		// Best effort: the CLI checks afterwards and copies it with sudo if this failed.
		_ = replaceFile(filepath.Join(staged, exe("masir")), m.ServiceBinary)
	}
	if m.Privileged && m.ClientBinary != "" && m.ClientBinary != m.Binary && m.ClientBinary != m.ServiceBinary {
		// The daemon already runs with the rights the CLI's copy needs. Still best
		// effort: the CLI checks content afterwards and finishes it with sudo if needed.
		_ = replaceFile(filepath.Join(staged, exe("masir")), m.ClientBinary)
	}
	r.State(pb.UpdateState_UPDATE_STATE_RESTARTING)
	if m.Restart != nil {
		m.Restart()
	}
	return nil
}

// ArchiveName is a release archive's file name, e.g. masir-cli-darwin-0.2.0-arm64.tar.gz:
// the system, then the version, then the architecture, as every download is named (see
// .goreleaser.yaml, and the desktop app in the release workflow).
func ArchiveName(tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("masir-cli-%s-%s-%s%s", goos, strings.TrimPrefix(tag, "v"), goarch, ext)
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Geo is the job that refreshes geosite.dat and geoip.dat.
type Geo struct {
	Clients  Clients
	AssetDir string // where Xray reads geo files from
	WorkDir  string
	Force    bool // download even when the installed files match
	// Reload makes Xray read the new files; on error the old files come back.
	Reload func() error
	// Preflight is shown the verified new files before anything is installed, and
	// returns the enabled rules they can't build. A job that finds any is blocked: the
	// installed data stays, the download is kept, and nothing changes for a running Xray.
	Preflight func(geoip, geosite string) []*pb.RuleProblem
	// DisableBroken says the user agreed to turn those rules off: Disable does that, and
	// the install goes ahead.
	DisableBroken bool
	Disable       func(ctx context.Context, problems []*pb.RuleProblem) error
}

// geoFiles maps what GeoRepo publishes to the names Xray reads.
var geoFiles = []struct{ remote, local string }{
	{"geosite.dat", "geosite.dat"},
	{"geoip.dat", "geoip.dat"},
}

func (g Geo) Run(ctx context.Context, r *Reporter) error {
	r.State(pb.UpdateState_UPDATE_STATE_CHECKING)
	rel, err := Latest(ctx, g.Clients, GeoRepo)
	if err != nil {
		return err
	}
	r.Version(rel.Tag)

	type file struct {
		asset      Asset
		local, sum string
	}
	var files []file
	upToDate := true
	var total int64
	for _, f := range geoFiles {
		a, ok := rel.Asset(f.remote)
		if !ok {
			return fmt.Errorf("%s release %s has no %s", GeoRepo, rel.Tag, f.remote)
		}
		s, ok := rel.Asset(f.remote + ".sha256sum")
		if !ok {
			return fmt.Errorf("%s release %s has no checksum for %s", GeoRepo, rel.Tag, f.remote)
		}
		list, err := fetchSmall(ctx, g.Clients, s.URL)
		if err != nil {
			return err
		}
		sum, err := checksum(list, f.remote)
		if err != nil {
			return err
		}
		if have, err := fileSHA256(filepath.Join(g.AssetDir, f.local)); err != nil || have != sum {
			upToDate = false
		}
		files = append(files, file{asset: a, local: f.local, sum: sum})
		total += a.Size
	}
	if upToDate && !g.Force {
		r.State(pb.UpdateState_UPDATE_STATE_UP_TO_DATE)
		return nil
	}

	dir := filepath.Join(g.WorkDir, rel.Tag)
	if err := prepare(g.WorkDir, rel.Tag); err != nil {
		return err
	}
	var done int64
	for _, f := range files {
		done += fileSize(filepath.Join(dir, f.asset.Name))
	}
	r.State(pb.UpdateState_UPDATE_STATE_DOWNLOADING)
	r.Bytes(done, total)
	for _, f := range files {
		r.File(f.asset.Name)
		if err := download(ctx, g.Clients, f.asset.URL, filepath.Join(dir, f.asset.Name), r.Add); err != nil {
			return err
		}
	}

	r.State(pb.UpdateState_UPDATE_STATE_VERIFYING)
	for _, f := range files {
		if err := verify(filepath.Join(dir, f.asset.Name), f.sum); err != nil {
			os.Remove(filepath.Join(dir, f.asset.Name))
			return err
		}
	}

	if g.Preflight != nil {
		var ip, site string
		for _, f := range files {
			if f.local == "geoip.dat" {
				ip = filepath.Join(dir, f.asset.Name)
			} else {
				site = filepath.Join(dir, f.asset.Name)
			}
		}
		if problems := g.Preflight(ip, site); len(problems) > 0 {
			if !g.DisableBroken || g.Disable == nil {
				r.Blocked(problems) // the verified download stays, for the retry that disables them
				return nil
			}
			if err := g.Disable(ctx, problems); err != nil {
				return fmt.Errorf("turn off the rules the new data can't build: %w", err)
			}
		}
	}

	r.State(pb.UpdateState_UPDATE_STATE_INSTALLING)
	var swapped []string // local names now holding new data; their old copies end in .old
	rollback := func() {
		for _, local := range swapped {
			dst := filepath.Join(g.AssetDir, local)
			if fileSize(dst+".old") > 0 {
				os.Rename(dst+".old", dst)
			} else {
				os.Remove(dst) // there was nothing before
			}
		}
	}
	for _, f := range files {
		dst := filepath.Join(g.AssetDir, f.local)
		os.Remove(dst + ".old")
		if fileSize(dst) > 0 {
			if err := os.Rename(dst, dst+".old"); err != nil {
				rollback()
				return fmt.Errorf("install %s: %w", f.local, err)
			}
		}
		if err := copyFile(filepath.Join(dir, f.asset.Name), dst, 0o644); err != nil {
			swapped = append(swapped, f.local)
			rollback()
			return fmt.Errorf("install %s: %w", f.local, err)
		}
		swapped = append(swapped, f.local)
	}
	if g.Reload != nil {
		if err := g.Reload(); err != nil {
			rollback()
			_ = g.Reload()
			return fmt.Errorf("Xray rejected the new geo data, so the old files were put back: %w", err)
		}
	}
	for _, local := range swapped {
		os.Remove(filepath.Join(g.AssetDir, local) + ".old")
	}
	os.RemoveAll(g.WorkDir)
	r.State(pb.UpdateState_UPDATE_STATE_DONE)
	return nil
}

// prepare makes workDir/tag and deletes downloads of other versions.
func prepare(workDir, tag string) error {
	if entries, err := os.ReadDir(workDir); err == nil {
		for _, e := range entries {
			if e.Name() != tag {
				os.RemoveAll(filepath.Join(workDir, e.Name()))
			}
		}
	}
	dir := filepath.Join(workDir, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.Chmod(dir, 0o755) // whatever the umask: the CLI installs from here
}

// unpack extracts the masir binary from a release archive into dir.
func unpack(archive, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	want := map[string]bool{exe("masir"): true}
	found := 0
	extract := func(name string, r io.Reader) error {
		base := filepath.Base(name)
		if !want[base] {
			return nil
		}
		found++
		f, err := os.OpenFile(filepath.Join(dir, base), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(r, 512<<20)); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}

	if strings.HasSuffix(archive, ".zip") {
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = extract(f.Name, rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag == tar.TypeReg {
				if err := extract(h.Name, tr); err != nil {
					return err
				}
			}
		}
	}
	if found == 0 {
		return errors.New("the release archive has no masir binary")
	}
	return nil
}

// replaceFile puts src at dst. A running program can't be overwritten in place, but on
// Unix its file can be renamed over (the old process keeps the old file open); Windows
// lets the running file be renamed out of the way first.
func replaceFile(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o755); err != nil {
		return err
	}
	preserveOwner(tmp, dst)
	if runtime.GOOS == "windows" {
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil && !os.IsNotExist(err) {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// copyFile writes src to a new file at dst. dst may sit in a directory its user controls,
// so it is created exclusively (never following a link planted there) and given its mode
// through the open file, not the path.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	os.Remove(dst) // a stale copy, or a link: removing it doesn't touch what it points to
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
