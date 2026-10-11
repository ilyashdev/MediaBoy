package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"MediaBoy/internal/core"
	"MediaBoy/internal/ffmpeg"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/music"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type musicState struct {
	cfg core.ConvertConfig
	win fyne.Window

	songs    []*core.Song
	selected int
	busy     atomic.Bool

	songList    *widget.List
	codecSelect *widget.Select
	titleEntry  *widget.Entry
	coverCanvas *canvas.Image
	titleLabel  *lineLabel
	statusBar   *lineLabel
	compileBtn  *widget.Button
	progress    *widget.ProgressBar

	settingsRefresh func()
	sizeLabel       *widget.Label
}

type tappableImage struct {
	widget.BaseWidget
	image *canvas.Image
	onTap func()
}

func newTappableImage(img *canvas.Image, onTap func()) *tappableImage {
	t := &tappableImage{image: img, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.image)
}

func (t *tappableImage) Tapped(_ *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func buildMusicEditorContent(win fyne.Window, mainCfg core.ConvertConfig) (fyne.CanvasObject, *musicState) {
	ms := &musicState{
		cfg:      mainCfg,
		win:      win,
		selected: -1,
	}
	ms.cfg.Mode = core.ModeCGB
	return ms.buildUI(), ms
}

func (ms *musicState) buildUI() fyne.CanvasObject {
	ms.coverCanvas = canvas.NewImageFromImage(nil)
	ms.coverCanvas.FillMode = canvas.ImageFillContain
	ms.coverCanvas.ScaleMode = canvas.ImageScalePixels
	ms.coverCanvas.SetMinSize(fyne.NewSize(320, 288))

	ms.titleLabel = statusLabel("No track selected")
	ms.titleLabel.Alignment = fyne.TextAlignCenter
	ms.titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	coverTap := newTappableImage(ms.coverCanvas, func() { ms.setCover() })

	prevBtn := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { ms.step(-1) })
	nextBtn := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() { ms.step(+1) })
	coverBtn := widget.NewButtonWithIcon("Set cover…", theme.FileImageIcon(), func() { ms.setCover() })

	preview := newTile(container.NewBorder(
		secLabel("Device preview"),
		container.NewVBox(
			ms.titleLabel,
			container.NewCenter(container.NewHBox(prevBtn, coverBtn, nextBtn)),
		),
		nil, nil,
		coverTap,
	))

	addBtn := widget.NewButtonWithIcon("Add Song…", theme.ContentAddIcon(), func() { ms.addSong() })
	addBtn.Importance = widget.HighImportance

	ms.statusBar = statusLabel("Add songs to begin.")
	ms.progress = widget.NewProgressBar()
	ms.progress.Hide()
	bottom := container.NewVBox(ms.progress, ms.statusBar)

	left := container.NewBorder(addBtn, nil, nil, nil, ms.buildSettingsPanel())
	mainSplit := container.NewHSplit(left, preview)
	mainSplit.Offset = 0.32

	return container.NewBorder(nil, bottom, nil, nil, mainSplit)
}

