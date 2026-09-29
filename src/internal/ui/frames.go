package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"MediaBoy/internal/core"
	"MediaBoy/internal/video"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// gifEditorState drives both the GIF and the Video editors; videoMode switches
// the source (GIF decoder vs ffmpeg) and enables the video-only options.
type gifEditorState struct {
	cfg core.ConvertConfig
	win fyne.Window

	videoMode    bool
	targetFPS    int
	audioPCM     []byte
	videoSrcPath string
	autoQuality  bool

	srcFrames []*image.RGBA
	delays    []int

	procFrames []image.Image // preprocessed frames cache
	procGen    int           // bumped whenever procFrames is invalidated

	currentFrame int
	playing      bool
	playStop     chan struct{}

	busy    atomic.Bool // compiling
	loading atomic.Bool // decoding a source

	cropWidget  *CropWidget
	frameSlider *widget.Slider
	frameLabel  *widget.Label
	infoLbl     *widget.Label
	statusBar   *widget.Label
	playBtn     *widget.Button
	compileBtn  *widget.Button
	fpsSelect   *widget.Select
	cropInfoLbl *widget.Label

	settingsRefresh func()

	autoTimer   *time.Timer
	autoTimerMu sync.Mutex
}

func buildFramesEditorContent(win fyne.Window, mainCfg core.ConvertConfig, videoMode bool) (fyne.CanvasObject, *gifEditorState) {
	gs := &gifEditorState{
		cfg:       mainCfg,
		win:       win,
		videoMode: videoMode,
		targetFPS: 24,
	}
	gs.cfg.Mode = core.ModeCGB
	gs.cfg.BilateralEnabled = false
	gs.cfg.SharpenEnabled = false
	gs.cfg.PosterizeEnabled = false
	gs.cfg.DitheringEnabled = false
	if gs.cfg.MaxVideoMB <= 0 {
		gs.cfg.MaxVideoMB = 8
	}
	return gs.buildUI(), gs
}

func (gs *gifEditorState) kind() string {
	if gs.videoMode {
		return "video"
	}
	return "GIF"
}

func (gs *gifEditorState) buildUI() fyne.CanvasObject {
	gs.cropWidget = NewCropWidget(func(r image.Rectangle) {
		gs.cfg.CropRect = r
		gs.cfg.CropEnabled = true
		gs.refreshCropInfo()
		gs.scheduleAutoConvert()
	})
	gs.cropWidget.FixAspect = true
	gs.cropWidget.SnapFunc = snapGBRect
	gs.cropWidget.Placeholder = "Open a " + gs.kind() + " to begin"

	gs.infoLbl = statusLabel("No " + gs.kind() + " loaded")
	gs.frameLabel = widget.NewLabel("")
	gs.frameLabel.Alignment = fyne.TextAlignTrailing

	gs.frameSlider = widget.NewSlider(0, 1)
	gs.frameSlider.Step = 1
	gs.frameSlider.OnChanged = func(v float64) {
		if len(gs.srcFrames) == 0 || gs.playing {
			return
		}
		gs.showFrame(int(v))
	}

	step := func(d int) {
		if len(gs.srcFrames) == 0 {
			return
		}
		gs.stopPlay()
		n := (gs.currentFrame + d + len(gs.srcFrames)) % len(gs.srcFrames)
		gs.frameSlider.SetValue(float64(n))
		gs.showFrame(n)
	}
	prevBtn := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { step(-1) })
	nextBtn := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() { step(+1) })
	gs.playBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() { gs.togglePlay() })

	frameControls := container.NewBorder(nil, nil,
		container.NewHBox(prevBtn, gs.playBtn, nextBtn),
		fixedWidth(190, gs.frameLabel),
		gs.frameSlider,
	)
	inputTile := newTile(container.NewBorder(
		secLabel("Input  ·  drag to crop (snaps to 160×144)"),
		container.NewVBox(frameControls, gs.infoLbl),
		nil, nil,
		gs.cropWidget,
	))

	openBtn := widget.NewButtonWithIcon("Open "+gs.kind()+"…", theme.FolderOpenIcon(), func() {
		if gs.videoMode {
			gs.openVideo()
		} else {
			gs.openGIF()
		}
	})
	openBtn.Importance = widget.HighImportance

	gs.statusBar = statusLabel("Open a " + gs.kind() + " to begin.")

	left := container.NewBorder(openBtn, nil, nil, nil, gs.buildSettingsPanel())
	mainSplit := container.NewHSplit(left, inputTile)
	mainSplit.Offset = 0.28

	return container.NewBorder(nil, gs.statusBar, nil, nil, mainSplit)
}

