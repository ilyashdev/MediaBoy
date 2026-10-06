package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"MediaBoy/internal/core"
	"MediaBoy/internal/deps"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/safe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	tabPixelArt = "Pixel Art"
	tabGB       = "GB / GBC"
)

type appState struct {
	cfg core.ConvertConfig
	win fyne.Window

	srcImage image.Image
	result   *imaging.ConvertResult

	cropWidget   *CropWidget
	outputCanvas *canvas.Image
	statusBar    *lineLabel
	paCropLabel  *widget.Label
	gbCropLabel  *widget.Label
	outputBtns   []*widget.Button // need a loaded image
	busy         atomic.Bool

	gif   *gifEditorState
	video *gifEditorState
	music *musicState
	mode  string

	galleryImages []image.Image
	galleryLabel  *widget.Label

	settingsRefresh    func()
	setImageLayout     func(landscape bool)
	rebuildPresetsFunc func()

	bilCache   imaging.BilCache
	previewGen int // bumped per preview run; stale results are dropped

	activeTab   string
	autoTimer   *time.Timer
	autoTimerMu sync.Mutex
}

func Run() {
	// Last resort for a panic on the UI thread outside uiDo (widget
	// callbacks): the event loop is gone by now, so log it and exit.
	defer func() {
		if p := safe.Recovered(recover()); p != nil {
			reportPanic(p, nil)
			os.Exit(2)
		}
	}()
	a := app.NewWithID("com.mediaboy.app")
	a.Settings().SetTheme(mbTheme{})
	icon := fyne.NewStaticResource("logo.png", logoPNG)
	a.SetIcon(icon)
	win := a.NewWindow("MediaBoy")
	win.SetIcon(icon)
	win.Resize(fyne.NewSize(1180, 740))

	s := &appState{
		cfg:       core.DefaultConfig(),
		win:       win,
		activeTab: tabPixelArt,
		mode:      "image",
	}
	panicStatus = s.statusForMode
	if home := a.Preferences().String(prefGBDKHome); home != "" {
		s.cfg.GBDKHome = home
	}

	imageContent := s.buildImageMode()

	centerHolder := container.NewStack(imageContent)
	modeContent := map[string]fyne.CanvasObject{"image": imageContent}
	var navBtns = map[string]*widget.Button{}

	setMode := func(mode string) {
		if modeContent[mode] == nil {
			switch mode {
			case "gif":
				modeContent[mode], s.gif = buildFramesEditorContent(win, s.cfg, false)
			case "video":
				modeContent[mode], s.video = buildFramesEditorContent(win, s.cfg, true)
			case "music":
				modeContent[mode], s.music = buildMusicEditorContent(win, s.cfg)
			}
		}
		s.mode = mode
		centerHolder.Objects[0] = modeContent[mode]
		for m, b := range navBtns {
			if m == mode {
				b.Importance = widget.HighImportance
			} else {
				b.Importance = widget.LowImportance
			}
			b.Refresh()
		}
		centerHolder.Refresh()
	}

	nav := container.NewHBox()
	for _, m := range []struct{ key, label string }{
		{"image", "Image"}, {"gif", "GIF"}, {"video", "Video"}, {"music", "Music"},
	} {
		key := m.key
		b := widget.NewButton(m.label, func() { setMode(key) })
		navBtns[key] = b
		nav.Add(b)
	}

	depsBtn := widget.NewButtonWithIcon("Dependencies", theme.SettingsIcon(), func() { s.checkDeps(true) })
	depsBtn.Importance = widget.LowImportance

	toolbar := newTile(container.NewBorder(nil, nil, nav, depsBtn))
	setMode("image")

	win.SetContent(withBackdrop(container.NewBorder(toolbar, nil, nil, nil, centerHolder)))
	win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) { s.handleDrop(uris, setMode) })
	a.Lifecycle().SetOnStarted(func() {
		if a.Preferences().BoolWithFallback(prefCheckOnLaunch, true) {
			s.checkDeps(false)
		} else {
			s.applyGBDKHome(deps.Check(s.cfg.GBDKHome).GBDKHome)
		}
	})
	win.ShowAndRun()
}

// statusForMode reports to whichever editor is currently on screen.
func (s *appState) statusForMode(msg string) {
	switch {
	case s.mode == "gif" && s.gif != nil:
		s.gif.setStatus(msg)
	case s.mode == "video" && s.video != nil:
		s.video.setStatus(msg)
	case s.mode == "music" && s.music != nil:
		s.music.setStatus(msg)
	default:
		s.setStatus(msg)
	}
}

