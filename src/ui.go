package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sqDialog "github.com/sqweek/dialog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type appState struct {
	cfg ConvertConfig
	win fyne.Window

	srcImage image.Image
	result   *ConvertResult

	cropWidget   *CropWidget
	outputCanvas *canvas.Image
	statusBar    *widget.Label
	paCropLabel  *widget.Label
	gbCropLabel  *widget.Label
	photoContent fyne.CanvasObject
	gif          *gifEditorState
	video        *gifEditorState
	music        *musicState

	galleryImages []image.Image
	galleryLabel  *widget.Label

	settingsRefresh    func()
	setImageLayout     func(landscape bool)
	rebuildPresetsFunc func()

	bilCache BilCache

	activeTab   string
	autoTimer   *time.Timer
	autoTimerMu sync.Mutex
}

func runUI() {
	a := app.NewWithID("com.mediaboy.app")
	icon := fyne.NewStaticResource("logo.png", logoPNG)
	a.SetIcon(icon)
	win := a.NewWindow("MediaBoy")
	win.SetIcon(icon)
	win.Resize(fyne.NewSize(1150, 720))

	s := &appState{
		cfg:       defaultConfig(),
		win:       win,
		activeTab: "Pixel Art",
	}
	wireExistingDeps(&s.cfg)

	s.statusBar = widget.NewLabel("Ready. Open an image to start.")

	cropW := NewCropWidget(func(r image.Rectangle) {
		s.cfg.CropRect = r
		s.cfg.CropEnabled = true
		s.refreshCropInfo()
		if s.rebuildPresetsFunc != nil {
			s.rebuildPresetsFunc()
		}

		s.scheduleAutoConvert()
	})
	s.cropWidget = cropW

	inputLabel := widget.NewLabelWithStyle("Input Image", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	inputPanel := container.NewBorder(inputLabel, nil, nil, nil, cropW)

	outImg := canvas.NewImageFromImage(nil)
	outImg.FillMode = canvas.ImageFillContain
	outImg.SetMinSize(fyne.NewSize(300, 220))
	s.outputCanvas = outImg

	outLabel := widget.NewLabelWithStyle("Output Preview", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	outputPanel := container.NewBorder(outLabel, nil, nil, nil, outImg)

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

	copyBtn := widget.NewButton("Copy", func() { s.copyToClipboard() })
	savePNGBtn := widget.NewButton("Save PNG", func() { s.saveAsPNG() })
	outActions := container.NewHBox(copyBtn, savePNGBtn)
	imageWithActions := container.NewBorder(nil, outActions, nil, nil, imageArea)

	settingsPanel, settingsRefresh := buildSettingsPanel(s)
	s.settingsRefresh = settingsRefresh

	mainSplit := container.NewHSplit(settingsPanel, imageWithActions)
	mainSplit.Offset = 0.24

	centerHolder := container.NewStack(mainSplit)
	var gifCenter fyne.CanvasObject
	var videoCenter fyne.CanvasObject
	var musicCenter fyne.CanvasObject

	openBtn := widget.NewButton("Open Image", nil)
	openGIFBtn := widget.NewButton("GIF", nil)
	openVideoBtn := widget.NewButton("Video", nil)
	musicBtn := widget.NewButton("Music", nil)

	setMode := func(mode string) {
		switch mode {
		case "gif":
			centerHolder.Objects[0] = gifCenter
		case "video":
			centerHolder.Objects[0] = videoCenter
		case "music":
			centerHolder.Objects[0] = musicCenter
		default:
			centerHolder.Objects[0] = mainSplit
		}
		tint := func(b *widget.Button, active bool) {
			if active {
				b.Importance = widget.HighImportance
			} else {
				b.Importance = widget.MediumImportance
			}
			b.Refresh()
		}
		tint(openBtn, mode == "image")
		tint(openGIFBtn, mode == "gif")
		tint(openVideoBtn, mode == "video")
		tint(musicBtn, mode == "music")
		centerHolder.Refresh()
	}

	openBtn.OnTapped = func() {
		setMode("image")
		s.openImage()
	}
	openGIFBtn.OnTapped = func() {
		if gifCenter == nil {
			gifCenter, s.gif = buildGifEditorContent(win, s.cfg)
		}
		setMode("gif")
		if s.gif != nil && len(s.gif.srcFrames) == 0 {
			s.gif.openGIF()
		}
	}
	openVideoBtn.OnTapped = func() {
		if videoCenter == nil {
			videoCenter, s.video = buildFramesEditorContent(win, s.cfg, true)
		}
		setMode("video")
		if s.video != nil && len(s.video.srcFrames) == 0 {
			s.video.openVideo()
		}
	}
	musicBtn.OnTapped = func() {
		if musicCenter == nil {
			musicCenter, s.music = buildMusicEditorContent(win, s.cfg)
		}
		setMode("music")
	}

	var installBtn *widget.Button
	installBtn = widget.NewButton("Install Dependency", func() {
		installBtn.Disable()
		go func() {
			defer installBtn.Enable()
			home, err := downloadAllDeps(func(msg string) { s.setStatus(msg) })
			if err != nil {
				s.setStatus("Dependency download failed: " + err.Error())
				return
			}
			s.cfg.GBDKHome = home
			if s.settingsRefresh != nil {
				s.settingsRefresh()
			}
			s.setStatus("GBDK + ffmpeg installed → " + home)
		}()
	})

	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(openBtn, openGIFBtn, openVideoBtn, musicBtn),
		installBtn)
	setMode("image")

	content := container.NewBorder(toolbar, s.statusBar, nil, nil, centerHolder)
	s.photoContent = content
	win.SetContent(content)
	win.ShowAndRun()
}

func (s *appState) openImage() {
	go func() {
		filename, err := sqDialog.File().
			Filter("Image Files", "jpg", "jpeg", "png").
			Title("Open Image").
			Load()
		if err != nil {
			if err != sqDialog.ErrCancelled {
				s.setStatus("File error: " + err.Error())
			}
			return
		}
		s.loadImageFromPath(filename)
	}()
}

func (s *appState) loadImageFromPath(path string) {
	f, err := os.Open(path)
	if err != nil {
		s.setStatus("Cannot open: " + err.Error())
		return
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
	var img image.Image
	switch ext {
	case ".png":
		img, err = png.Decode(f)
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
	default:
		s.setStatus("Unsupported format: " + ext)
		return
	}
	if err != nil {
		s.setStatus("Decode error: " + err.Error())
		return
	}

	s.srcImage = img
	s.cfg.CropRect = image.Rectangle{}
	s.cfg.CropEnabled = false
	s.bilCache = BilCache{}

	s.cropWidget.SetImage(img)
	s.result = nil

	name := sanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	s.cfg.Name = name

	b := img.Bounds()
	if s.setImageLayout != nil {
		s.setImageLayout(b.Dx() > b.Dy())
	}
	if s.rebuildPresetsFunc != nil {
		s.rebuildPresetsFunc()
	}
	if s.settingsRefresh != nil {
		s.settingsRefresh()
	}
	s.setStatus(fmt.Sprintf("Loaded: %s  (%dx%d)", filepath.Base(path), b.Dx(), b.Dy()))

	s.scheduleAutoConvert()
}

func (s *appState) doConvertPixelArt() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	s.setStatus("Converting (pixel art)…")
	go func() {
		result, err := runPixelArtPipeline(s.srcImage, s.cfg, &s.bilCache)
		if err != nil {
			s.setStatus("Error: " + err.Error())
			return
		}
		s.result = &result
		if result.ProcessedImage != nil {
			s.outputCanvas.Image = result.ProcessedImage
			s.outputCanvas.Refresh()
			b := result.ProcessedImage.Bounds()
			s.setStatus(fmt.Sprintf("Done — output %dx%d", b.Dx(), b.Dy()))
		}
	}()
}

func (s *appState) doConvertGB() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	s.setStatus("Converting (GB/GBC)…")
	go func() {
		result, err := runGBPipeline(s.srcImage, s.cfg, &s.bilCache)
		if err != nil {
			s.setStatus("Error: " + err.Error())
			return
		}
		s.result = &result
		if result.FullColor != nil {
			s.outputCanvas.Image = result.FullColor
			s.outputCanvas.Refresh()
			b := result.FullColor.Bounds()
			s.setStatus(fmt.Sprintf("Preview ready — %dx%d", b.Dx(), b.Dy()))
		}
	}()
}

