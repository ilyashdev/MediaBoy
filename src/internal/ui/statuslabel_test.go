package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// Fyne's ellipsis truncation panicked on a line ending in '\r' (CRLF output).
func TestStatusLabelCRLF(t *testing.T) {
	test.NewTempApp(t)
	l := statusLabel("x")
	w := test.NewTempWindow(t, l)
	w.Resize(fyne.NewSize(400, 40))
	l.SetText("Video error: exit status 183\r\nmoov atom not found\r\n\tError opening input\r\n")
	if want := "Video error: exit status 183 · moov atom not found · Error opening input"; l.Text != want {
		t.Errorf("got %q, want %q", l.Text, want)
	}
}
