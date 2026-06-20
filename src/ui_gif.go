package main

import (
	"fmt"
	"image"
	"image/gif"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqDialog "github.com/sqweek/dialog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type gifEditorState struct {
	cfg ConvertConfig
	win fyne.Window

	videoMode    bool
	targetFPS    int
	sourceFPS    float64
	audioPCM     []byte
	videoSrcPath string
	autoQuality  bool

	srcFrames []*image.RGBA
	delays    []int

	procFrames []image.Image

	currentFrame int
	playing      bool
	playStop     chan struct{}

	cropWidget  *CropWidget
	frameSlider *widget.Slider
	frameLabel  *widget.Label
	gifInfoLbl  *widget.Label
	statusBar   *widget.Label
	playBtn     *widget.Button
	compileBtn  *widget.Button
	cropInfoLbl *widget.Label

	settingsRefresh func()

	autoTimer   *time.Timer
	autoTimerMu sync.Mutex
}

func buildGifEditorContent(win fyne.Window, mainCfg ConvertConfig) (fyne.CanvasObject, *gifEditorState) {
	return buildFramesEditorContent(win, mainCfg, false)
}

func buildFramesEditorContent(win fyne.Window, mainCfg ConvertConfig, videoMode bool) (fyne.CanvasObject, *gifEditorState) {
	gs := &gifEditorState{
		cfg:       mainCfg,
		win:       win,
		videoMode: videoMode,
		targetFPS: 24,
	}
	gs.cfg.Mode = ModeCGB

	gs.cfg.BilateralEnabled = false
	gs.cfg.SharpenEnabled = false
	gs.cfg.PosterizeEnabled = false
	gs.cfg.DitheringEnabled = false
	if gs.cfg.MaxVideoMB <= 0 {
		gs.cfg.MaxVideoMB = 8
	}
	return gs.buildUI(), gs
}

func (gs *gifEditorState) buildUI() fyne.CanvasObject {
	gs.cropWidget = NewCropWidget(func(r image.Rectangle) {
		gs.cfg.CropRect = r
		gs.cfg.CropEnabled = true
		gs.refreshCropInfo()
		gs.scheduleAutoConvert()
	})
	gs.cropWidget.FixAspect = true
	gs.cropWidget.SnapFunc = gs.snapGB

	inLbl := widget.NewLabelWithStyle("Input Frame", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	imageArea := container.NewBorder(inLbl, nil, nil, nil, gs.cropWidget)

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

	openLabel, beginMsg := "Open GIF", "Open a GIF to begin."
	openAction := func() { gs.openGIF() }
	if gs.videoMode {
		openLabel, beginMsg = "Open Video", "Open a video to begin."
		openAction = func() { gs.openVideo() }
	}
	openBtn := widget.NewButton(openLabel, openAction)
	openBtn.Importance = widget.HighImportance
	topBar := container.NewHBox(openBtn)

	gs.statusBar = widget.NewLabel(beginMsg)

	mainSplit := container.NewHSplit(gs.buildSettingsPanel(), rightPanel)
	mainSplit.Offset = 0.24

	return container.NewBorder(topBar, gs.statusBar, nil, nil, mainSplit)
}

func (gs *gifEditorState) buildSettingsPanel() fyne.CanvasObject {
	imgTab := gs.buildImageTab()
	exTab, exRefresh := gs.buildExportTab()

	tabs := container.NewAppTabs(
		container.NewTabItem("Image", imgTab),
		container.NewTabItem("Export", exTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	gs.settingsRefresh = exRefresh
	return tabs
}

func (gs *gifEditorState) buildImageTab() fyne.CanvasObject {

	gs.cropInfoLbl = widget.NewLabel("Draw on input frame to crop")
	resetCropBtn := widget.NewButton("Reset Crop", func() {
		gs.cfg.CropRect = image.Rectangle{}
		gs.cfg.CropEnabled = false
		gs.cropWidget.CropRect = image.Rectangle{}
		gs.cropWidget.Refresh()
		gs.refreshCropInfo()
		gs.scheduleAutoConvert()
	})

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
			gs.scheduleAutoConvert()
		},
	)
	scalingSelect.SetSelectedIndex(int(gs.cfg.Scaling))

	panel := container.NewVBox(
		secLabel("Crop  (auto-snap 10:9)"),
		gs.cropInfoLbl,
		container.NewHBox(resetCropBtn),

		widget.NewSeparator(),
		widget.NewForm(widget.NewFormItem("Downscale", scalingSelect)),
	)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(220, 100))
	return scroll
}