func (s *appState) exportNow() error {
	if s.result == nil || s.result.FullColor == nil {
		return fmt.Errorf("convert first")
	}
	dir, name := s.cfg.OutputDir, s.cfg.Name
	full := s.result.FullColor

	var err error
	switch {
	case s.cfg.Mode == ModeDMG:
		tiles, pal := tileficationDMG(full)
		err = ExportGBDKDMG(dir, name, tiles, pal[0])
	case s.cfg.HiColor:
		err = ExportGBDKHiColor(dir, name, s.cfg.GBDKHome, full)
	default:
		tiles, pals := tilefication(full)
		err = ExportGBDK(dir, name, tiles, pals)
	}
	if err != nil {
		return err
	}
	_ = GenerateBatchFile(s.cfg)
	_ = saveImage(full, filepath.Join(dir, name+"_preview.jpg"))
	return nil
}

func (s *appState) doCompile() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	s.setStatus("Converting…")
	go func() {
		result, err := runGBPipeline(s.srcImage, s.cfg, &s.bilCache)
		if err != nil {
			s.setStatus("Error: " + err.Error())
			return
		}
		s.result = &result
		if result.FullColor != nil {
			s.outputCanvas.Image = result.FullColor
			s.outputCanvas.Refresh()
		}

		s.setStatus("Exporting…")
		if err := s.exportNow(); err != nil {
			s.setStatus("Export error: " + err.Error())
			return
		}

		s.setStatus("Compiling…")
		res := CompileGB(s.cfg)
		if res.Success {
			s.setStatus("Compiled OK → " + res.ROMPath)
		} else {
			s.setStatus("Compile failed — see output.")
		}
		showOutputDialog(s.win, "Compile Output", res.Output)
	}()
}