func (ms *musicState) buildSettingsPanel() fyne.CanvasObject {
	tracksTab := ms.buildTracksTab()
	exportTab, exRefresh := ms.buildExportTab()
	ms.settingsRefresh = exRefresh

	tabs := container.NewAppTabs(
		container.NewTabItem("Tracks", tracksTab),
		container.NewTabItem("Export", exportTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return tabs
}

func (ms *musicState) buildTracksTab() fyne.CanvasObject {
	ms.songList = widget.NewList(
		func() int { return len(ms.songs) },
		func() fyne.CanvasObject {
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), nil)
			del.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, nil, del, statusLabel("template"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*fyne.Container)
			lbl := row.Objects[0].(*lineLabel)
			del := row.Objects[1].(*widget.Button)
			idx := int(i)
			del.OnTapped = func() { ms.removeAt(idx) }
			if idx < len(ms.songs) {
				lbl.SetText(fmt.Sprintf("%d. %s", idx+1, ms.songs[idx].DisplayTitle()))
			}
		},
	)
	ms.songList.OnSelected = func(id widget.ListItemID) { ms.selectSong(int(id)) }

	ms.codecSelect = widget.NewSelect(
		[]string{
			core.CodecPCM.String(),
			core.CodecChiptuneUGE.String(),
			core.CodecChiptuneMOD.String(),
			core.CodecChiptuneAuto.String(),
		},
		func(v string) {
			if ms.selected >= 0 {
				ms.songs[ms.selected].Codec = codecFromString(v)
				ms.updateSize()
			}
		},
	)
	applyAllBtn := widget.NewButton("Use this codec for all tracks", func() {
		if ms.selected < 0 {
			return
		}
		c := ms.songs[ms.selected].Codec
		for _, sg := range ms.songs {
			sg.Codec = c
		}
		ms.setStatus("Codec applied to all tracks: " + c.String())
	})

	ms.titleEntry = widget.NewEntry()
	ms.titleEntry.SetPlaceHolder("Title shown on the device")
	ms.titleEntry.OnChanged = func(v string) {
		if ms.selected < 0 {
			return
		}
		ms.songs[ms.selected].Title = v
		ms.songList.Refresh()
		ms.updatePreview()
	}

	trackSec := newSection("Selected Track", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Title", ms.titleEntry),
			widget.NewFormItem("Codec", ms.codecSelect),
		),
		applyAllBtn,
		hintLabel("Click the device preview to set cover art (16×14 tiles)."),
	))
	ms.setTrackControlsEnabled(false)

	listTile := newTile(container.NewBorder(secLabel("Playlist"), nil, nil, nil, ms.songList))
	return container.NewBorder(nil, trackSec.tile, nil, nil, listTile)
}

func (ms *musicState) setTrackControlsEnabled(on bool) {
	if on {
		ms.titleEntry.Enable()
		ms.codecSelect.Enable()
	} else {
		ms.titleEntry.Disable()
		ms.codecSelect.Disable()
	}
}

