package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Live is a set of tree rows that each show a spinner until their result arrives,
// redrawn in place, so slow and fast checks report independently. Piped, it prints
// the finished rows once.
type Live struct {
	mu      sync.Mutex
	labels  []string
	prefix  []string // shown before the spinner and the result, e.g. a config's name
	results []string // "" while pending
	frame   int
	lines   int
	stop    chan struct{}
	done    chan struct{}
}

// LiveRow is one row: its label, and text shown before its spinner and result (a
// config's name, say).
type LiveRow struct{ Label, Prefix string }

func NewLive(rows ...LiveRow) *Live {
	l := &Live{results: make([]string, len(rows)), stop: make(chan struct{}), done: make(chan struct{})}
	for _, r := range rows {
		l.labels = append(l.labels, r.Label)
		l.prefix = append(l.prefix, r.Prefix)
	}
	if !styled {
		close(l.done)
		return l
	}
	os.Stdout.WriteString("\x1b[?25l")
	l.draw()
	go func() {
		defer close(l.done)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-t.C:
				l.mu.Lock()
				l.frame++
				l.draw()
				l.mu.Unlock()
			}
		}
	}()
	return l
}

// Set gives row i its result.
func (l *Live) Set(i int, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.results[i] = text
	if styled {
		l.draw()
	}
}

// Stop draws the final rows; pending ones read as skipped.
func (l *Live) Stop() {
	if styled {
		close(l.stop)
		<-l.done
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, r := range l.results {
		if r == "" {
			l.results[i] = Dim("–")
		}
	}
	if styled {
		l.draw()
		os.Stdout.WriteString("\x1b[?25h\n")
		return
	}
	fmt.Println(Tree(l.rows()))
}

func (l *Live) rows() [][2]string {
	rows := make([][2]string, len(l.labels))
	for i, label := range l.labels {
		v := l.results[i]
		if v == "" {
			v = Cyan(spinFrames[l.frame%len(spinFrames)]) + " " + Dim("testing")
		}
		if l.prefix[i] != "" {
			v = l.prefix[i] + "  " + v
		}
		rows[i] = [2]string{label, v}
	}
	return rows
}

// draw expects l.mu held.
func (l *Live) draw() {
	var b strings.Builder
	if l.lines > 0 {
		b.WriteString("\r")
		if l.lines > 1 {
			fmt.Fprintf(&b, "\x1b[%dA", l.lines-1)
		}
		b.WriteString("\x1b[J")
	}
	tree := Tree(l.rows())
	b.WriteString(tree)
	os.Stdout.WriteString(b.String())
	l.lines = strings.Count(tree, "\n") + 1
}
