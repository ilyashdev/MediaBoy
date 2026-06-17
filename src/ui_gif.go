package main

import (
	"fmt"
	"image"
	"image/gif"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sqDialog "github.com/sqweek/dialog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// gifEditorState holds all state for the GIF editor window.
type gifEditorState struct {
	cfg ConvertConfig
	win fyne.Window

	// source
	srcFrames []*image.RGBA
	delays    []int // centiseconds per frame (from GIF header)

	// processed (populated by processAllFrames)
	procFrames  []image.Image // preprocessed 160×144 frames
	tilefFrames []image.Image // tilefied preview images
	videoData   *VideoData

	// playback
	currentFrame int
	playing      bool
	playStop     chan struct{}

	// navigation
	onBack func()

	// widgets
	cropWidget  *CropWidget
	outCanvas   *canvas.Image
	frameSlider *widget.Slider
	frameLabel  *widget.Label
	gifInfoLbl  *widget.Label
	statusBar   *widget.Label
	processBtn  *widget.Button
	exportBtn   *widget.Button
	compileBtn  *widget.Button
	playBtn     *widget.Button
	cropInfoLbl *widget.Label
}

// ── Entry point ───────────────────────────────────────────────────────────────

// buildGifEditorContent builds the GIF editor as content for the main window
// (single-window app). onBack is called to restore the photo editor view.
func buildGifEditorContent(win fyne.Window, mainCfg ConvertConfig, onBack func()) fyne.CanvasObject {
	gs := &gifEditorState{
		cfg:    mainCfg,
		win:    win,
		onBack: onBack,
	}
	return gs.buildUI()
}

// ── UI construction ───────────────────────────────────────────────────────────

func (gs *gifEditorState) buildUI() fyne.CanvasObject {
	// Output canvas (right side)
	gs.outCanvas = canvas.NewImageFromImage(nil)
	gs.outCanvas.FillMode = canvas.ImageFillContain
	gs.outCanvas.SetMinSize(fyne.NewSize(300, 220))

	// CropWidget over input frames (left side)
	gs.cropWidget = NewCropWidget(func(r image.Rectangle) {
		gs.cfg.CropRect = r
		gs.cfg.CropEnabled = true
		gs.refreshCropInfo()
	})
	gs.cropWidget.FixAspect = true
	gs.cropWidget.SnapFunc = gs.snapGB

	inLbl := widget.NewLabelWithStyle("Input Frame", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	outLbl := widget.NewLabelWithStyle("Output Preview", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	inputPanel := container.NewBorder(inLbl, nil, nil, nil, gs.cropWidget)
	outputPanel := container.NewBorder(outLbl, nil, nil, nil, gs.outCanvas)
	imageArea := container.NewHSplit(inputPanel, outputPanel)
	imageArea.Offset = 0.5

	// Frame controls
	gs.gifInfoLbl = widget.NewLabel("No GIF loaded")
	gs.frameLabel = widget.NewLabel("")

	gs.frameSlider = widget.NewSlider(0, 1)
	gs.frameSlider.Step = 1
	gs.frameSlider.OnChanged = func(v float64) {
		if len(gs.srcFrames) == 0 || gs.playing {
			return
		}
		gs.showFrame(int(v))
	}

	prevBtn := widget.NewButton("◀", func() {
		if len(gs.srcFrames) == 0 {
			return
		}
		gs.stopPlay()
		n := gs.currentFrame - 1
		if n < 0 {
			n = len(gs.srcFrames) - 1
		}
		gs.frameSlider.SetValue(float64(n))
		gs.showFrame(n)
	})
	nextBtn := widget.NewButton("▶", func() {
		if len(gs.srcFrames) == 0 {
			return
		}
		gs.stopPlay()
		n := (gs.currentFrame + 1) % len(gs.srcFrames)
		gs.frameSlider.SetValue(float64(n))
		gs.showFrame(n)
	})
	gs.playBtn = widget.NewButton("▶  Play", func() { gs.togglePlay() })

	frameControls := container.NewBorder(
		nil, nil,
		container.NewHBox(prevBtn, gs.playBtn, nextBtn),
		gs.frameLabel,
		gs.frameSlider,
	)
	rightPanel := container.NewBorder(
		nil,
		container.NewVBox(gs.gifInfoLbl, frameControls),
		nil, nil,
		imageArea,
	)

	// Settings panel + toolbar
	gs.processBtn = widget.NewButton("Process All Frames", func() {
		go gs.processAllFrames()
	})
	gs.exportBtn = widget.NewButton("Export GBDK", func() { gs.doExport() })
	gs.exportBtn.Disable()
	gs.compileBtn = widget.NewButton("Compile ROM", func() { go gs.doCompile() })
	gs.compileBtn.Importance = widget.WarningImportance
	gs.compileBtn.Disable()

	openBtn := widget.NewButton("Open GIF", func() { gs.openGIF() })
	openBtn.Importance = widget.HighImportance

	backBtn := widget.NewButton("← Back", func() {
		gs.stopPlay()
		if gs.onBack != nil {
			gs.onBack()
		}
	})

	toolbar := container.NewHBox(
		backBtn,
		widget.NewSeparator(),
		openBtn, gs.processBtn,
		widget.NewSeparator(),
		gs.exportBtn, gs.compileBtn,
	)

	gs.statusBar = widget.NewLabel("Open a GIF to begin.")

	mainSplit := container.NewHSplit(gs.buildSettingsPanel(), rightPanel)
	mainSplit.Offset = 0.24

	return container.NewBorder(toolbar, gs.statusBar, nil, nil, mainSplit)
}

func (gs *gifEditorState) buildSettingsPanel() fyne.CanvasObject {
	// Console
	consoleSelect := widget.NewSelect(
		[]string{"GBC — Color", "GB — Monochrome (DMG)"},
		func(v string) {
			if v == "GB — Monochrome (DMG)" {
				gs.cfg.Mode = ModeDMG
			} else {
				gs.cfg.Mode = ModeCGB
			}
		},
	)
	consoleSelect.SetSelectedIndex(int(gs.cfg.Mode))

	// Scene segmentation threshold: lower → more scenes (more palettes, sharper
	// colour), higher → fewer scenes (palettes shared across frames, smaller ROM).
	sceneVal := widget.NewLabel(fmt.Sprintf("%.2f", gs.cfg.SceneThreshold))
	sceneSlider := widget.NewSlider(0.01, 1.00)
	sceneSlider.Step = 0.01
	sceneSlider.SetValue(gs.cfg.SceneThreshold)
	sceneSlider.OnChanged = func(v float64) {
		gs.cfg.SceneThreshold = v
		sceneVal.SetText(fmt.Sprintf("%.2f", v))
	}

	// Interpolated frames between each consecutive pair → keeps per-step tile
	// churn below the free-slot budget so every transition is a light frame.
	interpVal := widget.NewLabel(fmt.Sprintf("%d", gs.cfg.InterpFrames))
	interpSlider := widget.NewSlider(0, 8)
	interpSlider.Step = 1
	interpSlider.SetValue(float64(gs.cfg.InterpFrames))
	interpSlider.OnChanged = func(v float64) {
		gs.cfg.InterpFrames = int(v)
		interpVal.SetText(fmt.Sprintf("%d", int(v)))
	}

	// Crop
	gs.cropInfoLbl = widget.NewLabel("Draw on input frame to crop")
	resetCropBtn := widget.NewButton("Reset Crop", func() {
		gs.cfg.CropRect = image.Rectangle{}
		gs.cfg.CropEnabled = false
		gs.cropWidget.CropRect = image.Rectangle{}
		gs.cropWidget.Refresh()
		gs.refreshCropInfo()
	})

	// Bilateral
	radiusVal := widget.NewLabel(fmt.Sprintf("%d", gs.cfg.BilateralRadius))
	radiusSlider := widget.NewSlider(0, 32)
	radiusSlider.Step = 1
	radiusSlider.SetValue(float64(gs.cfg.BilateralRadius))
	radiusSlider.OnChanged = func(v float64) {
		gs.cfg.BilateralRadius = int(v)
		radiusVal.SetText(fmt.Sprintf("%d", int(v)))
	}
	sigmaVal := widget.NewLabel(fmt.Sprintf("%.0f", gs.cfg.BilateralSigma))
	sigmaSlider := widget.NewSlider(1, 150)
	sigmaSlider.Step = 1
	sigmaSlider.SetValue(gs.cfg.BilateralSigma)
	sigmaSlider.OnChanged = func(v float64) {
		gs.cfg.BilateralSigma = v
		sigmaVal.SetText(fmt.Sprintf("%.0f", v))
	}
	bilBody := container.NewVBox(widget.NewForm(
		widget.NewFormItem("Radius", sliderRow(radiusSlider, radiusVal)),
		widget.NewFormItem("Sigma σ", sliderRow(sigmaSlider, sigmaVal)),
	))
	bilSection := toggleSection("Bilateral Filter", gs.cfg.BilateralEnabled, func(v bool) {
		gs.cfg.BilateralEnabled = v
	}, bilBody)

	// Downscale
	scalingSelect := widget.NewSelect(
		[]string{"Median", "Bilinear", "Nearest"},
		func(v string) {
			switch v {
			case "Bilinear":
				gs.cfg.Scaling = ScalingBilinear
			case "Nearest":
				gs.cfg.Scaling = ScalingNearest
			default:
				gs.cfg.Scaling = ScalingMedian
			}
		},
	)
	scalingSelect.SetSelectedIndex(int(gs.cfg.Scaling))

	// Sharpen
	sharpenVal := widget.NewLabel(fmt.Sprintf("%.1f", gs.cfg.SharpenAmount))
	sharpenSlider := widget.NewSlider(0, 3)
	sharpenSlider.Step = 0.1
	sharpenSlider.SetValue(gs.cfg.SharpenAmount)
	sharpenSlider.OnChanged = func(v float64) {
		gs.cfg.SharpenAmount = v
		sharpenVal.SetText(fmt.Sprintf("%.1f", v))
	}
	sharpenSection := toggleSection("Sharpen", gs.cfg.SharpenEnabled, func(v bool) {
		gs.cfg.SharpenEnabled = v
	}, widget.NewForm(widget.NewFormItem("Amount", sliderRow(sharpenSlider, sharpenVal))))

	// Posterize
	postVal := widget.NewLabel(strconv.Itoa(gs.cfg.PosterizeLevels))
	postSlider := widget.NewSlider(2, 16)
	postSlider.Step = 1
	postSlider.SetValue(float64(gs.cfg.PosterizeLevels))
	postSlider.OnChanged = func(v float64) {
		gs.cfg.PosterizeLevels = int(v)
		postVal.SetText(strconv.Itoa(int(v)))
	}
	posterizeSection := toggleSection("Posterize", gs.cfg.PosterizeEnabled, func(v bool) {
		gs.cfg.PosterizeEnabled = v
	}, widget.NewForm(widget.NewFormItem("Levels", sliderRow(postSlider, postVal))))

	// Dithering
	ditherSection := toggleSection("Dithering", gs.cfg.DitheringEnabled, func(v bool) {
		gs.cfg.DitheringEnabled = v
	}, gs.buildDitheringBody())

	// GBDK paths
	nameEntry := widget.NewEntry()
	nameEntry.SetText(gs.cfg.Name)
	nameEntry.OnChanged = func(v string) { gs.cfg.Name = v }

	gbdkEntry := widget.NewEntry()
	gbdkEntry.SetText(gs.cfg.GBDKHome)
	gbdkEntry.SetPlaceHolder(`C:\Bin\gbdk`)
	gbdkEntry.OnChanged = func(v string) { gs.cfg.GBDKHome = v }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(gs.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("gbdk_out")
	outDirEntry.OnChanged = func(v string) { gs.cfg.OutputDir = v }

	panel := container.NewVBox(
		secLabel("Console"),
		widget.NewForm(widget.NewFormItem("Type", consoleSelect)),

		widget.NewSeparator(),
		secLabel("Scenes  (shared palettes)"),
		widget.NewForm(
			widget.NewFormItem("Threshold", sliderRow(sceneSlider, sceneVal)),
			widget.NewFormItem("Interp frames", sliderRow(interpSlider, interpVal)),
		),

		widget.NewSeparator(),
		secLabel("Crop  (auto-snap 10:9)"),
		gs.cropInfoLbl,
		container.NewHBox(resetCropBtn),

		widget.NewSeparator(),
		bilSection,

		widget.NewSeparator(),
		widget.NewForm(widget.NewFormItem("Downscale", scalingSelect)),

		widget.NewSeparator(),
		sharpenSection,

		widget.NewSeparator(),
		posterizeSection,

		widget.NewSeparator(),
		ditherSection,

		widget.NewSeparator(),
		secLabel("GBDK"),
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("GBDK Home", gbdkEntry),
			widget.NewFormItem("Output Dir", outDirEntry),
		),
	)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(220, 100))
	return scroll
}

func (gs *gifEditorState) buildDitheringBody() fyne.CanvasObject {
	ditherLabels := []string{"None", "Bayer", "Floyd-Steinberg", "Atkinson"}

	strengthVal := widget.NewLabel(fmt.Sprintf("%.2f", gs.cfg.DitheringStrength))
	strengthSlider := widget.NewSlider(0, 1)
	strengthSlider.Step = 0.05
	strengthSlider.SetValue(gs.cfg.DitheringStrength)
	strengthSlider.OnChanged = func(v float64) {
		gs.cfg.DitheringStrength = v
		strengthVal.SetText(fmt.Sprintf("%.2f", v))
	}

	levelsEntry := widget.NewEntry()
	levelsEntry.SetText(strconv.Itoa(gs.cfg.DitheringLevels))
	levelsEntry.SetPlaceHolder("4")
	levelsEntry.OnChanged = func(v string) {
		if l, err := strconv.Atoi(v); err == nil && l >= 2 && l <= 64 {
			gs.cfg.DitheringLevels = l
		}
	}

	levelsForm := widget.NewForm(widget.NewFormItem("Levels (FS/Atk)", levelsEntry))
	if gs.cfg.Dithering == DitheringNone || gs.cfg.Dithering == DitheringBayer {
		levelsForm.Hide()
	}

	ditherSelect := widget.NewSelect(ditherLabels, func(v string) {
		switch v {
		case "Bayer":
			gs.cfg.Dithering = DitheringBayer
			levelsForm.Hide()
		case "Floyd-Steinberg":
			gs.cfg.Dithering = DitheringFloydSteinberg
			levelsForm.Show()
		case "Atkinson":
			gs.cfg.Dithering = DitheringAtkinson
			levelsForm.Show()
		default:
			gs.cfg.Dithering = DitheringNone
			levelsForm.Hide()
		}
	})
	ditherSelect.SetSelectedIndex(int(gs.cfg.Dithering))

	return container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Type", ditherSelect),
			widget.NewFormItem("Strength", sliderRow(strengthSlider, strengthVal)),
		),
		levelsForm,
	)
}

