package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	sqDialog "github.com/sqweek/dialog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type musicState struct {
	cfg ConvertConfig
	win fyne.Window

	songs    []*Song
	selected int

	playing bool

	songList    *widget.List
	codecSelect *widget.Select
	titleEntry  *widget.Entry
	coverCanvas *canvas.Image
	titleLabel  *widget.Label
	playBtn     *widget.Button
	statusBar   *widget.Label
	compileBtn  *widget.Button
	progress    *widget.ProgressBar

	settingsRefresh func()
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

func (sg *Song) displayTitle() string {
	if strings.TrimSpace(sg.Title) != "" {
		return sg.Title
	}
	if sg.Path != "" {
		return strings.TrimSuffix(filepath.Base(sg.Path), filepath.Ext(sg.Path))
	}
	return "(untitled)"
}

func buildMusicEditorContent(win fyne.Window, mainCfg ConvertConfig) (fyne.CanvasObject, *musicState) {
	ms := &musicState{
		cfg:      mainCfg,
		win:      win,
		selected: -1,
	}
	ms.cfg.Mode = ModeCGB
	return ms.buildUI(), ms
}

func (ms *musicState) buildUI() fyne.CanvasObject {

	ms.coverCanvas = canvas.NewImageFromImage(nil)
	ms.coverCanvas.FillMode = canvas.ImageFillContain
	ms.coverCanvas.SetMinSize(fyne.NewSize(256, 224))

	ms.titleLabel = widget.NewLabelWithStyle("—", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	coverTap := newTappableImage(ms.coverCanvas, func() { ms.setCover() })
	coverHint := widget.NewLabelWithStyle("Click the cover to set artwork", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	prevBtn := widget.NewButton("◀", func() { ms.step(-1) })
	nextBtn := widget.NewButton("▶", func() { ms.step(+1) })
	ms.playBtn = widget.NewButton("▶  Play", func() { ms.togglePlay() })
	transport := container.NewHBox(prevBtn, ms.playBtn, nextBtn)

	deviceLbl := widget.NewLabelWithStyle("Device Preview", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	preview := container.NewBorder(
		deviceLbl,
		container.NewVBox(ms.titleLabel, container.NewCenter(transport), coverHint),
		nil, nil,
		coverTap,
	)

	addBtn := widget.NewButton("Add Song…", func() { ms.addSong() })
	addBtn.Importance = widget.HighImportance
	topBar := container.NewHBox(addBtn)

	ms.statusBar = widget.NewLabel("Add songs to begin.")
	ms.progress = widget.NewProgressBar()
	ms.progress.Hide()
	bottom := container.NewVBox(ms.progress, ms.statusBar)

	mainSplit := container.NewHSplit(ms.buildSettingsPanel(), preview)
	mainSplit.Offset = 0.30

	return container.NewBorder(topBar, bottom, nil, nil, mainSplit)
}

func (ms *musicState) buildSettingsPanel() fyne.CanvasObject {
	tracksTab := ms.buildTracksTab()
	exportTab, exRefresh := ms.buildExportTab()

	tabs := container.NewAppTabs(
		container.NewTabItem("Tracks", tracksTab),
		container.NewTabItem("Export", exportTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	ms.settingsRefresh = exRefresh
	return tabs
}

func (ms *musicState) buildTracksTab() fyne.CanvasObject {
	ms.songList = widget.NewList(
		func() int { return len(ms.songs) },
		func() fyne.CanvasObject {
			del := widget.NewButton("✕", nil)
			del.Importance = widget.LowImportance
			return container.NewHBox(del, widget.NewLabel("template"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*fyne.Container)
			del := row.Objects[0].(*widget.Button)
			lbl := row.Objects[1].(*widget.Label)
			idx := int(i)
			del.OnTapped = func() { ms.removeAt(idx) }
			if idx < len(ms.songs) {
				lbl.SetText(fmt.Sprintf("%d. %s", idx+1, ms.songs[idx].displayTitle()))
			}
		},
	)
	ms.songList.OnSelected = func(id widget.ListItemID) { ms.selectSong(int(id)) }

	listScroll := container.NewVScroll(ms.songList)
	listScroll.SetMinSize(fyne.NewSize(220, 160))

	ms.codecSelect = widget.NewSelect(
		[]string{
			CodecPCM.String(),
			CodecChiptuneUGE.String(),
			CodecChiptuneMOD.String(),
			CodecChiptuneAuto.String(),
		},
		func(v string) {
			if ms.selected < 0 {
				return
			}
			ms.songs[ms.selected].Codec = codecFromString(v)
		},
	)
	applyAllBtn := widget.NewButton("Apply codec to all", func() {
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
	ms.titleEntry.SetPlaceHolder("Track title (shown on device)")
	ms.titleEntry.OnChanged = func(v string) {
		if ms.selected < 0 {
			return
		}
		ms.songs[ms.selected].Title = v
		ms.songList.Refresh()
		ms.updatePreview()
	}

	panel := container.NewVBox(
		secLabel("Tracks"),
		listScroll,

		widget.NewSeparator(),
		secLabel("Selected Track"),
		widget.NewForm(
			widget.NewFormItem("Name", ms.titleEntry),
			widget.NewFormItem("Codec", ms.codecSelect),
		),
		container.NewHBox(applyAllBtn),
		widget.NewLabel("Rename: edit “Name” above (shows on device).\nCover: click the device preview (16×14 tiles)."),
	)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(240, 100))
	return scroll
}

func (ms *musicState) buildExportTab() (fyne.CanvasObject, func()) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(ms.cfg.Name)
	nameEntry.SetPlaceHolder("music")
	nameEntry.OnChanged = func(v string) { ms.cfg.Name = sanitizeName(v) }

	gbdkEntry := widget.NewEntry()
	gbdkEntry.SetText(ms.cfg.GBDKHome)
	gbdkEntry.SetPlaceHolder(`C:\Bin\gbdk`)
	gbdkEntry.OnChanged = func(v string) { ms.cfg.GBDKHome = v }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(ms.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("out")
	outDirEntry.OnChanged = func(v string) { ms.cfg.OutputDir = v }

	const rate4 = "4096 Hz — lighter ROM"
	const rate8 = "8192 Hz — clearer"
	const rate9 = "9198 Hz — GBVP2 parity"
	rateRadio := widget.NewRadioGroup([]string{rate4, rate8, rate9}, func(v string) {
		switch v {
		case rate9:
			ms.cfg.PCMRate = 9198
		case rate8:
			ms.cfg.PCMRate = 8192
		default:
			ms.cfg.PCMRate = 4096
		}
	})
	switch ms.cfg.PCMRate {
	case 9198:
		rateRadio.SetSelected(rate9)
	case 8192:
		rateRadio.SetSelected(rate8)
	default:
		rateRadio.SetSelected(rate4)
	}

	targetRadio := widget.NewRadioGroup(
		[]string{TargetCGBFast.String(), TargetDMG.String()},
		func(v string) {
			if v == TargetDMG.String() {
				ms.cfg.MusicTarget = TargetDMG
			} else {
				ms.cfg.MusicTarget = TargetCGBFast
			}
		},
	)
	targetRadio.SetSelected(ms.cfg.MusicTarget.String())

	panel := container.NewVBox(
		secLabel("Build Target"),
		targetRadio,

		widget.NewSeparator(),
		secLabel("PCM Sample Rate"),
		rateRadio,

		widget.NewSeparator(),
		secLabel("Output"),
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("GBDK Home", gbdkEntry),
			widget.NewFormItem("Output Dir", outDirEntry),
		),
		widget.NewLabel("PCM needs ffmpeg on PATH.\nChiptune (.uge/.mod) needs the\nrespective driver/tool."),
	)

	openDirBtn := widget.NewButton("Open output folder", func() { openOutputFolder(ms.cfg.OutputDir) })
	ms.compileBtn = widget.NewButton("Compile ROM (.gbc)", func() { ms.doCompile() })
	ms.compileBtn.Importance = widget.HighImportance
	actionBox := container.NewVBox(ms.compileBtn, openDirBtn)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(240, 100))

	refresh := func() {
		nameEntry.SetText(ms.cfg.Name)
		gbdkEntry.SetText(ms.cfg.GBDKHome)
		outDirEntry.SetText(ms.cfg.OutputDir)
	}
	return container.NewBorder(nil, actionBox, nil, nil, scroll), refresh
}

func (ms *musicState) addSong() {
	go func() {
		path, err := sqDialog.File().
			Filter("Audio / MIDI / Tracker", "mp3", "mp4", "wav", "ogg", "m4a", "flac", "mid", "midi", "uge", "mod").
			Title("Add Song").
			Load()
		if err != nil {
			if err != sqDialog.ErrCancelled {
				ms.setStatus("File error: " + err.Error())
			}
			return
		}
		sg := &Song{
			Path:  path,
			Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Codec: codecForExt(filepath.Ext(path)),
		}
		ms.songs = append(ms.songs, sg)
		ms.songList.Refresh()
		ms.compileBtn.Enable()
		idx := len(ms.songs) - 1
		ms.songList.Select(idx)
		ms.setStatus(fmt.Sprintf("Added: %s  (%d track(s))", sg.displayTitle(), len(ms.songs)))
	}()
}

func (ms *musicState) setCover() {
	if ms.selected < 0 {
		ms.setStatus("Select a track first.")
		return
	}
	go func() {
		path, err := sqDialog.File().
			Filter("Image", "png", "jpg", "jpeg").
			Title("Set Cover").
			Load()
		if err != nil {
			if err != sqDialog.ErrCancelled {
				ms.setStatus("File error: " + err.Error())
			}
			return
		}
		img, err := decodeImageFile(path)
		if err != nil {
			ms.setStatus("Cover decode error: " + err.Error())
			return
		}
		sg := ms.songs[ms.selected]
		sg.CoverPath = path
		sg.cover = img
		ms.updatePreview()
		ms.setStatus("Cover set for: " + sg.displayTitle())
	}()
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
		ms.updatePreview()
	case ms.selected > i:
		ms.selected--
	}
	ms.songList.Refresh()
	if len(ms.songs) == 0 {
		ms.compileBtn.Disable()
	}
	ms.setStatus("Track removed.")
}

func (ms *musicState) selectSong(i int) {
	if i < 0 || i >= len(ms.songs) {
		ms.selected = -1
		return
	}
	ms.selected = i
	sg := ms.songs[i]
	ms.codecSelect.SetSelected(sg.Codec.String())
	ms.titleEntry.SetText(sg.Title)
	ms.updatePreview()
}

func (ms *musicState) step(d int) {
	if len(ms.songs) == 0 {
		return
	}
	n := ms.selected + d
	if n < 0 {
		n = len(ms.songs) - 1
	}
	n %= len(ms.songs)
	if n < 0 {
		n += len(ms.songs)
	}
	ms.songList.Select(n)
}

func (ms *musicState) togglePlay() {
	ms.playing = !ms.playing
	if ms.playing {
		ms.playBtn.SetText("⏸  Stop")
	} else {
		ms.playBtn.SetText("▶  Play")
	}
}

func (ms *musicState) updatePreview() {
	if ms.selected < 0 {
		ms.coverCanvas.Image = nil
		ms.coverCanvas.Refresh()
		ms.titleLabel.SetText("—")
		return
	}
	sg := ms.songs[ms.selected]
	ms.titleLabel.SetText(sg.displayTitle())
	ms.coverCanvas.Image = renderDevicePreview(sg)
	ms.coverCanvas.Refresh()
}

func (ms *musicState) doCompile() {
	if len(ms.songs) == 0 {
		ms.setStatus("Add at least one song.")
		return
	}
	go func() {
		ms.progress.SetValue(0)
		ms.progress.Show()
		defer ms.progress.Hide()

		ms.setStatus("Encoding music project…")
		banks, err := ExportGBDKMusic(ms.cfg, ms.songs, ms.setStatus)
		if err != nil {
			ms.setStatus("Music export error: " + err.Error())
			return
		}
		ms.cfg.ROMBanks = banks
		ms.cfg.HiColor = false
		if ms.cfg.MusicTarget == TargetDMG {
			ms.cfg.Mode = ModeDMG
		} else {
			ms.cfg.Mode = ModeCGB
		}
		ms.setStatus("Compiling ROM…")
		res := CompileGBWithProgress(ms.cfg, func(done, total int) {
			if total > 0 {
				ms.progress.SetValue(float64(done) / float64(total))
			}
		})
		if res.Success {
			ms.setStatus("Compiled OK → " + res.ROMPath)
		} else {
			ms.setStatus("Compile failed — see output.")
		}
		showOutputDialog(ms.win, "Music Compile Output", res.Output)
	}()
}

func (ms *musicState) setStatus(msg string) {
	if ms.statusBar != nil {
		ms.statusBar.SetText(msg)
	}
}

func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return png.Decode(f)
	case ".jpg", ".jpeg":
		return jpeg.Decode(f)
	default:
		img, _, err := image.Decode(f)
		return img, err
	}
}

func codecFromString(s string) MusicCodec {
	switch s {
	case CodecChiptuneUGE.String():
		return CodecChiptuneUGE
	case CodecChiptuneMOD.String():
		return CodecChiptuneMOD
	case CodecChiptuneAuto.String():
		return CodecChiptuneAuto
	default:
		return CodecPCM
	}
}

func codecForExt(ext string) MusicCodec {
	switch strings.ToLower(ext) {
	case ".uge":
		return CodecChiptuneUGE
	case ".mod":
		return CodecChiptuneMOD
	case ".mid", ".midi":
		return CodecChiptuneAuto
	default:
		return CodecPCM
	}
}
