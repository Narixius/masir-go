package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicePlistIsValid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "masir.plist")
	if err := os.WriteFile(p, []byte(servicePlist("/usr/local/lib/masir/masir", 501)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v\n%s", err, out)
	}
	out, err := exec.Command("plutil", "-extract", "ProgramArguments", "json", "-o", "-", p).Output()
	if err != nil || strings.TrimSpace(string(out)) != `["\/usr\/local\/lib\/masir\/masir","daemon","run","--user","501"]` {
		t.Fatalf("ProgramArguments = %s, %v", out, err)
	}
	if out, _ := exec.Command("plutil", "-extract", "KeepAlive.SuccessfulExit", "raw", p).Output(); strings.TrimSpace(string(out)) != "false" {
		t.Fatalf("KeepAlive.SuccessfulExit = %s; a clean Shutdown must not be restarted", out)
	}
}