func (s *appState) refreshGalleryLabel() {
	if s.galleryLabel != nil {
		s.galleryLabel.SetText(fmt.Sprintf("Gallery: %d image(s)", len(s.galleryImages)))
	}
}

func (s *appState) doAddToGallery() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	go func() {
		result, err := runGBPipeline(s.srcImage, s.cfg, &s.bilCache)
		if err != nil || result.FullColor == nil {
			s.setStatus("Could not process image for the gallery.")
			return
		}
		s.galleryImages = append(s.galleryImages, result.FullColor)
		s.refreshGalleryLabel()
		s.setStatus(fmt.Sprintf("Added to gallery (%d image(s)).", len(s.galleryImages)))
	}()
}

func (s *appState) doClearGallery() {
	s.galleryImages = nil
	s.refreshGalleryLabel()
	s.setStatus("Gallery cleared.")
}

func (s *appState) doCompileGallery() {
	if len(s.galleryImages) == 0 {
		s.setStatus("Add images to the gallery first.")
		return
	}
	go func() {
		s.setStatus("Exporting gallery…")
		banks, err := ExportGBDKImageGallery(s.cfg, s.galleryImages)
		if err != nil {
			s.setStatus("Gallery export error: " + err.Error())
			return
		}
		s.cfg.ROMBanks = banks
		s.cfg.HiColor = true
		s.cfg.Mode = ModeCGB
		s.setStatus("Compiling gallery ROM…")
		res := CompileGB(s.cfg)
		if res.Success {
			s.setStatus(fmt.Sprintf("Gallery ROM (%d images) → %s", len(s.galleryImages), res.ROMPath))
		} else {
			s.setStatus("Compile failed — see output.")
		}
		showOutputDialog(s.win, "Gallery Compile Output", res.Output)
	}()
}

