package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/orochaa/go-clack/prompts/symbols"
	pc "github.com/orochaa/go-clack/third_party/picocolors"
	"golang.org/x/term"
)

// styled is on when stdout is a terminal. Off, output is plain lines that scripts and
// pipes can read: no rail or symbols, and warnings and errors on stderr.
var styled = term.IsTerminal(int(os.Stdout.Fd()))

// Colors, re-exported so commands need one import for the whole look.
var (
	Bold    = pc.Bold
	Dim     = pc.Dim
	Gray    = pc.Gray
	Red     = pc.Red
	Green   = pc.Green
	Yellow  = pc.Yellow
	Blue    = pc.Blue
	Cyan    = pc.Cyan
	Magenta = pc.Magenta
)

// Each log line sits on clack's rail: a gray "│", then "<symbol>  message"; further
// lines continue on the rail.
func logLine(symbol, msg string) {
	lines := strings.Split(msg, "\n")
	var b strings.Builder
	b.WriteString(Gray(symbols.BAR) + "\n")
	b.WriteString(symbol + "  " + lines[0] + "\n")
	for _, l := range lines[1:] {
		b.WriteString(Gray(symbols.BAR) + "  " + l + "\n")
	}
	os.Stdout.WriteString(b.String())
}

func Info(msg string)    { log(Blue(symbols.INFO), msg, os.Stdout, "") }
func Success(msg string) { log(Green(symbols.SUCCESS), msg, os.Stdout, "") }
func Step(msg string)    { log(Green(symbols.STEP_SUBMIT), msg, os.Stdout, "") }
func Warn(msg string)    { log(Yellow(symbols.WARN), msg, os.Stderr, "warning: ") }

// Error messages are raw text, often carrying a name from a subscription, so Error
// puts them in display order itself.
func Error(msg string) { log(Red(symbols.ERROR), Visual(msg), os.Stderr, "error: ") }

func log(symbol, msg string, plain *os.File, prefix string) {
	if !styled {
		fmt.Fprintln(plain, prefix+msg)
		return
	}
	logLine(symbol, msg)
}

// Message prints lines on the rail without a symbol.
func Message(msg string) {
	if !styled {
		fmt.Println(msg)
		return
	}
	var b strings.Builder
	b.WriteString(Gray(symbols.BAR) + "\n")
	for _, l := range strings.Split(msg, "\n") {
		b.WriteString(Gray(symbols.BAR) + "  " + l + "\n")
	}
	os.Stdout.WriteString(b.String())
}

func Intro(title string) {
	if !styled {
		return
	}
	fmt.Printf("%s  %s\n", Gray(symbols.BAR_START), title)
}

func Outro(msg string) {
	if !styled {
		return
	}
	fmt.Printf("%s\n%s  %s\n\n", Gray(symbols.BAR), Gray(symbols.BAR_END), msg)
}

// Note draws clack's titled box. When it wouldn't fit the terminal, it falls back to
// lines on the rail, which never wrap into a broken box.
func Note(msg, title string) {
	lines := strings.Split(msg, "\n")
	if !styled {
		fmt.Println(title + ":")
		for _, l := range lines {
			fmt.Println("  " + l)
		}
		return
	}
	inner := Width(title)
	for _, l := range lines {
		inner = max(inner, Width(l))
	}
	inner += 2
	if inner+4 > TermWidth() {
		var b strings.Builder
		b.WriteString(Gray(symbols.BAR) + "\n")
		b.WriteString(Green(symbols.STEP_SUBMIT) + "  " + title + "\n")
		for _, l := range lines {
			b.WriteString(Gray(symbols.BAR) + "  " + l + "\n")
		}
		os.Stdout.WriteString(b.String())
		return
	}

	var b strings.Builder
	b.WriteString(Gray(symbols.BAR) + "\n")
	b.WriteString(Green(symbols.STEP_SUBMIT) + "  " + title + " " +
		Gray(strings.Repeat(symbols.BAR_H, max(inner-Width(title)-1, 1))+symbols.CORNER_TOP_RIGHT) + "\n")
	empty := Gray(symbols.BAR) + strings.Repeat(" ", inner+2) + Gray(symbols.BAR) + "\n"
	b.WriteString(empty)
	for _, l := range lines {
		b.WriteString(Gray(symbols.BAR) + "  " + Pad(l, inner) + Gray(symbols.BAR) + "\n")
	}
	b.WriteString(empty)
	b.WriteString(Gray(symbols.CONNECT_LEFT+strings.Repeat(symbols.BAR_H, inner+2)+symbols.CORNER_BOTTOM_RIGHT) + "\n")
	os.Stdout.WriteString(b.String())
}

// Tree renders "label: value" pairs as ├─ / └─ branches, padded so values line up.
func Tree(rows [][2]string) string {
	w := 0
	for _, r := range rows {
		w = max(w, Width(r[0]))
	}
	var b strings.Builder
	for i, r := range rows {
		branch := "├─"
		if i == len(rows)-1 {
			branch = "└─"
		}
		if i > 0 {
			b.WriteString("\n")
		}
		label := ""
		if r[0] != "" {
			label = Pad(r[0]+":", w+1) + " "
		}
		b.WriteString(Dim(branch) + " " + label + r[1])
	}
	return b.String()
}

// Styled reports whether output is a terminal, where masir draws clack's look.
func Styled() bool { return styled }