func (gs *gifEditorState) buildExportTab() (fyne.CanvasObject, func()) {

	qualityVal := widget.NewLabel(fmt.Sprintf("%d", gs.cfg.Quality))
	qualitySlider := widget.NewSlider(0, 64)
	qualitySlider.Step = 1
	qualitySlider.SetValue(float64(gs.cfg.Quality))
	qualitySlider.OnChanged = func(v float64) {
		gs.cfg.Quality = int(v)
		qualityVal.SetText(fmt.Sprintf("%d", int(v)))
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(gs.cfg.Name)
	nameEntry.SetPlaceHolder("video")
	nameEntry.OnChanged = func(v string) { gs.cfg.Name = sanitizeName(v) }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(gs.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("out")
	outDirEntry.OnChanged = func(v string) { gs.cfg.OutputDir = v }

	gbvForm := widget.NewForm(widget.NewFormItem("Quality", sliderRow(qualitySlider, qualityVal)))

	if gs.videoMode {
		sizeSelect := widget.NewSelect([]string{"1 MB", "2 MB", "4 MB", "8 MB"}, func(v string) {
			if n, err := strconv.Atoi(strings.TrimSuffix(v, " MB")); err == nil {
				gs.cfg.MaxVideoMB = n
			}
		})
		sizeSelect.SetSelected(fmt.Sprintf("%d MB", gs.cfg.MaxVideoMB))
		gbvForm.Append("Max ROM", sizeSelect)

		autoCheck := widget.NewCheck("fit Max ROM automatically", func(v bool) {
			gs.autoQuality = v
			if v {
				qualitySlider.Disable()
			} else {
				qualitySlider.Enable()
			}
		})
		autoCheck.SetChecked(gs.autoQuality)
		gbvForm.Append("Auto quality", autoCheck)
	}

	panel := container.NewVBox(
		secLabel("GBVideoPlayer2"),
		gbvForm,

		widget.NewSeparator(),
		secLabel("Output"),
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("Output Dir", outDirEntry),
		),
	)

	gs.compileBtn = widget.NewButton("Compile ROM (.gbc)", func() { gs.doCompile() })
	gs.compileBtn.Importance = widget.HighImportance
	gs.compileBtn.Disable()
	actionBox := container.NewVBox(
		widget.NewButton("Open output folder", func() { openOutputFolder(gs.cfg.OutputDir) }),
		gs.compileBtn,
	)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(220, 100))

	refresh := func() {
		nameEntry.SetText(gs.cfg.Name)
		outDirEntry.SetText(gs.cfg.OutputDir)
	}
	return container.NewBorder(nil, actionBox, nil, nil, scroll), refresh
}

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
	gs.currentFrame = 0

	gs.cfg.CropRect = image.Rectangle{}
	gs.cfg.CropEnabled = false
	gs.cropWidget.CropRect = image.Rectangle{}
	gs.cropWidget.SetImage(frames[0])

	gs.frameSlider.Max = float64(len(frames) - 1)
	gs.frameSlider.SetValue(0)

	gs.cfg.Name = sanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if gs.settingsRefresh != nil {
		gs.settingsRefresh()
	}
	gs.compileBtn.Enable()

	gs.refreshCropInfo()
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

	gs.cropWidget.SrcImage = gs.srcFrames[i]
	gs.cropWidget.Refresh()
}

func (gs *gifEditorState) refreshCropInfo() {
	if gs.cropInfoLbl == nil {
		return
	}
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

func (gs *gifEditorState) scheduleAutoConvert() {
	gs.autoTimerMu.Lock()
	defer gs.autoTimerMu.Unlock()
	if gs.autoTimer != nil {
		gs.autoTimer.Stop()
	}
	gs.autoTimer = time.AfterFunc(120*time.Millisecond, func() {
		gs.procFrames = nil
		if len(gs.srcFrames) > 0 {
			gs.showFrame(gs.currentFrame)
		}
	})
}

func (gs *gifEditorState) buildProcessedFrames(progress func(done, total int)) []image.Image {
	if len(gs.srcFrames) == 0 {
		gs.setStatus("Open a source first.")
		return nil
	}
	if gs.procFrames != nil {
		if progress != nil {
			progress(len(gs.procFrames), len(gs.procFrames))
		}
		return gs.procFrames
	}
	n := len(gs.srcFrames)
	out := make([]image.Image, n)
	cfg := gs.cfg
	var done int64
	var firstErr atomic.Value
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i := range gs.srcFrames {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			p, err := runGBPipelineFrame(gs.srcFrames[i], cfg)
			if err != nil {
				firstErr.Store(err)
				return
			}
			out[i] = p
			d := atomic.AddInt64(&done, 1)
			if progress != nil {
				progress(int(d), n)
			} else {
				gs.setStatus(fmt.Sprintf("Preprocessing frame %d / %d…", d, n))
			}
		}(i)
	}
	wg.Wait()
	if e := firstErr.Load(); e != nil {
		gs.setStatus("Frame error: " + e.(error).Error())
		return nil
	}
	gs.procFrames = out
	return out
}

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
				delay = 2
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