func (ms *musicState) buildExportTab() (fyne.CanvasObject, func()) {
	rateRadio := widget.NewRadioGroup(nil, nil)
	rateRadio.Required = true
	// setRates offers the target's sample rates, keeping the chosen one when
	// the target has it.
	setRates := func() {
		dmg := ms.cfg.MusicTarget == core.TargetDMG
		rates := music.PCMRatesCGB
		if dmg {
			rates = music.PCMRatesDMG
		}
		labels := make([]string, len(rates))
		sel := ""
		for i, r := range rates {
			labels[i] = rateLabel(r, rates)
			if r == ms.cfg.PCMRate {
				sel = labels[i]
			}
		}
		if sel == "" {
			ms.cfg.PCMRate = music.DefaultPCMRate(dmg)
			sel = rateLabel(ms.cfg.PCMRate, rates)
		}
		rateRadio.OnChanged = nil
		rateRadio.Options = labels
		rateRadio.SetSelected(sel)
		rateRadio.OnChanged = func(v string) {
			for _, r := range rates {
				if rateLabel(r, rates) == v {
					ms.cfg.PCMRate = r
				}
			}
			ms.updateSize()
		}
		rateRadio.Refresh()
	}

	targetRadio := widget.NewRadioGroup(
		[]string{core.TargetCGBFast.String(), core.TargetDMG.String()},
		func(v string) {
			if v == core.TargetDMG.String() {
				ms.cfg.MusicTarget = core.TargetDMG
			} else {
				ms.cfg.MusicTarget = core.TargetCGBFast
			}
			setRates()
			ms.updatePreview()
			ms.updateSize()
		},
	)
	targetRadio.Required = true
	targetRadio.SetSelected(ms.cfg.MusicTarget.String())
	setRates()
	compressRadio := widget.NewRadioGroup([]string{compressOn, compressOff}, func(v string) {
		ms.cfg.MusicUncompressed = v == compressOff
		ms.updateSize()
	})
	compressRadio.Required = true
	compressRadio.SetSelected(compressOn)
	if ms.cfg.MusicUncompressed {
		compressRadio.SetSelected(compressOff)
	}

	if ms.cfg.MaxMusicMB <= 0 {
		ms.cfg.MaxMusicMB = 8
	}
	sizeSelect := widget.NewSelect([]string{"1 MB", "2 MB", "4 MB", "8 MB"}, func(v string) {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, " MB")); err == nil {
			ms.cfg.MaxMusicMB = n
		}
		ms.updateSize()
	})
	sizeSelect.SetSelected(fmt.Sprintf("%d MB", ms.cfg.MaxMusicMB))
	overflowHint := hintLabel("")
	overflowSelect := widget.NewSelect(musicOverflowLabels, func(v string) {
		for i, l := range musicOverflowLabels {
			if l == v {
				ms.cfg.MusicOverflow = i
				overflowHint.SetText(musicOverflowHints[i])
			}
		}
		ms.updateSize()
	})
	overflowSelect.SetSelectedIndex(ms.cfg.MusicOverflow)

	ms.sizeLabel = widget.NewLabel("")
	ms.sizeLabel.Wrapping = fyne.TextWrapWord

	nameEntry := widget.NewEntry()
	nameEntry.SetText(ms.cfg.Name)
	nameEntry.SetPlaceHolder("music")
	nameEntry.OnChanged = func(v string) { ms.cfg.Name = sanitizeName(v) }

	gbdkEntry := widget.NewEntry()
	gbdkEntry.SetText(ms.cfg.GBDKHome)
	gbdkEntry.SetPlaceHolder(core.DefaultConfig().GBDKHome)
	gbdkEntry.OnChanged = func(v string) { ms.cfg.GBDKHome = v }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(ms.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("out")
	outDirEntry.OnChanged = func(v string) { ms.cfg.OutputDir = v }

	ms.compileBtn = widget.NewButtonWithIcon("Compile ROM (.gbc)", theme.MediaPlayIcon(), func() { ms.doCompile() })
	ms.compileBtn.Importance = widget.HighImportance
	ms.compileBtn.Disable()
	actionBox := container.NewVBox(
		ms.compileBtn,
		widget.NewButtonWithIcon("Open output folder", theme.FolderIcon(), func() { openOutputFolder(ms.cfg.OutputDir) }),
	)

	column := panelColumn(
		newSection("Build Target", targetRadio).tile,
		newSection("PCM Sample Rate", container.NewVBox(rateRadio,
			hintLabel("43 output levels, noise-shaped. Higher rates are clearer and take more ROM."))).tile,
		newSection("Compression", compressRadio).tile,
		newSection("Cartridge", container.NewVBox(
			widget.NewForm(
				widget.NewFormItem("Max ROM", sizeSelect),
				widget.NewFormItem("If too long", overflowSelect),
			),
			overflowHint,
			ms.sizeLabel,
		)).tile,
		newSection("Output", container.NewVBox(
			widget.NewForm(
				widget.NewFormItem("Name", nameEntry),
				widget.NewFormItem("GBDK Home", gbdkEntry),
				widget.NewFormItem("Output Dir", outDirEntry),
			),
			hintLabel("PCM needs ffmpeg. .uge/.mod tracks stay silent for now (no driver)."),
		)).tile,
	)

	refresh := func() {
		nameEntry.SetText(ms.cfg.Name)
		gbdkEntry.SetText(ms.cfg.GBDKHome)
		outDirEntry.SetText(ms.cfg.OutputDir)
	}
	return container.NewBorder(nil, actionBox, nil, nil, column), refresh
}