// ── File loading ──────────────────────────────────────────────────────────────

func (gs *gifEditorState) openGIF() {
	go func() {
		path, err := sqDialog.File().
			Filter("GIF Files", "gif").
			Title("Open GIF").
			Load()
		if err != nil {
			if err != sqDialog.ErrCancelled {
				gs.setStatus("File error: " + err.Error())
			}
			return
		}
		gs.loadGIF(path)
	}()
}

func (gs *gifEditorState) loadGIF(path string) {
	f, err := os.Open(path)
	if err != nil {
		gs.setStatus("Cannot open: " + err.Error())
		return
	}
	defer f.Close()

	m, err := gif.DecodeAll(f)
	if err != nil {
		gs.setStatus("GIF decode error: " + err.Error())
		return
	}

	frames := gifToFrames(m)
	if len(frames) == 0 {
		gs.setStatus("GIF has no frames.")
		return
	}

	gs.stopPlay()

	gs.srcFrames = frames
	gs.delays = append([]int(nil), m.Delay...)
	gs.procFrames = nil
	gs.tilefFrames = nil
	gs.videoData = nil
	gs.currentFrame = 0

	// Reset crop and show first frame in crop widget
	gs.cropWidget.SetImage(frames[0])

	// Update slider range
	gs.frameSlider.Max = float64(len(frames) - 1)
	gs.frameSlider.SetValue(0)

	gs.exportBtn.Disable()
	gs.compileBtn.Disable()

	gs.cfg.Name = sanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))

	gs.showFrame(0)
	gs.updateGifInfo()
	gs.setStatus(fmt.Sprintf("Loaded: %s — %d frames", filepath.Base(path), len(frames)))
}

