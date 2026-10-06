package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	sqDialog "github.com/sqweek/dialog"
)

// Native file dialogs are not modal to the Fyne window, so without a guard
// every extra click spawns another one. Only one may be open at a time.
var fileDialogOpen atomic.Bool

type fileFilter struct {
	desc string
	exts []string
}

// pickFile shows a native open (or save) dialog on a background goroutine and
// calls onPick with the chosen path. status receives errors and the
// "already open" notice.
func pickFile(title string, save bool, filter fileFilter, status func(string), onPick func(path string)) {
	if !fileDialogOpen.CompareAndSwap(false, true) {
		status("A file dialog is already open.")
		return
	}
	goSafe(status, func() {
		defer fileDialogOpen.Store(false)
		b := sqDialog.File().Title(title).Filter(filter.desc, filter.exts...)
		var path string
		var err error
		if save {
			path, err = b.Save()
		} else {
			path, err = b.Load()
		}
		if err != nil {
			if err != sqDialog.ErrCancelled {
				status("File error: " + err.Error())
			}
			return
		}
		onPick(path)
	})
}

var (
	folderMu     sync.Mutex
	folderOpened = map[string]time.Time{}
)

// openOutputFolder reveals dir in the system file manager. Repeated clicks
// within a couple of seconds are ignored so they don't stack up windows.
func openOutputFolder(dir string) {
	if dir == "" {
		dir = "out"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	folderMu.Lock()
	if t, ok := folderOpened[dir]; ok && time.Since(t) < 2*time.Second {
		folderMu.Unlock()
		return
	}
	folderOpened[dir] = time.Now()
	folderMu.Unlock()

	_ = os.MkdirAll(dir, 0755)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	_ = cmd.Start()
}

func showOutputDialog(win fyne.Window, title, text string) {
	entry := widget.NewMultiLineEntry()
	entry.SetText(text)
	entry.Disable()
	d := dialog.NewCustom(title, "Close", container.NewScroll(entry), win)
	d.Resize(fyne.NewSize(600, 400))
	d.Show()
}

// progressDialog is a modal progress bar with a caption.
type progressDialog struct {
	bar *widget.ProgressBar
	lbl *lineLabel
	dlg dialog.Dialog
	// ctx is cancelled by the dialog's Cancel button (when it has one) and
	// when it closes.
	ctx    context.Context
	cancel context.CancelFunc
}

// newProgressDialog must be called from a background goroutine; it builds and
// shows the dialog on the UI thread and waits for it.
func newProgressDialog(win fyne.Window, title string) *progressDialog {
	return showProgress(win, title, false)
}

// newCancelableProgressDialog is newProgressDialog with a Cancel button that
// cancels p.ctx; the job should watch it.
func newCancelableProgressDialog(win fyne.Window, title string) *progressDialog {
	return showProgress(win, title, true)
}

func showProgress(win fyne.Window, title string, cancelable bool) *progressDialog {
	p := &progressDialog{}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	uiDoAndWait(func() {
		p.bar = widget.NewProgressBar()
		p.lbl = statusLabel("Starting…")
		body := container.NewVBox(p.lbl, p.bar)
		if cancelable {
			var btn *widget.Button
			btn = widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
				p.cancel()
				btn.Disable()
				p.lbl.SetText("Cancelling…")
			})
			body.Add(container.NewCenter(btn))
		}
		p.dlg = dialog.NewCustomWithoutButtons(title, body, win)
		p.dlg.Resize(fyne.NewSize(380, 110))
		p.dlg.Show()
	})
	return p
}

// set and close are safe to call from any goroutine. Once cancelled, set
// leaves the "Cancelling…" caption alone.
func (p *progressDialog) set(frac float64, msg string) {
	uiDo(func() {
		p.bar.SetValue(frac)
		if p.ctx.Err() == nil {
			p.lbl.SetText(msg)
		}
	})
}

func (p *progressDialog) close() {
	p.cancel()
	uiDo(p.dlg.Hide)
}
