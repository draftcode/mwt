// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// progress reports how far a concurrent fan-out has got, on a single line
// rewritten in place. It writes only to a terminal: redirected output is meant
// to be parsed, and carriage returns in a log or a pipe are noise.
type progress struct {
	w       io.Writer
	total   int
	mu      sync.Mutex
	n       int
	written int
}

// newProgress returns a progress for total items, inert unless w is a terminal.
func newProgress(w io.Writer, total int) *progress {
	if !isTerminal(w) || total == 0 {
		return &progress{}
	}
	return &progress{w: w, total: total}
}

// done records one finished item and redraws the line.
func (p *progress) done(name string) {
	if p.w == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	line := fmt.Sprintf("syncing %d/%d  %s", p.n, p.total, name)
	// Pad over whatever the previous, possibly longer, line left behind.
	pad := ""
	if n := p.written - len(line); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	p.written = len(line)
	fmt.Fprintf(p.w, "\r%s%s", line, pad)
}

// clear erases the line so the caller's own output starts from column zero.
func (p *progress) clear() {
	if p.w == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", p.written))
	p.written = 0
}

// isTerminal reports whether w is a character device, which is as much as mwt
// needs to know to decide whether redrawing a line is appropriate.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