func (gs *gifEditorState) updateGifInfo() {
	if len(gs.srcFrames) == 0 {
		gs.gifInfoLbl.SetText("No GIF loaded")
		return
	}
	total := 0
	for _, d := range gs.delays {
		total += d
	}
	avgDelay := 10
	if len(gs.delays) > 0 {
		avgDelay = total / len(gs.delays)
	}
	if avgDelay < 1 {
		avgDelay = 1
	}
	b := gs.srcFrames[0].Bounds()
	gs.gifInfoLbl.SetText(fmt.Sprintf(
		"%d frames  |  %.1f FPS avg  |  %d×%d px source",
		len(gs.srcFrames), 100.0/float64(avgDelay), b.Dx(), b.Dy(),
	))
}

// ── Frame display ─────────────────────────────────────────────────────────────

func (gs *gifEditorState) showFrame(i int) {
	if i < 0 || i >= len(gs.srcFrames) {
		return
	}
	gs.currentFrame = i

	delay := 10
	if i < len(gs.delays) && gs.delays[i] > 0 {
		delay = gs.delays[i]
	}
	gs.frameLabel.SetText(fmt.Sprintf("Frame %d / %d  |  %dms", i+1, len(gs.srcFrames), delay*10))

	// Input: update crop widget without resetting the crop rect
	gs.cropWidget.SrcImage = gs.srcFrames[i]
	gs.cropWidget.Refresh()

	// Output: prefer tilefied → preprocessed → raw
	switch {
	case gs.tilefFrames != nil && i < len(gs.tilefFrames) && gs.tilefFrames[i] != nil:
		gs.outCanvas.Image = gs.tilefFrames[i]
	case gs.procFrames != nil && i < len(gs.procFrames) && gs.procFrames[i] != nil:
		gs.outCanvas.Image = gs.procFrames[i]
	default:
		gs.outCanvas.Image = gs.srcFrames[i]
	}
	gs.outCanvas.Refresh()
}

