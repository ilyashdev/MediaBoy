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

	settingsRefresh    func()
	setImageLayout     func(landscape bool)
	rebuildPresetsFunc func()

	// bilateral cache
	bilCache BilCache

	// auto-convert on fast-param change
	activeTab   string
	autoTimer   *time.Timer
	autoTimerMu sync.Mutex
}

func runUI() {
	a := app.NewWithID("com.mediaboy.app")
	win := a.NewWindow("MediaBoy")
	win.Resize(fyne.NewSize(1150, 720))

	s := &appState{
		cfg:       defaultConfig(),
		win:       win,
		activeTab: "Pixel Art",
	}

	s.statusBar = widget.NewLabel("Ready. Open an image to start.")

	cropW := NewCropWidget(func(r image.Rectangle) {
		s.cfg.CropRect = r
		s.cfg.CropEnabled = true
		s.refreshCropInfo()
		if s.rebuildPresetsFunc != nil {
			s.rebuildPresetsFunc()
		}
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

	// imageArea holds either an HSplit (portrait/square) or VSplit (landscape).
	// Replaced when an image is loaded based on its aspect ratio.
	imageArea := container.NewStack()
	s.setImageLayout = func(landscape bool) {
		var split *container.Split
		if landscape {
			// Wide image: input above, output below
			split = container.NewVSplit(inputPanel, outputPanel)
		} else {
			// Tall/square image: input left, output right
			split = container.NewHSplit(inputPanel, outputPanel)
		}
		split.Offset = 0.5
		imageArea.Objects = []fyne.CanvasObject{split}
		imageArea.Refresh()
	}
	s.setImageLayout(false) // default: side by side

	copyBtn := widget.NewButton("Copy", func() { s.copyToClipboard() })
	savePNGBtn := widget.NewButton("Save PNG", func() { s.saveAsPNG() })
	outActions := container.NewHBox(copyBtn, savePNGBtn)
	imageWithActions := container.NewBorder(nil, outActions, nil, nil, imageArea)

	settingsPanel, settingsRefresh := buildSettingsPanel(s)
	s.settingsRefresh = settingsRefresh

	mainSplit := container.NewHSplit(settingsPanel, imageWithActions)
	mainSplit.Offset = 0.24

	nameEntry := widget.NewEntry()
	nameEntry.SetText(s.cfg.Name)
	nameEntry.SetPlaceHolder("project name")
	nameEntry.OnChanged = func(v string) { s.cfg.Name = v }
	nameEntry.Resize(fyne.NewSize(140, 36))

	openBtn := widget.NewButton("Open Image", func() { s.openImage() })
	openBtn.Importance = widget.HighImportance

	openGIFBtn := widget.NewButton("GIF", func() {
		gifContent := buildGifEditorContent(win, s.cfg, func() {
			win.SetContent(s.photoContent)
		})
		win.SetContent(gifContent)
	})
	openVideoBtn := widget.NewButton("Video", func() {
		dialog.ShowInformation("Coming Soon", "Video import → GB video export: coming soon.", win)
	})
	musicBtn := widget.NewButton("Music", func() {
		dialog.ShowInformation("Coming Soon", "Music / SFX for GB: coming soon.", win)
	})

	nameBox := container.NewHBox(widget.NewLabel("Name:"), nameEntry)
	toolbar := container.NewHBox(openBtn, openGIFBtn, openVideoBtn, musicBtn, widget.NewSeparator(), nameBox)

	content := container.NewBorder(toolbar, s.statusBar, nil, nil, mainSplit)
	s.photoContent = content
	win.SetContent(content)
	win.ShowAndRun()
}

// ── File I/O ──────────────────────────────────────────────────────────────────

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
		s.setImageLayout(b.Dx() > b.Dy()) // landscape → VSplit; portrait/square → HSplit
	}
	if s.rebuildPresetsFunc != nil {
		s.rebuildPresetsFunc()
	}
	if s.settingsRefresh != nil {
		s.settingsRefresh()
	}
	s.setStatus(fmt.Sprintf("Loaded: %s  (%dx%d)", filepath.Base(path), b.Dx(), b.Dy()))
}

// ── Pixel Art convert ─────────────────────────────────────────────────────────

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

// ── GB/GBC convert ────────────────────────────────────────────────────────────

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
		if result.ProcessedImage != nil {
			s.outputCanvas.Image = result.ProcessedImage
			s.outputCanvas.Refresh()
		}
		tiles := "no"
		if result.Tiles != nil {
			h := len(result.Tiles)
			w := 0
			if h > 0 {
				w = len(result.Tiles[0])
			}
			tiles = fmt.Sprintf("%d", w*h)
		}
		b := result.ProcessedImage.Bounds()
		s.setStatus(fmt.Sprintf("Done — %dx%d px, %s tiles", b.Dx(), b.Dy(), tiles))
	}()
}

func (s *appState) doExport() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	if s.result == nil || s.result.Tiles == nil {
		s.setStatus("Run 'Convert to GB/GBC' first.")
		return
	}

	dir := s.cfg.OutputDir
	name := s.cfg.Name
	var err error
	if s.cfg.Mode == ModeDMG {
		err = ExportGBDKDMG(dir, name, s.result.Tiles, s.result.Palettes[0])
	} else {
		err = ExportGBDK(dir, name, s.result.Tiles, s.result.Palettes)
	}
	if err != nil {
		s.setStatus("Export error: " + err.Error())
		return
	}
	_ = GenerateBatchFile(s.cfg)
	_ = saveImage(s.result.ProcessedImage, filepath.Join(dir, name+"_preview.jpg"))
	s.setStatus(fmt.Sprintf("Exported → %s/%s.h  .c  main.c  compile.bat", dir, name))
}

func (s *appState) doCompile() {
	if s.result == nil || s.result.Tiles == nil {
		s.doConvertGB()
	}
	s.doExport()
	if s.result == nil {
		return
	}
	s.setStatus("Compiling…")
	go func() {
		res := CompileGB(s.cfg)
		if res.Success {
			s.setStatus("Compiled OK → " + res.ROMPath)
		} else {
			s.setStatus("Compile failed — see output.")
		}
		showOutputDialog(s.win, "Compile Output", res.Output)
	}()
}

// ── Bilateral apply ───────────────────────────────────────────────────────────

// doApplyBilateral clears the bilateral cache and runs the full pipeline,
// forcing the bilateral filter to recompute with the current radius/sigma.
func (s *appState) doApplyBilateral() {
	s.bilCache = BilCache{}
	if s.activeTab == "GB / GBC" {
		s.doConvertGB()
	} else {
		s.doConvertPixelArt()
	}
}

// ── Output actions ────────────────────────────────────────────────────────────

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
		// Use PowerShell to copy PNG into the Windows clipboard as a bitmap.
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

// ── Auto-convert (fast path, bilateral is cached) ────────────────────────────

// scheduleAutoConvert debounces rapid param changes (sharpen, scaling) into
// a single convert call 120 ms after the last change.
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

// ── Snap functions ────────────────────────────────────────────────────────────

// snapGB snaps to the largest N×160 × N×144 rectangle centered in drawn.
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

// snapPixelArt snaps to TargetW:TargetH aspect ratio, centered in drawn.
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

// ── Crop info ─────────────────────────────────────────────────────────────────

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

// ── Misc ──────────────────────────────────────────────────────────────────────

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
	return b.String()
}