func (gs *gifEditorState) buildSettingsPanel() fyne.CanvasObject {
	imgTab := gs.buildImageTab()
	exTab, exRefresh := gs.buildExportTab()
	gs.settingsRefresh = exRefresh

	tabs := container.NewAppTabs(
		container.NewTabItem("Image", imgTab),
		container.NewTabItem("Export", exTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return tabs
}

func (gs *gifEditorState) buildImageTab() fyne.CanvasObject {
	gs.cropInfoLbl = widget.NewLabel(cropInfoText(gs.cfg.CropRect))
	gs.cropInfoLbl.Wrapping = fyne.TextWrapWord
	cropSec := newSection("Crop", container.NewVBox(
		gs.cropInfoLbl,
		widget.NewButtonWithIcon("Reset crop", theme.ContentUndoIcon(), func() {
			gs.cfg.CropRect = image.Rectangle{}
			gs.cfg.CropEnabled = false
			gs.cropWidget.CropRect = image.Rectangle{}
			gs.cropWidget.Refresh()
			gs.refreshCropInfo()
			gs.scheduleAutoConvert()
		}),
	))

	scalingSelect := widget.NewSelect([]string{"Median", "Bilinear", "Nearest"}, func(v string) {
		gs.cfg.Scaling = scalingFromString(v)
		gs.scheduleAutoConvert()
	})
	scalingSelect.SetSelectedIndex(int(gs.cfg.Scaling))
	form := widget.NewForm(widget.NewFormItem("Downscale", scalingSelect))

	if gs.videoMode {
		opts := make([]string, len(fpsPresetValues))
		for i, v := range fpsPresetValues {
			opts[i] = fmt.Sprintf("%d fps", v)
		}
		fpsSelect := widget.NewSelect(opts, nil)
		gs.fpsSelect = fpsSelect
		fpsSelect.SetSelected(fmt.Sprintf("%d fps", gs.targetFPS))
		fpsSelect.OnChanged = func(v string) {
			n, err := strconv.Atoi(strings.TrimSuffix(v, " fps"))
			if err != nil || n == gs.targetFPS {
				return
			}
			if gs.videoSrcPath == "" {
				gs.targetFPS = n
				return
			}
			if gs.loading.Load() {
				gs.setStatus("Still loading the video — try again in a moment.")
				fpsSelect.SetSelected(fmt.Sprintf("%d fps", gs.targetFPS))
				return
			}
			go gs.decodeVideo(gs.videoSrcPath, false, n)
		}
		form.Append("Frame rate", fpsSelect)
	}

	objs := []fyne.CanvasObject{cropSec.tile, newSection("Frames", form).tile}
	if gs.videoMode {
		objs = append(objs, hintLabel("Lower frame rates fit longer clips into the ROM."))
	}
	return panelColumn(objs...)
}

func (gs *gifEditorState) buildExportTab() (fyne.CanvasObject, func()) {
	qualityVal := widget.NewLabel(strconv.Itoa(gs.cfg.Quality))
	qualitySlider := widget.NewSlider(0, 64)
	qualitySlider.Step = 1
	qualitySlider.SetValue(float64(gs.cfg.Quality))
	qualitySlider.OnChanged = func(v float64) {
		gs.cfg.Quality = int(v)
		qualityVal.SetText(strconv.Itoa(int(v)))
	}

	gbvForm := widget.NewForm(widget.NewFormItem("Quality", sliderRow(qualitySlider, qualityVal)))

	if gs.videoMode {
		sizeSelect := widget.NewSelect([]string{"1 MB", "2 MB", "4 MB", "8 MB"}, func(v string) {
			if n, err := strconv.Atoi(strings.TrimSuffix(v, " MB")); err == nil {
				gs.cfg.MaxVideoMB = n
			}
		})
		sizeSelect.SetSelected(fmt.Sprintf("%d MB", gs.cfg.MaxVideoMB))
		gbvForm.Append("Max ROM", sizeSelect)

		autoCheck := widget.NewCheck("Fit Max ROM automatically", func(v bool) {
			gs.autoQuality = v
			if v {
				qualitySlider.Disable()
			} else {
				qualitySlider.Enable()
			}
		})
		autoCheck.SetChecked(gs.autoQuality)
		gbvForm.Append("", autoCheck)
	}

	encSec := newSection("Encoding", container.NewVBox(
		gbvForm,
		hintLabel("Quality is a compression tolerance: 0 keeps the picture sharpest. "+
			"Higher values shrink the ROM so more frames fit, but noticeably degrade the graphics "+
			"(blocky, smeared, color-bleeding frames)."),
	))

	nameEntry := widget.NewEntry()
	nameEntry.SetText(gs.cfg.Name)
	nameEntry.SetPlaceHolder("video")
	nameEntry.OnChanged = func(v string) { gs.cfg.Name = sanitizeName(v) }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(gs.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("out")
	outDirEntry.OnChanged = func(v string) { gs.cfg.OutputDir = v }

	outSec := newSection("Output", widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Output Dir", outDirEntry),
	))

	gs.compileBtn = widget.NewButtonWithIcon("Compile ROM (.gbc)", theme.MediaPlayIcon(), func() { gs.doCompile() })
	gs.compileBtn.Importance = widget.HighImportance
	gs.compileBtn.Disable()
	actionBox := container.NewVBox(
		gs.compileBtn,
		widget.NewButtonWithIcon("Open output folder", theme.FolderIcon(), func() { openOutputFolder(gs.cfg.OutputDir) }),
	)

	refresh := func() {
		nameEntry.SetText(gs.cfg.Name)
		outDirEntry.SetText(gs.cfg.OutputDir)
	}
	return container.NewBorder(nil, actionBox, nil, nil, panelColumn(encSec.tile, outSec.tile)), refresh
}

func (gs *gifEditorState) openGIF() {
	pickFile("Open GIF", false, fileFilter{"GIF Files", []string{"gif"}}, gs.setStatus, gs.loadGIF)
}

func (gs *gifEditorState) setName(path string) {
	gs.cfg.Name = sanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if gs.settingsRefresh != nil {
		gs.settingsRefresh()
	}
}

func (gs *gifEditorState) updateInfo() {
	if len(gs.srcFrames) == 0 {
		gs.infoLbl.SetText("No " + gs.kind() + " loaded")
		return
	}
	fps := video.GIFFPS(gs.delays)
	if gs.videoMode {
		fps = float64(gs.targetFPS)
	}
	b := gs.srcFrames[0].Bounds()
	gs.infoLbl.SetText(fmt.Sprintf("%d frames  ·  %.1f fps  ·  %.1f s  ·  %d×%d px source",
		len(gs.srcFrames), fps, float64(len(gs.srcFrames))/fps, b.Dx(), b.Dy()))
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
	gs.frameLabel.SetText(fmt.Sprintf("Frame %d / %d  ·  %d ms", i+1, len(gs.srcFrames), delay*10))

	gs.cropWidget.SrcImage = gs.srcFrames[i]
	gs.cropWidget.Refresh()
}

func (gs *gifEditorState) refreshCropInfo() {
	gs.cropInfoLbl.SetText(cropInfoText(gs.cfg.CropRect))
}

func (gs *gifEditorState) togglePlay() {
	if gs.playing {
		gs.stopPlay()
	} else {
		gs.startPlay()
	}
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
	gs.playBtn.SetIcon(theme.MediaPlayIcon())
}

func (p *progressDialog) phase(lo, hi float64, label string) func(done, total int) {
	return func(done, total int) {
		if total <= 0 {
			return
		}
		p.set(lo+(hi-lo)*float64(done)/float64(total), fmt.Sprintf("%s frames %d / %d…", label, done, total))
	}
}

func (gs *gifEditorState) warnTruncated(res video.GBVP2Result) {
	mb := gs.cfg.MaxVideoMB
	if mb <= 0 {
		mb = 8
	}
	advice := "raise the Quality number or trim the clip"
	if gs.videoMode {
		advice = "lower the Frame rate, " + advice
		if mb < 8 {
			advice = "raise the Max ROM size, " + advice
		}
	}
	msg := fmt.Sprintf(
		"Too long for a %d MB ROM — only %d of %d frames fit.\n"+
			"The rest were dropped and playback loops at the cut.\n\nTo fit more: %s.",
		mb, res.FramesUsed, res.FramesTotal, advice)
	dialog.ShowInformation("Output truncated", msg, gs.win)
}

func (gs *gifEditorState) encodeParams() (float64, []byte) {
	if gs.videoMode {
		return float64(gs.targetFPS), gs.audioPCM
	}
	return video.GIFFPS(gs.delays), nil
}

func (gs *gifEditorState) openVideo() {
	pickFile("Open Video", false,
		fileFilter{"Video", []string{"mp4", "mov", "mkv", "avi", "webm", "m4v"}},
		gs.setStatus, gs.loadVideo)
}

func (gs *gifEditorState) warnIfLikelyTooBig() {
	maxBanks := video.BanksFromMB(gs.cfg.MaxVideoMB)
	audioBanks := (len(gs.audioPCM)/2 + 0x3FFF) / 0x4000
	if audioBanks*100 >= maxBanks*60 {
		dialog.ShowInformation("May exceed ROM size",
			fmt.Sprintf("This clip's audio alone needs ~%d of the %d banks (%d MB) — the video will likely be truncated.\n\n"+
				"Raise Max ROM, enable Auto quality, or use a shorter clip.",
				audioBanks, maxBanks, gs.cfg.MaxVideoMB), gs.win)
	}
}

func uniformDelays(n, fps int) []int {
	d := 10
	if fps > 0 {
		d = max(1, 100/fps)
	}
	delays := make([]int, n)
	for i := range delays {
		delays[i] = d
	}
	return delays
}

func (gs *gifEditorState) setFrames(frames []*image.RGBA, delays []int, resetCrop bool) {
	gs.stopPlay()
	gs.srcFrames = frames
	gs.delays = delays
	gs.invalidateFrames()
	gs.currentFrame = 0
	if resetCrop {
		gs.cfg.CropRect = image.Rectangle{}
		gs.cfg.CropEnabled = false
		gs.cropWidget.SetImage(frames[0])
	} else {
		gs.cropWidget.SrcImage = frames[0]
	}
	gs.frameSlider.Max = float64(len(frames) - 1)
	gs.frameSlider.SetValue(0)
	gs.compileBtn.Enable()
	gs.refreshCropInfo()
	gs.showFrame(0)
	gs.updateInfo()
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

// setStatus is safe to call from any goroutine.
func (gs *gifEditorState) setStatus(msg string) {
	fyne.Do(func() { gs.statusBar.SetText(msg) })
}