func (gs *gifEditorState) refreshCropInfo() {
	cr := gs.cfg.CropRect
	if cr.Empty() {
		gs.cropInfoLbl.SetText("Draw on input frame to crop")
	} else {
		gs.cropInfoLbl.SetText(fmt.Sprintf("Crop: %d×%d px", cr.Dx(), cr.Dy()))
	}
}

func (gs *gifEditorState) snapGB(drawn image.Rectangle, bounds image.Rectangle) image.Rectangle {
	if drawn.Empty() {
		return drawn
	}
	cx := (drawn.Min.X + drawn.Max.X) / 2
	cy := (drawn.Min.Y + drawn.Max.Y) / 2
	scaleW := drawn.Dx() / gbW
	scaleH := drawn.Dy() / gbH
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	if scale < 1 {
		scale = 1
	}
	nw := gbW * scale
	nh := gbH * scale
	return clampRect(image.Rect(cx-nw/2, cy-nh/2, cx+nw/2, cy+nh/2), bounds)
}

// ── Processing ────────────────────────────────────────────────────────────────

func (gs *gifEditorState) processAllFrames() {
	if len(gs.srcFrames) == 0 {
		gs.setStatus("Open a GIF first.")
		return
	}
	gs.stopPlay()
	gs.processBtn.Disable()
	gs.exportBtn.Disable()
	gs.compileBtn.Disable()

	n := len(gs.srcFrames)
	procFrames := make([]image.Image, n)
	tilefFrames := make([]image.Image, n)

	for i, src := range gs.srcFrames {
		gs.setStatus(fmt.Sprintf("Preprocessing frame %d / %d…", i+1, n))

		processed, err := runGBPipelineFrame(src, gs.cfg)
		if err != nil {
			gs.setStatus(fmt.Sprintf("Error on frame %d: %v", i+1, err))
			gs.processBtn.Enable()
			return
		}
		procFrames[i] = processed

		// Per-frame tilefication for preview (Mode affects color vs grayscale)
		var tiles [][]Tile
		var palettes [8]Palette
		if gs.cfg.Mode == ModeDMG {
			tiles, palettes = tileficationDMG(processed)
		} else {
			tiles, palettes = tilefication(processed)
		}
		tilefFrames[i] = tilesToImage(tiles, palettes)

		// Live preview of the frame being processed
		if i == gs.currentFrame {
			gs.outCanvas.Image = tilefFrames[i]
			gs.outCanvas.Refresh()
		}
	}

	// Interpolate: insert blended frames between each consecutive pair so that
	// per-step tile churn stays within the free-slot budget → no keyframes.
	encFrames := procFrames
	encDelays := gs.delays
	if gs.cfg.InterpFrames > 0 {
		gs.setStatus(fmt.Sprintf("Interpolating (%d frames between each pair)…", gs.cfg.InterpFrames))
		encFrames, encDelays = interpolateFrames(procFrames, gs.delays, gs.cfg.InterpFrames)
	}

	gs.setStatus(fmt.Sprintf("Segmenting scenes & delta-encoding (%d frames)…", len(encFrames)))
	gs.videoData = tileficationVideo(encFrames, gs.cfg, gs.cfg.InterpFrames+1)

	// Map GIF inter-frame delays (centiseconds) to vsync frames (~60 Hz).
	// Interpolated frames have delay 0 in encDelays → map to 1 vsync minimum.
	for i := range gs.videoData.Frames {
		d := 1
		if i < len(encDelays) && encDelays[i] > 0 {
			d = int(float64(encDelays[i])*0.6 + 0.5)
		}
		if d < 1 {
			d = 1
		}
		if d > 255 {
			d = 255
		}
		gs.videoData.Frames[i].Delay = uint8(d)
	}

	// Report compression: count scenes (palette reloads) and total uploads.
	scenes, uploads := 0, 0
	for _, f := range gs.videoData.Frames {
		if f.NewPalette {
			scenes++
		}
		uploads += len(f.Uploads)
	}

	gs.procFrames = procFrames
	gs.tilefFrames = tilefFrames

	gs.showFrame(gs.currentFrame)
	gs.processBtn.Enable()
	gs.exportBtn.Enable()
	gs.compileBtn.Enable()
	gs.setStatus(fmt.Sprintf("Done — %d frames (%d encoded), %d scenes, %d tile uploads. Ready to export.", n, len(encFrames), scenes, uploads))
}