func (s *appState) doApplyBilateral() {
	s.bilCache = BilCache{}
	if s.activeTab == "GB / GBC" {
		s.doConvertGB()
	} else {
		s.doConvertPixelArt()
	}
}

func (s *appState) saveAsPNG() {
	if s.result == nil || s.result.ProcessedImage == nil {
		s.setStatus("Nothing to save — convert first.")
		return
	}
	go func() {
		path, err := sqDialog.File().Filter("PNG Image", "png").Title("Save as PNG").Save()
		if err != nil {
			if err != sqDialog.ErrCancelled {
				s.setStatus("Save error: " + err.Error())
			}
			return
		}
		if !strings.HasSuffix(strings.ToLower(path), ".png") {
			path += ".png"
		}
		f, err := os.Create(path)
		if err != nil {
			s.setStatus("Save error: " + err.Error())
			return
		}
		defer f.Close()
		if err := png.Encode(f, s.result.ProcessedImage); err != nil {
			s.setStatus("Encode error: " + err.Error())
			return
		}
		s.setStatus("Saved → " + path)
	}()
}

func (s *appState) copyToClipboard() {
	if s.result == nil || s.result.ProcessedImage == nil {
		s.setStatus("Nothing to copy — convert first.")
		return
	}
	go func() {
		tmp, err := os.CreateTemp("", "mediaboy_clip_*.png")
		if err != nil {
			s.setStatus("Copy failed: " + err.Error())
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if err := png.Encode(tmp, s.result.ProcessedImage); err != nil {
			tmp.Close()
			s.setStatus("Copy failed: " + err.Error())
			return
		}
		tmp.Close()

		ps := fmt.Sprintf(
			`Add-Type -AssemblyName System.Drawing,System.Windows.Forms;`+
				`$i=[System.Drawing.Image]::FromFile('%s');`+
				`[System.Windows.Forms.Clipboard]::SetImage($i);`+
				`$i.Dispose()`,
			strings.ReplaceAll(tmpPath, `'`, `''`))
		if err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Run(); err != nil {
			s.setStatus("Copy failed: " + err.Error())
			return
		}
		s.setStatus("Copied to clipboard.")
	}()
}

func (s *appState) scheduleAutoConvert() {
	s.autoTimerMu.Lock()
	defer s.autoTimerMu.Unlock()
	if s.autoTimer != nil {
		s.autoTimer.Stop()
	}
	s.autoTimer = time.AfterFunc(120*time.Millisecond, func() {
		if s.activeTab == "GB / GBC" {
			s.doConvertGB()
		} else {
			s.doConvertPixelArt()
		}
	})
}

func (s *appState) snapGB(drawn image.Rectangle, bounds image.Rectangle) image.Rectangle {
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
	r := image.Rect(cx-nw/2, cy-nh/2, cx+nw/2, cy+nh/2)
	return clampRect(r, bounds)
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

func (s *appState) refreshCropInfo() {
	cr := s.cfg.CropRect
	var text string
	if cr.Empty() {
		text = "Draw on the image to crop"
	} else {
		text = fmt.Sprintf("Crop: %d×%d px", cr.Dx(), cr.Dy())
	}
	if s.paCropLabel != nil {
		s.paCropLabel.SetText(text)
	}
	if s.gbCropLabel != nil {
		s.gbCropLabel.SetText(text)
	}
}

func (s *appState) setStatus(msg string) {
	if s.statusBar != nil {
		s.statusBar.SetText(msg)
	}
}

func showOutputDialog(win fyne.Window, title, text string) {
	entry := widget.NewMultiLineEntry()
	entry.SetText(text)
	entry.Disable()
	d := dialog.NewCustom(title, "Close", container.NewScroll(entry), win)
	d.Resize(fyne.NewSize(600, 400))
	d.Show()
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

func openOutputFolder(dir string) {
	if dir == "" {
		dir = "out"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	_ = os.MkdirAll(dir, 0755)
	_ = exec.Command("explorer", dir).Start()
}