func (ms *musicState) removeAt(i int) {
	if i < 0 || i >= len(ms.songs) {
		return
	}
	ms.songs = append(ms.songs[:i], ms.songs[i+1:]...)
	switch {
	case ms.selected == i:
		ms.selected = -1
		ms.songList.UnselectAll()
		ms.setTrackControlsEnabled(false)
		ms.updatePreview()
	case ms.selected > i:
		ms.selected--
	}
	ms.songList.Refresh()
	if len(ms.songs) == 0 {
		ms.compileBtn.Disable()
	}
	ms.updatePreview()
	ms.updateSize()
	ms.setStatus("Track removed.")
}

func (ms *musicState) selectSong(i int) {
	if i < 0 || i >= len(ms.songs) {
		ms.selected = -1
		return
	}
	ms.selected = i
	sg := ms.songs[i]
	ms.setTrackControlsEnabled(true)
	ms.codecSelect.SetSelected(sg.Codec.String())
	ms.titleEntry.SetText(sg.Title)
	ms.updatePreview()
}

func (ms *musicState) step(d int) {
	if len(ms.songs) == 0 {
		return
	}
	n := ((ms.selected+d)%len(ms.songs) + len(ms.songs)) % len(ms.songs)
	ms.songList.Select(n)
}

func (ms *musicState) updatePreview() {
	if ms.selected < 0 {
		ms.coverCanvas.Image = nil
		ms.coverCanvas.Refresh()
		ms.titleLabel.SetText("No track selected")
		return
	}
	sg := ms.songs[ms.selected]
	ms.titleLabel.SetText(sg.DisplayTitle())
	ms.coverCanvas.Image = music.RenderDevicePreview(sg, ms.selected, len(ms.songs), ms.cfg.MusicTarget == core.TargetDMG)
	ms.coverCanvas.Refresh()
}

// setStatus is safe to call from any goroutine.
func (ms *musicState) setStatus(msg string) {
	setText(ms.statusBar, msg)
}

func codecFromString(s string) core.MusicCodec {
	switch s {
	case core.CodecChiptuneUGE.String():
		return core.CodecChiptuneUGE
	case core.CodecChiptuneMOD.String():
		return core.CodecChiptuneMOD
	case core.CodecChiptuneAuto.String():
		return core.CodecChiptuneAuto
	default:
		return core.CodecPCM
	}
}

func codecForExt(ext string) core.MusicCodec {
	switch strings.ToLower(ext) {
	case ".uge":
		return core.CodecChiptuneUGE
	case ".mod":
		return core.CodecChiptuneMOD
	case ".mid", ".midi":
		return core.CodecChiptuneAuto
	default:
		return core.CodecPCM
	}
}

// Threading: file dialog callbacks and the build run on background goroutines;
// musicState and widgets are only touched on the UI thread via fyne.Do.

func (ms *musicState) addSong() {
	pickFile("Add Song", false, fileFilter{"Audio / MIDI / Tracker", songExts}, ms.setStatus, ms.addSongPath)
}

// addSongPath appends a track; safe to call from any goroutine.
func (ms *musicState) addSongPath(path string) {
	sg := &core.Song{
		Path:  path,
		Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Codec: codecForExt(filepath.Ext(path)),
	}
	if d, err := ffmpeg.ProbeDuration(path); err == nil {
		sg.Seconds = d
	}
	uiDo(func() {
		ms.songs = append(ms.songs, sg)
		ms.songList.Refresh()
		ms.compileBtn.Enable()
		ms.songList.Select(len(ms.songs) - 1)
		ms.updateSize()
		ms.setStatus(fmt.Sprintf("Added: %s  (%d track(s))", sg.DisplayTitle(), len(ms.songs)))
	})
}

func (ms *musicState) setCover() {
	if ms.selected < 0 {
		ms.setStatus("Select a track first.")
		return
	}
	sg := ms.songs[ms.selected]
	pickFile("Set Cover", false, fileFilter{"Image", imageExts}, ms.setStatus, func(path string) {
		ms.setCoverPath(sg, path)
	})
}