// ── Playback ──────────────────────────────────────────────────────────────────

func (gs *gifEditorState) togglePlay() {
	if gs.playing {
		gs.stopPlay()
	} else {
		gs.startPlay()
	}
}

func (gs *gifEditorState) startPlay() {
	if len(gs.srcFrames) == 0 || gs.playing {
		return
	}
	gs.playing = true
	gs.playStop = make(chan struct{})
	gs.playBtn.SetText("⏸  Pause")

	go func() {
		for {
			i := gs.currentFrame
			delay := 10
			if i < len(gs.delays) && gs.delays[i] > 0 {
				delay = gs.delays[i]
			}
			if delay < 2 {
				delay = 2 // clamp to 20 ms (browser minimum)
			}

			select {
			case <-gs.playStop:
				return
			case <-time.After(time.Duration(delay) * 10 * time.Millisecond):
			}

			next := (gs.currentFrame + 1) % len(gs.srcFrames)
			gs.showFrame(next)
		}
	}()
}

func (gs *gifEditorState) stopPlay() {
	if !gs.playing {
		return
	}
	gs.playing = false
	if gs.playStop != nil {
		close(gs.playStop)
		gs.playStop = nil
	}
	if gs.playBtn != nil {
		gs.playBtn.SetText("▶  Play")
	}
}