func (s *appState) buildImageMode() fyne.CanvasObject {
	s.statusBar = statusLabel("Open an image to start.")

	s.cropWidget = NewCropWidget(func(r image.Rectangle) {
		s.cfg.CropRect = r
		s.cfg.CropEnabled = true
		s.refreshCropInfo()
		if s.rebuildPresetsFunc != nil {
			s.rebuildPresetsFunc()
		}
		s.scheduleAutoConvert()
	})
	s.cropWidget.Placeholder = "Open an image to begin"

	inputPanel := newTile(container.NewBorder(secLabel("Input  ·  drag to crop"), nil, nil, nil, s.cropWidget))

	s.outputCanvas = canvas.NewImageFromImage(nil)
	s.outputCanvas.FillMode = canvas.ImageFillContain
	s.outputCanvas.ScaleMode = canvas.ImageScalePixels
	s.outputCanvas.SetMinSize(fyne.NewSize(300, 220))

	saveBtn := widget.NewButtonWithIcon("Save PNG", theme.DocumentSaveIcon(), func() { s.saveAsPNG() })
	outActions := container.NewHBox(saveBtn)
	s.outputBtns = []*widget.Button{saveBtn}
	if runtime.GOOS == "windows" {
		copyBtn := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() { s.copyToClipboard() })
		outActions.Add(copyBtn)
		s.outputBtns = append(s.outputBtns, copyBtn)
	}
	outputPanel := newTile(container.NewBorder(secLabel("Output preview"), outActions, nil, nil, s.outputCanvas))

	imageArea := container.NewStack()
	s.setImageLayout = func(landscape bool) {
		var split *container.Split
		if landscape {
			split = container.NewVSplit(inputPanel, outputPanel)
		} else {
			split = container.NewHSplit(inputPanel, outputPanel)
		}
		split.Offset = 0.5
		imageArea.Objects = []fyne.CanvasObject{split}
		imageArea.Refresh()
	}
	s.setImageLayout(false)

	settingsPanel, settingsRefresh := buildSettingsPanel(s)
	s.settingsRefresh = settingsRefresh
	for _, b := range s.outputBtns {
		b.Disable()
	}

	openBtn := widget.NewButtonWithIcon("Open Image…", theme.FolderOpenIcon(), func() { s.openImage() })
	openBtn.Importance = widget.HighImportance
	left := container.NewBorder(openBtn, nil, nil, nil, settingsPanel)

	mainSplit := container.NewHSplit(left, imageArea)
	mainSplit.Offset = 0.26
	return container.NewBorder(nil, s.statusBar, nil, nil, mainSplit)
}

func (s *appState) openImage() {
	pickFile("Open Image", false, fileFilter{"Image Files", imageExts},
		s.setStatus, s.loadImageFromPath)
}

// runJob runs fn in the background unless another compile/export job of this
// editor is still in progress.
func runJob(busy *atomic.Bool, status func(string), fn func()) {
	if !busy.CompareAndSwap(false, true) {
		status("Busy — wait for the current build to finish.")
		return
	}
	goSafe(status, func() {
		defer busy.Store(false)
		fn()
	})
}

func (s *appState) refreshGalleryLabel() {
	if s.galleryLabel != nil {
		s.galleryLabel.SetText(fmt.Sprintf("%d image(s) in gallery", len(s.galleryImages)))
	}
}

func (s *appState) doClearGallery() {
	s.galleryImages = nil
	s.refreshGalleryLabel()
	s.setStatus("Gallery cleared.")
}

func (s *appState) saveAsPNG() {
	if s.result == nil || s.result.ProcessedImage == nil {
		s.setStatus("Nothing to save yet — wait for the preview.")
		return
	}
	img := s.result.ProcessedImage
	pickFile("Save as PNG", true, fileFilter{"PNG Image", []string{"png"}}, s.setStatus, func(path string) {
		if !strings.HasSuffix(strings.ToLower(path), ".png") {
			path += ".png"
		}
		f, err := os.Create(path)
		if err != nil {
			s.setStatus("Save error: " + err.Error())
			return
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			s.setStatus("Encode error: " + err.Error())
			return
		}
		s.setStatus("Saved → " + path)
	})
}

func (s *appState) snapPixelArt(drawn image.Rectangle, bounds image.Rectangle) image.Rectangle {
	if drawn.Empty() {
		return drawn
	}
	tw := s.cfg.TargetW
	th := s.cfg.TargetH
	if tw <= 0 || th <= 0 {
		return drawn
	}

	cx := (drawn.Min.X + drawn.Max.X) / 2
	cy := (drawn.Min.Y + drawn.Max.Y) / 2
	aspect := float64(tw) / float64(th)

	var nw, nh int
	if float64(drawn.Dx())/float64(drawn.Dy()) > aspect {
		nh = drawn.Dy()
		nw = int(float64(nh) * aspect)
	} else {
		nw = drawn.Dx()
		nh = int(float64(nw) / aspect)
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	r := image.Rect(cx-nw/2, cy-nh/2, cx+nw/2, cy+nh/2)
	return clampRect(r, bounds)
}

func (s *appState) resetCrop() {
	s.cfg.CropRect = image.Rectangle{}
	s.cfg.CropEnabled = false
	s.cropWidget.CropRect = image.Rectangle{}
	s.cropWidget.Refresh()
	s.refreshCropInfo()
	if s.rebuildPresetsFunc != nil {
		s.rebuildPresetsFunc()
	}
	s.scheduleAutoConvert()
}

func (s *appState) refreshCropInfo() {
	text := cropInfoText(s.cfg.CropRect)
	if s.paCropLabel != nil {
		s.paCropLabel.SetText(text)
	}
	if s.gbCropLabel != nil {
		s.gbCropLabel.SetText(text)
	}
}

func cropInfoText(cr image.Rectangle) string {
	if cr.Empty() {
		return "No crop — drag on the input to select an area."
	}
	return fmt.Sprintf("Crop: %d×%d px", cr.Dx(), cr.Dy())
}

// setStatus is safe to call from any goroutine.
func (s *appState) setStatus(msg string) {
	setText(s.statusBar, msg)
}

func sanitizeName(name string) string {
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "image"
	}
	s := b.String()
	// A C identifier (used for include guards, symbol names, etc.) may not
	// start with a digit, so prefix one with '_'. Without this a name like
	// "222" generates "#ifndef 222_H", which sdcc rejects ("macro names must
	// be identifiers"), skipping the whole header and cascading into
	// undefined-type/macro errors downstream.
	if s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return s
}
