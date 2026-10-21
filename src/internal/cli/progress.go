package cli

import (
	"fmt"
	"os"
	"sync"

	"MediaBoy/internal/video"
)

// meter shows progress on one stderr line that is rewritten in place. It is
// safe for concurrent use: frame workers report from many goroutines.
type meter struct {
	mu    sync.Mutex
	shown string // the line on screen, "" when none
}

// update shows label with done/total, rewriting the line only when the
// percentage changes.
func (m *meter) update(label string, done, total int) {
	if total <= 0 {
		return
	}
	line := fmt.Sprintf("%s %d / %d (%d %%)", label, min(done, total), total, 100*min(done, total)/total)
	m.mu.Lock()
	defer m.mu.Unlock()
	if line == m.shown {
		return
	}
	pad := max(0, len(m.shown)-len(line))
	fmt.Fprintf(os.Stderr, "\r%s%*s", line, pad, "")
	m.shown = line
}

// frames reports a frame-by-frame phase.
func (m *meter) frames(label string) func(done, total int) {
	return func(done, total int) { m.update(label, done, total) }
}

// fit reports a Trim/Split encode, whose passes each restart at the part's
// first frame.
func (m *meter) fit(split bool) video.FitProgress {
	return func(part, pass, done, total int) {
		label := fmt.Sprintf("Fitting · pass %d — frame", pass)
		if split {
			label = fmt.Sprintf("Part %d · pass %d — frame", part, pass)
		}
		m.update(label, done, total)
	}
}

// say ends the progress line and prints msg on a line of its own.
func (m *meter) say(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shown != "" {
		fmt.Fprintln(os.Stderr)
		m.shown = ""
	}
	fmt.Fprintln(os.Stderr, msg)
}

// end finishes the progress line.
func (m *meter) end() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shown != "" {
		fmt.Fprintln(os.Stderr)
		m.shown = ""
	}
}
