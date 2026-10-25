// Package ui is masir's terminal look: clack-style logs, notes, prompts and the live
// picker, built on go-clack with width math that handles emoji and Persian text.
package ui

import (
	"os"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/orochaa/go-clack/core"
	"golang.org/x/term"
)

// SetupBidi turns on go-clack's RTL shaping (CLACK_RTL) unless the terminal already
// lays out right-to-left text itself. MASIR_BIDI=on|off overrides the guess.
func SetupBidi() {
	if _, set := os.LookupEnv("CLACK_RTL"); set {
		return
	}
	// Pipes and scripts get the text as is; shaping is only for display.
	on := term.IsTerminal(int(os.Stdout.Fd())) && !terminalDoesBidi()
	switch strings.ToLower(os.Getenv("MASIR_BIDI")) {
	case "on", "1", "true":
		on = true
	case "off", "0", "false":
		on = false
	}
	if on {
		_ = os.Setenv("CLACK_RTL", "1")
	}
}

func terminalDoesBidi() bool {
	// Windows Terminal and the modern Windows console shape and order Persian/
	// Arabic themselves; applying masir's own visual reordering on top reverses
	// the text. Users on a terminal that doesn't can set MASIR_BIDI=on.
	if runtime.GOOS == "windows" {
		return true
	}
	return os.Getenv("TERM_PROGRAM") == "Apple_Terminal" ||
		os.Getenv("VTE_VERSION") != "" ||
		os.Getenv("KONSOLE_VERSION") != "" ||
		strings.Contains(os.Getenv("TERM"), "mlterm")
}

// Visual puts right-to-left text (a Persian config name, say) in display order for
// terminals that don't do bidi. Apply it exactly once, to text from outside masir:
// subscription and config names, rule names, error messages. go-clack's options and
// the picker's items apply it themselves. Color codes are skipped, since reordering
// would scatter their characters.
func Visual(s string) string {
	if strings.Contains(s, "\n") { // each line is its own paragraph
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = Visual(l)
		}
		return strings.Join(lines, "\n")
	}
	if !strings.Contains(s, "\x1b") {
		return core.Visual(s)
	}
	var b strings.Builder
	start := 0
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			i++
			continue
		}
		b.WriteString(core.Visual(s[start:i]))
		end := skipEscape(s, i)
		b.WriteString(s[i:end])
		i, start = end, end
	}
	b.WriteString(core.Visual(s[start:]))
	return b.String()
}

// Width is how many terminal columns s takes, ignoring ANSI escapes.
func Width(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w += runeWidth(r)
		i += size
	}
	return w
}

// Truncate cuts s to w columns, ending with "…" when it had to cut. Escapes are kept
// and colors reset, so a cut never leaks color into what follows.
func Truncate(s string, w int) string {
	if Width(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cols, escaped := 0, false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := skipEscape(s, i)
			b.WriteString(s[i:j])
			escaped = true
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runeWidth(r)
		if cols+rw > w-1 {
			break
		}
		b.WriteRune(r)
		cols += rw
		i += size
	}
	b.WriteString("…")
	if escaped {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// Pad right-pads s with spaces to w columns.
func Pad(s string, w int) string {
	if n := w - Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// TermWidth is the output terminal's width, 80 when it can't be read.
func TermWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

func termHeight() int {
	if _, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && h > 0 {
		return h
	}
	return 24
}

func skipEscape(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '[' {
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
	}
	return min(j+1, len(s))
}

func runeWidth(r rune) int {
	switch {
	case r == 0 || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0 // combining marks, zero-width joiners, variation selectors
	case r >= 0x1F1E6 && r <= 0x1F1FF:
		return 1 // a regional indicator; two make one 2-column flag
	case r >= 0x1F000,
		r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE4F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	}
	return 1
}
