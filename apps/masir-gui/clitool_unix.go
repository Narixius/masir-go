//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// The command-line tool is a link in the user's own bin directory, so installing it needs
// no administrator: it runs the masir the app ships with, and a later update of the app
// carries it along.
//
// ~/.local/bin is not on the PATH of a stock macOS, so installing also adds a marked,
// idempotent block to the shell's own config. An existing line for the directory is left
// alone, and removing the tool removes only the block this app wrote.

const (
	cliBlockStart = "# >>> masir command line >>>"
	cliBlockEnd   = "# <<< masir command line <<<"
)

// cliPathFile is a shell config and whether it is fish (which has its own syntax).
type cliPathFile struct {
	path string
	fish bool
}

func cliBinDir() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func cliCommand() string { return "masir" }

// cliStatusNow reads the installed command and whether a shell would find it. A GUI
// started from Finder does not inherit the terminal's PATH, so a standard directory
// counts as on the PATH even when this process's own PATH does not hold it.
func cliStatusNow(bin string) cliStatus {
	st := cliStatus{}
	link := ""
	if dir, err := cliBinDir(); err == nil {
		link = filepath.Join(dir, cliCommand())
		st.dir = dir
	}
	found := ""
	switch {
	case link != "" && executable(link):
		found = link
	default:
		if p, ok := findCLI(); ok {
			found = p
		}
	}
	if found == "" {
		return st
	}
	st.installed = true
	st.path = found
	st.dir = filepath.Dir(found)
	st.ours = cliOurs(found, bin)
	st.onPath = cliOnPath(st.dir)
	return st
}

// cliOurs reports whether the command is this app's: a link to the bundled masir, or a
// link that still points into a Masir bundle (which repairCLI can repoint).
func cliOurs(found, bin string) bool {
	if target, err := os.Readlink(found); err == nil {
		return samePath(target, bin) || ourLinkTarget(target)
	}
	return samePath(found, bin)
}

// findCLI looks for any masir a shell would find, without counting the copy inside the
// app bundle: that one is only on the PATH once the app puts its directory there.
func findCLI() (string, bool) {
	if p, err := exec.LookPath(cliCommand()); err == nil {
		return p, true
	}
	for _, dir := range cliSearchDirs() {
		p := filepath.Join(dir, cliCommand())
		if executable(p) {
			return p, true
		}
	}
	return "", false
}

// executable reports whether p is a file a shell can run. A file named masir that is
// not executable is not a command.
func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// cliStandardDirs are the directories a login shell searches before the user's own
// PATH entries. Tests replace it so a machine's real masir cannot change the result.
var cliStandardDirs = []string{"/usr/local/bin", "/opt/homebrew/bin", "/opt/local/bin", "/usr/bin", "/bin"}

// cliSearchDirs is where a masir may already have been installed. The common ones are
// checked even when a Finder-launched app's PATH does not contain them.
func cliSearchDirs() []string {
	dirs := append([]string{}, cliStandardDirs...)
	if dir, err := cliBinDir(); err == nil {
		dirs = append(dirs, dir)
	}
	if home, err := homeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "bin"))
	}
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	return uniqueDirs(dirs)
}