// setCoverPath (background goroutine) decodes path as the cover of sg.
func (ms *musicState) setCoverPath(sg *core.Song, path string) {
	img, err := imaging.DecodeImageFile(path)
	if err != nil {
		ms.setStatus("Cover decode error: " + err.Error())
		return
	}
	uiDo(func() {
		sg.CoverPath = path
		sg.Cover = img
		ms.updatePreview()
		ms.setStatus("Cover set for: " + sg.DisplayTitle())
	})
}

func (ms *musicState) doCompile() {
	if len(ms.songs) == 0 {
		ms.setStatus("Add at least one song.")
		return
	}
	songs := append([]*core.Song(nil), ms.songs...)
	cfg := ms.cfg
	cfg.HiColor = false
	if cfg.MusicTarget == core.TargetDMG {
		cfg.Mode = core.ModeDMG
	} else {
		cfg.Mode = core.ModeCGB
	}
	setProgress := func(v float64) { uiDo(func() { ms.progress.SetValue(v) }) }

	runJob(&ms.busy, ms.setStatus, func() {
		uiDo(func() {
			ms.progress.SetValue(0)
			ms.progress.Show()
		})
		defer uiDo(ms.progress.Hide)

		ms.setStatus("Encoding music project…")
		res, err := music.Build(cfg, songs, ms.setStatus, func(done, total int) {
			if total > 0 {
				setProgress(float64(done) / float64(total))
			}
		})
		switch {
		case err != nil:
			ms.setStatus("Music export error: " + err.Error())
		case !res.Success:
			ms.setStatus("Compile failed — see output.")
		default:
			msg := "Compiled OK → " + strings.Join(res.ROMs, ", ")
			if len(res.Notes) > 0 {
				msg += " — " + strings.Join(res.Notes, "; ")
			}
			ms.setStatus(msg)
			return
		}
		// Only a failure needs the compiler's log.
		if res.Output != "" {
			uiDo(func() { showOutputDialog(ms.win, "Music Compile Output", res.Output) })
		}
	})
}

// rateLabel names a sample rate in the export settings.
func rateLabel(r int, rates []int) string {
	switch r {
	case rates[len(rates)-1]:
		return fmt.Sprintf("%d Hz — clearest", r)
	case rates[0]:
		return fmt.Sprintf("%d Hz — longest", r)
	}
	return fmt.Sprintf("%d Hz", r)
}

const (
	compressOn  = "DPCM 4-bit — half the size (recommended)"
	compressOff = "Off — 8-bit, a little clearer"
)

var musicOverflowLabels = []string{"Trim the playlist", "Fit by lowering the sample rate", "Split into several ROMs"}

var musicOverflowHints = []string{
	"Cuts the track that does not fit (with a fade) and leaves out the ones after it.",
	"Uses the highest sample rate at which every track fits.",
	"Several ROMs: name_part1, name_part2…",
}

// updateSize shows how much of Max ROM the tracks take.
func (ms *musicState) updateSize() {
	if ms.sizeLabel == nil {
		return
	}
	total, unknown := 0.0, 0
	for _, sg := range ms.songs {
		if sg.Codec != core.CodecPCM {
			continue
		}
		if sg.Seconds <= 0 {
			unknown++
		}
		total += sg.Seconds
	}
	perSample := 1.0
	if !ms.cfg.MusicUncompressed {
		perSample = 0.5
	}
	mb := total * float64(ms.cfg.PCMRate) * perSample / (1 << 20)
	maxMB := max(ms.cfg.MaxMusicMB, 1)
	text := fmt.Sprintf("%d:%02d of audio ≈ %.1f MB of %d MB", int(total)/60, int(total)%60, mb, maxMB)
	if unknown > 0 {
		text += fmt.Sprintf(" (%d track(s) of unknown length)", unknown)
	}
	if mb > float64(maxMB)*0.97 {
		text += " — too long: " + strings.ToLower(musicOverflowLabels[ms.cfg.MusicOverflow][:1]) + musicOverflowLabels[ms.cfg.MusicOverflow][1:]
	}
	ms.sizeLabel.SetText(text)
}
