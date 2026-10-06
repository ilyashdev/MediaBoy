package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"MediaBoy/internal/safe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// lineLabel is a single-line label that truncates instead of widening the window.
type lineLabel struct{ widget.Label }

func statusLabel(text string) *lineLabel {
	l := &lineLabel{}
	l.Text = oneLine(text)
	l.Truncation = fyne.TextTruncateEllipsis
	l.ExtendBaseWidget(l)
	return l
}

func (l *lineLabel) SetText(text string) { l.Label.SetText(oneLine(text)) }

// oneLine folds text into one line. Fyne's ellipsis truncation panics on a
// line ending in '\r' (CRLF output of ffmpeg, compilers, the shell), so line
// breaks become " · " and other control characters are dropped.
func oneLine(s string) string {
	var b strings.Builder
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(strings.Map(func(r rune) rune {
			switch {
			case r == '\t':
				return ' '
			case unicode.IsControl(r):
				return -1
			}
			return r
		}, line))
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(line)
	}
	return b.String()
}

// panicStatus shows panics caught on the UI thread; set by Run.
var panicStatus func(string)

// uiDo is fyne.Do with a panic in fn caught and reported instead of
// taking down the app.
func uiDo(fn func()) {
	fyne.Do(func() {
		defer recoverTo(panicStatus)
		fn()
	})
}

func uiDoAndWait(fn func()) {
	fyne.DoAndWait(func() {
		defer recoverTo(panicStatus)
		fn()
	})
}

// setText updates a label from any goroutine. A panic is only logged, so
// reporting one through a status bar can't loop.
func setText(l *lineLabel, msg string) {
	fyne.Do(func() {
		defer recoverTo(nil)
		l.SetText(msg)
	})
}

// goSafe runs fn on a new goroutine; a panic is reported to status.
func goSafe(status func(string), fn func()) {
	go func() {
		defer recoverTo(status)
		fn()
	}()
}

// recoverTo must be deferred directly.
func recoverTo(status func(string)) {
	if p := safe.Recovered(recover()); p != nil {
		reportPanic(p, status)
	}
}

// reportPanic logs a panic with its stack to stderr and the crash log and
// shows a short notice in status, if given.
func reportPanic(p *safe.PanicError, status func(string)) {
	msg := fmt.Sprintf("panic: %v\n\n%s", p.Value, p.Stack)
	fmt.Fprintln(os.Stderr, msg)
	notice := p.Error()
	if path, err := appendCrashLog(msg); err == nil {
		notice += " — details in " + path
	}
	if status != nil {
		status(notice)
	}
}

func appendCrashLog(msg string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "MediaBoy", "crash.log")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "=== %s\n%s\n", time.Now().Format(time.RFC3339), msg)
	return path, err
}
