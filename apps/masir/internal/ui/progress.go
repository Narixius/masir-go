package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/orochaa/go-clack/prompts/symbols"
	isunicodesupported "github.com/orochaa/go-clack/third_party/is-unicode-supported"
)

var (
	progressFill   = "━"
	progressEmpty  = "─"
	progressFrames = []string{"◒", "◐", "◓", "◑"}
)

func init() {
	if !isunicodesupported.IsUnicodeSupported() {
		progressFill, progressEmpty = "#", "-"
		progressFrames = []string{"|", "/", "-", "\\"}
	}
}

// Progress is a compact progress line on clack's rail, for work with a known byte
// total (downloads) or unknown work (checking and installing). It redraws one line
// in place; piping prints only the final line.
type Progress struct {
	mu      sync.Mutex
	label   string
	done    int64
	total   int64
	frame   int
	samples []sample

	stop    chan struct{}
	stopped chan struct{}
	active  bool
	lines   int
}

type sample struct {
	at    time.Time
	bytes int64
}

// NewProgress starts a progress line. When stdout isn't a terminal it does nothing
// until Stop or Fail, which print a plain final line.
func NewProgress(label string) *Progress {
	p := &Progress{
		label:   label,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
		samples: []sample{{at: time.Now()}},
	}
	if !styled {
		close(p.stopped)
		return p
	}
	p.active = true
	os.Stdout.WriteString("\x1b[?25l")
	os.Stdout.WriteString(Gray(symbols.BAR) + "\n")
	go func() {
		defer close(p.stopped)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		p.mu.Lock()
		p.drawLocked()
		p.mu.Unlock()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				p.mu.Lock()
				p.frame++
				p.drawLocked()
				p.mu.Unlock()
			}
		}
	}()
	return p
}

// Set updates the progress. total 0 means the size isn't known yet.
func (p *Progress) Set(done, total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done, p.total = done, total
	now := time.Now()
	p.samples = append(p.samples, sample{at: now, bytes: done})
	for len(p.samples) > 1 && now.Sub(p.samples[0].at) > 5*time.Second {
		p.samples = p.samples[1:]
	}
}

// SetLabel changes what the progress line says, e.g. the file being fetched.
func (p *Progress) SetLabel(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.label = label
}

// Stop ends with the green success symbol.
func (p *Progress) Stop(msg string) { p.finish(false, msg) }

// Fail ends with the red error symbol. Like Error, it puts msg in display order.
func (p *Progress) Fail(msg string) { p.finish(true, msg) }

func (p *Progress) finish(failed bool, msg string) {
	p.mu.Lock()
	active := p.active
	if active {
		p.active = false
		close(p.stop)
	}
	p.mu.Unlock()
	if active {
		<-p.stopped
		os.Stdout.WriteString("\r\x1b[K")
		os.Stdout.WriteString("\x1b[?25h")
	}
	if !styled {
		if failed {
			Error(msg)
		} else {
			Step(msg)
		}
		return
	}
	symbol := Green(symbols.STEP_SUBMIT)
	if failed {
		symbol = Red(symbols.STEP_ERROR)
		msg = Red(Visual(msg))
	}
	os.Stdout.WriteString(symbol + " " + msg + "\n")
}

// drawLocked expects p.mu held.
func (p *Progress) drawLocked() {
	line := p.lineLocked()
	if p.lines > 0 {
		os.Stdout.WriteString("\r\x1b[K")
	}
	os.Stdout.WriteString(line)
	p.lines = 1
}

func (p *Progress) lineLocked() string {
	stats := progressStats(p.done, p.total, p.speedLocked(), p.etaLocked())
	bar := progressBar(p.done, p.total, 12, p.frame)
	label := Truncate(p.label, max(TermWidth()-Width(stats)-Width(bar)-5, 8))
	if label != "" {
		label += "  "
	}
	return Magenta(progressFrames[p.frame%len(progressFrames)]) + " " + label + bar + "  " + stats
}

// progressBar is a thin line: filled cells with progressFill, empty ones with
// progressEmpty. With no total it shows a short segment moving through the line.
func progressBar(done, total int64, width, frame int) string {
	if width < 1 {
		width = 1
	}
	fill := 0
	if total > 0 {
		fill = int(done * int64(width) / total)
		fill = min(max(fill, 0), width)
	} else {
		span := min(max(width/4, 2), width)
		start := frame % max(width-span+1, 1)
		return Gray(strings.Repeat(progressEmpty, start)) + Cyan(strings.Repeat(progressFill, span)) +
			Gray(strings.Repeat(progressEmpty, width-start-span))
	}
	return Cyan(strings.Repeat(progressFill, fill)) + Gray(strings.Repeat(progressEmpty, width-fill))
}

// progressStats is the compact text after the bar: percent, bytes, speed and ETA.
func progressStats(done, total int64, speed float64, eta time.Duration) string {
	if total <= 0 {
		s := compactBytes(done)
		if speed > 0 {
			s += " · " + compactBytes(int64(speed)) + "/s"
		}
		return s
	}
	pct := int(done * 100 / total)
	pct = min(max(pct, 0), 100)
	s := fmt.Sprintf("%d%% %s/%s", pct, compactBytes(done), compactBytes(total))
	if speed > 0 {
		s += " · " + compactBytes(int64(speed)) + "/s"
		if eta > 0 {
			s += " · ETA " + shortDuration(eta)
		}
	}
	return s
}

// compactBytes is Bytes with less precision, for the narrow progress line.
func compactBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%d B", n)
	case f >= 100:
		return fmt.Sprintf("%.0f %s", f, units[i])
	default:
		return fmt.Sprintf("%.1f %s", f, units[i])
	}
}

// speedLocked is the average speed over the last few seconds. Expects p.mu held.
func (p *Progress) speedLocked() float64 {
	if len(p.samples) < 2 {
		return 0
	}
	first := p.samples[0]
	last := p.samples[len(p.samples)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt < 0.5 || last.bytes <= first.bytes {
		return 0
	}
	return float64(last.bytes-first.bytes) / dt
}

// etaLocked estimates the time left at the current average speed. Expects p.mu held.
func (p *Progress) etaLocked() time.Duration {
	if p.total <= p.done {
		return 0
	}
	speed := p.speedLocked()
	if speed <= 0 {
		return 0
	}
	return time.Duration(float64(p.total-p.done) / speed * float64(time.Second))
}

// Bytes formats a byte count the way masir shows downloads.
func Bytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.2f %s", f, units[i])
}

// shortDuration is a rough time left: seconds, or minutes and hours for long ones.
func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
