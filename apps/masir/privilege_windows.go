package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const privilegeName = "Administrator rights"

const elevationNote = "masir starts its daemon as Administrator for this session; Windows asks for permission."

const privilegeOwner = "Administrators"

const elevationAsk = "Windows asks for permission"

const enableTip = "Tip: `masir daemon enable` installs the daemon as a Windows service that starts at boot, so connecting won't ask for permission again."

const elevationHint = "Windows may ask for permission"

func elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// canElevateSilently reports whether becoming Administrator won't prompt: only when
// this process already is one.
func canElevateSilently() bool { return elevated() }

// startElevatedDaemon starts `masir daemon run` as Administrator in the background,
// serving user, until it's stopped or Windows restarts. Windows asks for permission
// unless this process is already elevated.
func startElevatedDaemon(user string, log *os.File) error {
	exe, err := self()
	if err != nil {
		return err
	}
	if elevated() {
		cmd := exec.Command(exe, "daemon", "run", "--user", user)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = detached()
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString("daemon run --user " + windows.EscapeArg(user))
	if err := windows.ShellExecute(0, verb, file, params, nil, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fail("Administrator permission was declined")
		}
		return fmt.Errorf("run as Administrator: %w", err)
	}
	return nil
}

// elevate re-runs masir with args as Administrator: Windows shows its permission
// prompt. ShellExecute can't wait for the elevated process, so that process writes its
// outcome to a file (see --elevated-result) and this waits for the file.
func elevate(args ...string) error {
	exe, err := self()
	if err != nil {
		return err
	}
	result := filepath.Join(os.TempDir(), fmt.Sprintf("masir-elevated-%d.txt", os.Getpid()))
	os.Remove(result)
	defer os.Remove(result)

	args = append(args, "--elevated-result", result)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = windows.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err := windows.ShellExecute(0, verb, file, params, nil, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fail("Administrator permission was declined")
		}
		return fmt.Errorf("run as Administrator: %w", err)
	}

	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if appCtx.Err() != nil {
			return appCtx.Err()
		}
		if b, err := os.ReadFile(result); err == nil {
			if msg := strings.TrimSpace(string(b)); msg != "ok" {
				return errors.New(msg)
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fail("the elevated masir didn't finish")
}

// currentUser is who the service should serve: this account's SID.
func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Uid
	}
	return ""
}