func (gs *gifEditorState) doCompile() {
	if len(gs.srcFrames) == 0 {
		gs.setStatus("Open a source first.")
		return
	}
	go func() {
		player, err := ensureGBVP2()
		if err != nil {
			gs.setStatus("GBVP2: " + err.Error())
			return
		}
		prog := gs.newProgress("Compiling video")
		frames := gs.buildProcessedFrames(prog.phase(0, 0.4, "Preprocessing"))
		if frames == nil {
			prog.close()
			return
		}
		fps, audio := gs.encodeParams()
		var res GBVP2Result
		if gs.videoMode && gs.autoQuality {
			if res, err = gs.fitQuality(player, frames, audio, fps, prog); err == nil {
				err = writeGBVP2ROM(gs.cfg.OutputDir, gs.cfg.Name, res.ROM)
			}
		} else {
			res, err = ExportGBVP2(gs.cfg.OutputDir, gs.cfg.Name, player, frames, audio, fps,
				gs.cfg.Quality, gs.cfg.MaxVideoMB, prog.phase(0.4, 1.0, "Encoding"))
		}
		prog.close()
		if err != nil {
			gs.setStatus("GBVP2 error: " + err.Error())
			return
		}
		gs.setStatus(fmt.Sprintf("GBVP2 ROM → %s/%s.gbc  (%d banks, %d/%d frames, q%d)",
			gs.cfg.OutputDir, gs.cfg.Name, res.Banks, res.FramesUsed, res.FramesTotal, gs.cfg.Quality))
		if res.Truncated() {
			gs.warnTruncated(res)
		}
	}()
}