func uniqueDirs(dirs []string) []string {
	seen := map[string]bool{}
	out := dirs[:0]
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

// cliOnPath reports whether a login shell would find a command in dir. Standard
// directories are compiled into every login PATH on macOS and Linux; ~/.local/bin is
// only there when a shell config says so.
func cliOnPath(dir string) bool {
	if dir == "" {
		return false
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p != "" && samePath(p, dir) {
			return true
		}
	}
	switch filepath.Clean(dir) {
	case "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin":
		return true
	}
	if own, err := cliBinDir(); err == nil && samePath(dir, own) {
		return shellReadsPath(dir)
	}
	return false
}

// shellReadsPath reports whether the login shell's own files put dir on the PATH. A
// .bashrc line only counts when the login file sources .bashrc: macOS terminals start
// login shells and would otherwise miss it.
func shellReadsPath(dir string) bool {
	home, err := homeDir()
	if err != nil {
		return false
	}
	switch filepath.Base(loginShell()) {
	case "bash":
		login := bashLoginFile(home)
		if pathInFiles(dir, []string{login}) {
			return true
		}
		if !pathInFiles(dir, []string{filepath.Join(home, ".bashrc")}) {
			return false
		}
		data, err := os.ReadFile(login)
		return err == nil && strings.Contains(string(data), ".bashrc")
	default:
		return pathInFiles(dir, cliReadFiles())
	}
}

// installCLI links bin into ~/.local/bin and makes sure a shell reads that directory.
// A command that is not the app's is left alone, and so is one that is already there.
func installCLI(bin string) (cliStatus, error) {
	if bin == "" || !executable(bin) {
		return cliStatus{}, errors.New("the app ships no masir executable to install")
	}
	dir, err := cliBinDir()
	if err != nil {
		return cliStatus{}, err
	}
	link := filepath.Join(dir, cliCommand())

	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(link)
			switch {
			case samePath(target, bin):
				added := ensureCLIPath(dir)
				st := cliStatusNow(bin)
				st.needsShell = added
				return st, nil
			case !fileExists(link) && ourLinkTarget(target):
				// The app moved or was replaced: repoint the old link.
				if err := os.Remove(link); err != nil {
					return cliStatus{}, err
				}
			case !fileExists(link):
				return cliStatus{}, fmt.Errorf("%s is a broken link to another program; remove it first", link)
			default:
				// Another masir, or a working link to another copy: leave it be.
				return cliStatusNow(bin), nil
			}
		} else {
			// A regular file named masir belongs to someone else.
			return cliStatusNow(bin), nil
		}
	}

	if p, ok := findCLI(); ok && !samePath(p, link) {
		return cliStatusNow(bin), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cliStatus{}, err
	}
	if err := os.Symlink(bin, link); err != nil {
		return cliStatus{}, err
	}
	added := ensureCLIPath(dir)
	st := cliStatusNow(bin)
	st.needsShell = added
	return st, nil
}

// ensureCLIPath adds ~/.local/bin to the shell configs that need it. It returns whether
// it changed a file, which means an already open terminal will not see the command yet.
func ensureCLIPath(dir string) bool {
	if cliOnPath(dir) {
		return false
	}
	added := false
	for _, f := range cliWriteFiles() {
		if pathInFiles(dir, []string{f.path}) {
			continue
		}
		changed, err := appendPathBlock(f.path, f.fish)
		if err != nil {
			log.Printf("cli: %v", err)
			continue
		}
		added = added || changed
	}
	return added
}

// removeCLI removes this app's link and the PATH blocks it wrote. Another masir is not
// touched.
func removeCLI(bin string) error {
	dir, err := cliBinDir()
	if err != nil {
		return err
	}
	link := filepath.Join(dir, cliCommand())
	if target, err := os.Readlink(link); err == nil {
		if !samePath(target, bin) && !ourLinkTarget(target) {
			return fmt.Errorf("%s links another masir; leaving it alone", link)
		}
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if fileExists(link) {
		return fmt.Errorf("%s is another program; leaving it alone", link)
	}
	removePathBlocks()
	return nil
}

// repairCLI repoints a link whose Masir app no longer exists and restores the PATH block
// when it has gone missing. It never creates a link the person removed.
func repairCLI(bin string) {
	if bin == "" || !executable(bin) {
		return
	}
	dir, err := cliBinDir()
	if err != nil {
		return
	}
	link := filepath.Join(dir, cliCommand())
	target, err := os.Readlink(link)
	if err != nil {
		return // no link: respect a removal
	}
	if !samePath(target, bin) && !ourLinkTarget(target) {
		return // another program's link
	}
	if !executable(link) {
		if err := os.Remove(link); err != nil {
			return
		}
		if err := os.Symlink(bin, link); err != nil {
			log.Printf("cli: %v", err)
			return
		}
	}
	if !cliOnPath(dir) {
		ensureCLIPath(dir)
	}
}

// ourLinkTarget recognizes a link this app made in an earlier location, including one
// left by a moved or replaced Masir.app.
func ourLinkTarget(target string) bool {
	if filepath.Base(target) != "masir" {
		return false
	}
	return strings.Contains(target, "Masir.app/") ||
		strings.Contains(target, ".app/Contents/Resources/") ||
		strings.Contains(target, "/Resources/bin/masir")
}

func pathInFiles(dir string, files []string) bool {
	want := dir
	if own, err := cliBinDir(); err == nil && samePath(dir, own) {
		want = ".local/bin"
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.Contains(line, want) &&
				(strings.Contains(strings.ToUpper(line), "PATH") || strings.Contains(line, "fish_add_path")) {
				return true
			}
		}
	}
	return false
}