// ── Export / Compile ──────────────────────────────────────────────────────────

func (gs *gifEditorState) doExport() {
	if gs.videoData == nil {
		gs.setStatus("Process all frames first.")
		return
	}
	dir, name := gs.cfg.OutputDir, gs.cfg.Name
	if err := ExportGBDKVideo(dir, name, gs.videoData); err != nil {
		gs.setStatus("Export error: " + err.Error())
		return
	}
	gs.cfg.ROMBanks = gs.videoData.ROMBanks // sizing for the linker
	_ = GenerateBatchFile(gs.cfg)
	gs.setStatus(fmt.Sprintf("Exported → %s/  (%d ROM banks)", dir, gs.videoData.ROMBanks))
}

func (gs *gifEditorState) doCompile() {
	gs.doExport()
	if gs.videoData == nil {
		return
	}
	gs.setStatus("Compiling…")
	res := CompileGB(gs.cfg)
	if res.Success {
		gs.setStatus("Compiled OK → " + res.ROMPath)
	} else {
		gs.setStatus("Compile failed — see output.")
	}
	showOutputDialog(gs.win, "Compile Output", res.Output)
}

func (gs *gifEditorState) setStatus(msg string) {
	if gs.statusBar != nil {
		gs.statusBar.SetText(msg)
	}
}
