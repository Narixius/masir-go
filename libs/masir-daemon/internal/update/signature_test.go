package update

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aead.dev/minisign"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func newKey(t *testing.T) (minisign.PublicKey, minisign.PrivateKey) {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func trust(t *testing.T, keys ...minisign.PublicKey) {
	t.Helper()
	var texts []string
	for _, k := range keys {
		texts = append(texts, k.String())
	}
	old := publicKeys
	publicKeys = strings.Join(texts, ",")
	t.Cleanup(func() { publicKeys = old })
}

// release publishes v0.2.0 with sums as its checksum list and sig, if any, as its signature.
func release(t *testing.T, sig func(sums []byte) []byte) (bin string, r *Runner, task Masir) {
	t.Helper()
	gh := newFakeGitHub(t)
	name := ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
	arc := archive(t, name, map[string]string{exe("masir"): "new masir"})
	sums := []byte(fmt.Sprintf("%s  %s\n", sum(arc), name))
	files := map[string][]byte{name: arc, "checksums.txt": sums}
	if sig != nil {
		files[SignatureAsset] = sig(sums)
	}
	gh.publish(ReleaseRepo, "v0.2.0", files)

	bin = filepath.Join(t.TempDir(), exe("masir"))
	os.WriteFile(bin, []byte("old masir"), 0o755)
	r = NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, t.TempDir())
	task = Masir{Clients: clients(), Current: "0.1.0", Binary: bin, WorkDir: r.workDir}
	return bin, r, task
}

func installed(t *testing.T, bin string) string {
	t.Helper()
	got, _ := os.ReadFile(bin)
	return string(got)
}

func TestUpdateInstallsAReleaseSignedByATrustedKey(t *testing.T) {
	pub, priv := newKey(t)
	trust(t, pub)
	bin, r, task := release(t, func(sums []byte) []byte { return minisign.Sign(priv, sums) })
	r.Start(task.Run)
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_DONE || job.Error != "" {
		t.Fatalf("job = %v (%s); a deferred install must finish as done", job.State, job.Error)
	}
	if got := installed(t, bin); got != "new masir" {
		t.Errorf("installed %q", got)
	}
}

func TestUpdateRefusesARelease(t *testing.T) {
	pub, priv := newKey(t)
	_, otherPriv := newKey(t)
	for name, c := range map[string]struct {
		sig  func(sums []byte) []byte
		want string
	}{
		"unsigned":              {nil, "isn't signed"},
		"signed by another key": {func(s []byte) []byte { return minisign.Sign(otherPriv, s) }, "signature doesn't match"},
		"signature of other content": {func(s []byte) []byte {
			return minisign.Sign(priv, append([]byte("tampered "), s...))
		}, "signature doesn't match"},
		"garbage signature": {func([]byte) []byte { return []byte("not a signature") }, "signature doesn't match"},
	} {
		t.Run(name, func(t *testing.T) {
			trust(t, pub)
			bin, r, task := release(t, c.sig)
			r.Start(task.Run)
			job := wait(t, r)
			if job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, c.want) {
				t.Fatalf("job = %v %q, want a failure containing %q", job.State, job.Error, c.want)
			}
			if got := installed(t, bin); got != "old masir" {
				t.Errorf("a refused release replaced the binary: %q", got)
			}
		})
	}
}

// A key can be replaced: a build trusting the old and the new key accepts either's releases.
func TestUpdateAcceptsEitherOfTwoKeys(t *testing.T) {
	oldPub, _ := newKey(t)
	newPub, newPriv := newKey(t)
	trust(t, oldPub, newPub)
	bin, r, task := release(t, func(s []byte) []byte { return minisign.Sign(newPriv, s) })
	r.Start(task.Run)
	if job := wait(t, r); job.Error != "" {
		t.Fatalf("job = %v (%s)", job.State, job.Error)
	}
	if got := installed(t, bin); got != "new masir" {
		t.Errorf("installed %q", got)
	}
}

func TestBuildWithoutKeysDoesNotCheckSignatures(t *testing.T) {
	trust(t) // none
	bin, r, task := release(t, nil)
	r.Start(task.Run)
	if job := wait(t, r); job.Error != "" {
		t.Fatalf("job = %v (%s)", job.State, job.Error)
	}
	if got := installed(t, bin); got != "new masir" {
		t.Errorf("installed %q", got)
	}
}

func TestInvalidKeyInTheBuildFailsClosed(t *testing.T) {
	old := publicKeys
	publicKeys = "not-a-key"
	t.Cleanup(func() { publicKeys = old })
	bin, r, task := release(t, nil)
	r.Start(task.Run)
	if job := wait(t, r); job.State != pb.UpdateState_UPDATE_STATE_FAILED || !strings.Contains(job.Error, "invalid update signing key") {
		t.Fatalf("job = %v %q", job.State, job.Error)
	}
	if got := installed(t, bin); got != "old masir" {
		t.Errorf("installed %q", got)
	}
}