// cliReadFiles are the shell files the login shell reads. Checking all of them keeps a
// PATH that is already configured from getting a second block.
func cliReadFiles() []string {
	home, err := homeDir()
	if err != nil {
		return nil
	}
	switch filepath.Base(loginShell()) {
	case "zsh":
		return []string{
			filepath.Join(home, ".zshenv"),
			filepath.Join(home, ".zprofile"),
			filepath.Join(home, ".zshrc"),
		}
	case "bash":
		return []string{
			filepath.Join(home, ".bashrc"),
			bashLoginFile(home),
		}
	case "fish":
		files := []string{filepath.Join(home, ".config", "fish", "config.fish")}
		matches, _ := filepath.Glob(filepath.Join(home, ".config", "fish", "conf.d", "*.fish"))
		return append(files, matches...)
	default:
		return []string{filepath.Join(home, ".profile")}
	}
}

// cliWriteFiles are where a missing PATH line belongs. zsh reads .zshrc for an
// interactive shell; bash needs its login file as well, because macOS terminals start
// login shells and do not read .bashrc unless the login file sources it.
func cliWriteFiles() []cliPathFile {
	home, err := homeDir()
	if err != nil {
		return nil
	}
	switch filepath.Base(loginShell()) {
	case "zsh":
		return []cliPathFile{{path: filepath.Join(home, ".zshrc")}}
	case "bash":
		return []cliPathFile{
			{path: filepath.Join(home, ".bashrc")},
			{path: bashLoginFile(home)},
		}
	case "fish":
		return []cliPathFile{{
			path: filepath.Join(home, ".config", "fish", "conf.d", "masir.fish"),
			fish: true,
		}}
	default:
		return []cliPathFile{{path: filepath.Join(home, ".profile")}}
	}
}

func bashLoginFile(home string) string {
	for _, name := range []string{".bash_profile", ".bash_login"} {
		if p := filepath.Join(home, name); fileExists(p) {
			return p
		}
	}
	return filepath.Join(home, ".profile")
}

// cliAllFiles is every file removing the tool may need to clean, including the ones a
// different shell would have read.
func cliAllFiles() []string {
	home, err := homeDir()
	if err != nil {
		return nil
	}
	files := []string{
		filepath.Join(home, ".zshenv"),
		filepath.Join(home, ".zprofile"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".bash_login"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
		filepath.Join(home, ".config", "fish", "conf.d", "masir.fish"),
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".config", "fish", "conf.d", "*.fish"))
	return append(files, matches...)
}

// loginShell is the shell a terminal will start. A Finder-launched app often has no
// SHELL, so the passwd entry and then the platform default are fallbacks.
func loginShell() string {
	if s := os.Getenv("SHELL"); s != "" && filepath.IsAbs(s) {
		return s
	}
	if u, err := user.Current(); err == nil {
		if data, err := os.ReadFile("/etc/passwd"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				f := strings.Split(line, ":")
				if len(f) >= 7 && f[2] == u.Uid && f[6] != "" {
					return f[6]
				}
			}
		}
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

func appendPathBlock(path string, fish bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), cliBlockStart) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	block := cliPathBlock(fish)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		block = "\n" + block
	}
	if _, err := f.WriteString(block); err != nil {
		return false, err
	}
	return true, nil
}

func cliPathBlock(fish bool) string {
	if fish {
		return cliBlockStart + "\n" +
			"if not contains $HOME/.local/bin $PATH\n" +
			"    set -gx PATH $PATH $HOME/.local/bin\n" +
			"end\n" +
			cliBlockEnd + "\n"
	}
	return cliBlockStart + "\n" +
		"case \":$PATH:\" in\n" +
		"  *\":$HOME/.local/bin:\"*) ;;\n" +
		"  *) export PATH=\"$PATH:$HOME/.local/bin\" ;;\n" +
		"esac\n" +
		cliBlockEnd + "\n"
}

func removePathBlocks() {
	for _, path := range cliAllFiles() {
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), cliBlockStart) {
			continue
		}
		next := stripPathBlock(string(data))
		if next == string(data) {
			continue
		}
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(path, []byte(next), mode); err != nil {
			log.Printf("cli: %v", err)
		}
	}
}

func stripPathBlock(s string) string {
	var out strings.Builder
	skip := false
	for _, line := range strings.SplitAfter(s, "\n") {
		t := strings.TrimSpace(line)
		if t == cliBlockStart {
			skip = true
			continue
		}
		if skip {
			if t == cliBlockEnd {
				skip = false
			}
			continue
		}
		out.WriteString(line)
	}
	return out.String()
}
