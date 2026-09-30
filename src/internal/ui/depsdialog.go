package ui

import (
	"errors"
	"strings"
	"sync/atomic"

	"MediaBoy/internal/deps"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	prefGBDKHome      = "gbdkHome"
	prefCheckOnLaunch = "deps.checkOnLaunch"
)

var depsInstalling atomic.Bool

// checkDeps detects GBDK and ffmpeg/ffprobe. When something is missing (or
// always, if showIfComplete) it shows a dialog offering to install just the
// missing parts.
func (s *appState) checkDeps(showIfComplete bool) {
	st := deps.Check(s.cfg.GBDKHome)
	s.applyGBDKHome(st.GBDKHome)
	if st.Complete() && !showIfComplete {
		return
	}
	s.showDepsDialog(st)
}

func (s *appState) applyGBDKHome(home string) {
	if home == "" {
		return
	}
	s.cfg.GBDKHome = home
	fyne.CurrentApp().Preferences().SetString(prefGBDKHome, home)
	if s.settingsRefresh != nil {
		s.settingsRefresh()
	}
	if s.music != nil {
		s.music.cfg.GBDKHome = home
		s.music.settingsRefresh()
	}
}

func depRow(ok bool, name, purpose, path string) fyne.CanvasObject {
	icon := widget.NewIcon(theme.ConfirmIcon())
	where := path
	if !ok {
		icon.SetResource(theme.ErrorIcon())
		where = "not found"
	}
	title := widget.NewLabelWithStyle(name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	detail := widget.NewLabel(purpose + " — " + where)
	detail.Wrapping = fyne.TextWrapWord
	return container.NewBorder(nil, nil, container.NewCenter(icon), nil, container.NewVBox(title, detail))
}

func (s *appState) showDepsDialog(st deps.Status) {
	prefs := fyne.CurrentApp().Preferences()
	var d dialog.Dialog

	body := container.NewVBox(newTile(container.NewVBox(
		depRow(st.HaveGBDK(), "GBDK-2020", "Game Boy C compiler (SDCC + lcc), builds every ROM", st.GBDKHome),
		depRow(st.FFmpeg != "", "ffmpeg", "decodes video and PCM music", st.FFmpeg),
		depRow(st.FFprobe != "", "ffprobe", "reads video frame rate and length", st.FFprobe),
	)))

	install := func(system bool) func() {
		return func() {
			d.Hide()
			s.installDeps(st, system)
		}
	}

	switch {
	case st.Complete():
		body.Add(hintLabel("Everything is installed. No host C compiler is needed: GBDK ships its own (SDCC)."))

	case st.HaveFFmpeg():
		// Only GBDK is missing: it is always installed locally.
		btn := widget.NewButtonWithIcon("Install GBDK", theme.DownloadIcon(), install(false))
		btn.Importance = widget.HighImportance
		body.Add(newTile(container.NewVBox(
			hintLabel("GBDK will be downloaded into "+deps.LocalDir()+" (portable, no password needed)."),
			btn,
		)))

	default:
		plan, sysOK := deps.SystemPlan(st)
		sysBtn := widget.NewButtonWithIcon("Install ffmpeg system-wide", theme.ComputerIcon(), install(true))
		sysBtn.Importance = widget.HighImportance
		if !sysOK {
			sysBtn.Disable()
		}
		localBtn := widget.NewButtonWithIcon("Install everything locally", theme.FolderIcon(), install(false))

		body.Add(newTile(container.NewVBox(
			secLabel("System-wide ffmpeg"),
			hintLabel("ffmpeg is installed with the system package manager and stays available to other programs.\n"+
				strings.Join(plan, "\n")),
			sysBtn,
		)))
		body.Add(newTile(container.NewVBox(
			secLabel("Locally"),
			hintLabel("Downloads the missing tools into "+deps.LocalDir()+
				". Nothing outside that folder is touched and no password is needed."),
			localBtn,
		)))
	}

	startup := widget.NewCheck("Check dependencies at startup", func(v bool) { prefs.SetBool(prefCheckOnLaunch, v) })
	startup.SetChecked(prefs.BoolWithFallback(prefCheckOnLaunch, true))
	closeBtn := widget.NewButton("Close", func() { d.Hide() })
	body.Add(container.NewBorder(nil, nil, nil, closeBtn, startup))

	title := "Dependencies"
	if !st.Complete() {
		title = "Missing dependencies"
	}
	d = dialog.NewCustomWithoutButtons(title, body, s.win)
	d.Resize(fyne.NewSize(560, 0))
	d.Show()
}

func (s *appState) installDeps(st deps.Status, system bool) {
	if !depsInstalling.CompareAndSwap(false, true) {
		s.statusForMode("Dependencies are already being installed…")
		return
	}
	lbl := statusLabel("Starting…")
	pd := dialog.NewCustomWithoutButtons("Installing dependencies",
		container.NewVBox(lbl, widget.NewProgressBarInfinite()), s.win)
	pd.Resize(fyne.NewSize(460, 0))
	pd.Show()
	report := func(msg string) {
		fyne.Do(func() {
			lbl.SetText(msg)
			s.statusForMode(msg)
		})
	}

	go func() {
		defer depsInstalling.Store(false)
		var res deps.Status
		var err error
		if system {
			res, err = deps.InstallSystem(st, report)
		} else {
			res, err = deps.InstallLocal(st, report)
		}
		fyne.Do(func() {
			pd.Hide()
			s.applyGBDKHome(res.GBDKHome)
			if errors.Is(err, deps.ErrRestartRequired) {
				s.statusForMode("ffmpeg installed — restart MediaBoy to use it.")
				dialog.ShowInformation("Restart required",
					"ffmpeg was installed by the system package manager.\n\n"+
						"MediaBoy still has the PATH it was started with, so it will only\n"+
						"see ffmpeg after a restart. Please close and reopen the app.", s.win)
				return
			}
			if err != nil {
				s.statusForMode("Dependency install failed.")
				dialog.ShowError(err, s.win)
				return
			}
			s.statusForMode("Dependencies installed.")
			s.showDepsDialog(res)
		})
	}()
}
