//go:build darwin || linux

package transport

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListenServesOneUser(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := Listen(sock, Peer{UID: os.Getuid(), GID: os.Getgid()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if info, err := os.Stat(sock); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}

	go func() {
		if c, err := l.Accept(); err == nil {
			_, _ = c.Write([]byte("hi"))
			c.Close()
		}
	}()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got, _ := io.ReadAll(c); string(got) != "hi" {
		t.Fatalf("own uid got %q, want to be served", got)
	}

	// A stale socket left by a dead daemon is replaced.
	l.Close()
	if !fileExists(sock) {
		t.Skip("listener removed its socket on close; nothing stale to test")
	}
	l2, err := Listen(sock, Peer{UID: os.Getuid(), GID: os.Getgid()})
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	l2.Close()
}

func TestPeerCheckDropsOtherUsers(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	inner, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	l := &peerChecked{Listener: inner, allow: func(int) bool { return false }}
	defer l.Close()
	go func() { _, _ = l.Accept() }()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("rejected peer read = %v, want EOF (connection closed)", err)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