func (gs *gifEditorState) fitQuality(player string, frames []image.Image, audio []byte, fps float64, prog *gifProgress) (GBVP2Result, error) {
	const loQ, hiQ = 0, 64
	lo, hi := loQ, hiQ
	bestQ := -1
	var best, worst GBVP2Result
	for lo <= hi {
		mid := (lo + hi) / 2
		var pcb func(int, int)
		if prog != nil {
			pcb = prog.phase(0.4, 1.0, fmt.Sprintf("Auto quality q%d:", mid))
		}
		res, err := buildGBVP2ROM(gs.cfg.OutputDir, player, frames, audio, fps, mid, gs.cfg.MaxVideoMB, pcb)
		if err != nil {
			return GBVP2Result{}, err
		}
		worst = res
		if !res.Truncated() {
			bestQ, best = mid, res
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	if bestQ < 0 {
		gs.cfg.Quality = hiQ
		return worst, nil
	}
	gs.cfg.Quality = bestQ
	return best, nil
}

type gifProgress struct {
	bar *widget.ProgressBar
	lbl *widget.Label
	dlg dialog.Dialog
}

func (gs *gifEditorState) newProgress(title string) *gifProgress {
	bar := widget.NewProgressBar()
	lbl := widget.NewLabel("Starting…")
	d := dialog.NewCustomWithoutButtons(title, container.NewVBox(lbl, bar), gs.win)
	d.Resize(fyne.NewSize(380, 110))
	d.Show()
	return &gifProgress{bar: bar, lbl: lbl, dlg: d}
}

func (p *gifProgress) set(frac float64, msg string) {
	if p == nil {
		return
	}
	p.bar.SetValue(frac)
	p.lbl.SetText(msg)
}

func (p *gifProgress) close() {
	if p != nil && p.dlg != nil {
		p.dlg.Hide()
	}
}

func (p *gifProgress) phase(lo, hi float64, label string) func(done, total int) {
	return func(done, total int) {
		if total <= 0 {
			return
		}
		f := lo + (hi-lo)*float64(done)/float64(total)
		p.bar.SetValue(f)
		p.lbl.SetText(fmt.Sprintf("%s frames %d / %d…", label, done, total))
	}
}

func (gs *gifEditorState) warnTruncated(res GBVP2Result) {
	mb := gs.cfg.MaxVideoMB
	if mb <= 0 {
		mb = 8
	}
	advice := "lower the FPS, raise the Quality number, or trim the clip"
	if mb < 8 {
		advice = "raise the Max ROM size, " + advice
	}
	msg := fmt.Sprintf(
		"Video too long for a %d MB ROM — only %d of %d frames fit.\n"+
			"The rest were dropped and playback loops at the cut.\n\nTo fit more: %s.",
		mb, res.FramesUsed, res.FramesTotal, advice)
	dialog.ShowInformation("Video truncated", msg, gs.win)
}

func (gs *gifEditorState) encodeParams() (float64, []byte) {
	if gs.videoMode {
		return float64(gs.targetFPS), gs.audioPCM
	}
	return gifFPS(gs.delays), nil
}

func (gs *gifEditorState) openVideo() {
	go func() {
		path, err := sqDialog.File().
			Filter("Video", "mp4", "mov", "mkv", "avi", "webm", "m4v").
			Title("Open Video").
			Load()
		if err != nil {
			if err != sqDialog.ErrCancelled {
				gs.setStatus("File error: " + err.Error())
			}
			return
		}
		gs.loadVideo(path)
	}()
}

func (gs *gifEditorState) loadVideo(path string) {
	gs.videoSrcPath = path
	if sf, err := probeVideoFPS(path); err == nil && sf > 0 {
		gs.sourceFPS = sf
		if float64(gs.targetFPS) > sf {
			gs.targetFPS = capFPS(sf)
		}
	}
	prog := gs.newProgress("Loading video")
	prog.set(0, fmt.Sprintf("Extracting frames @ %d fps…", gs.targetFPS))
	gs.setStatus(fmt.Sprintf("Extracting frames @ %d fps…", gs.targetFPS))
	frames, err := extractVideoFrames(path, gs.targetFPS, func(done, total int) {
		if total <= 0 {
			return
		}
		prog.set(0.9*float64(done)/float64(total),
			fmt.Sprintf("Decoding frames %d / %d…", done, total))
	})
	if err != nil {
		prog.close()
		gs.setStatus("Video error: " + err.Error())
		return
	}
	prog.set(0.95, "Extracting audio…")
	gs.setStatus("Extracting audio…")
	gs.audioPCM = extractVideoAudio(path)
	prog.close()

	gs.cfg.Name = sanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if gs.settingsRefresh != nil {
		gs.settingsRefresh()
	}
	gs.setVideoFrames(frames, true)
	aud := "no audio"
	if gs.audioPCM != nil {
		aud = fmt.Sprintf("%d KB audio", len(gs.audioPCM)/1024)
	}
	gs.setStatus(fmt.Sprintf("Loaded: %s — %d frames @ %d fps, %s", filepath.Base(path), len(frames), gs.targetFPS, aud))
	gs.warnIfLikelyTooBig()
}

func (gs *gifEditorState) warnIfLikelyTooBig() {
	if !gs.videoMode {
		return
	}
	maxBanks := banksFromMB(gs.cfg.MaxVideoMB)
	audioBanks := (len(gs.audioPCM)/2 + 0x3FFF) / 0x4000
	if audioBanks*100 >= maxBanks*60 {
		dialog.ShowInformation("May exceed ROM size",
			fmt.Sprintf("This clip's audio alone needs ~%d of the %d banks (%d MB) — the video will likely be truncated.\n\n"+
				"Raise Max ROM, enable Auto quality, or use a shorter clip.",
				audioBanks, maxBanks, gs.cfg.MaxVideoMB), gs.win)
	}
}

func (gs *gifEditorState) setVideoFrames(frames []*image.RGBA, resetCrop bool) {
	if len(frames) == 0 {
		return
	}
	gs.stopPlay()
	d := 10
	if gs.targetFPS > 0 {
		if d = 100 / gs.targetFPS; d < 1 {
			d = 1
		}
	}
	delays := make([]int, len(frames))
	for i := range delays {
		delays[i] = d
	}
	gs.srcFrames = frames
	gs.delays = delays
	gs.procFrames = nil
	gs.currentFrame = 0
	if resetCrop {
		gs.cfg.CropRect = image.Rectangle{}
		gs.cfg.CropEnabled = false
		gs.cropWidget.CropRect = image.Rectangle{}
	}
	gs.cropWidget.SetImage(frames[0])
	gs.frameSlider.Max = float64(len(frames) - 1)
	gs.frameSlider.SetValue(0)
	gs.compileBtn.Enable()
	gs.refreshCropInfo()
	gs.showFrame(0)
	gs.updateGifInfo()
}

var fpsPresetValues = []int{12, 15, 24, 30, 60}

func capFPS(sourceFPS float64) int {
	best := fpsPresetValues[0]
	for _, p := range fpsPresetValues {
		if float64(p) <= sourceFPS+0.5 {
			best = p
		}
	}
	return best
}

func (gs *gifEditorState) setStatus(msg string) {
	if gs.statusBar != nil {
		gs.statusBar.SetText(msg)
	}
}
