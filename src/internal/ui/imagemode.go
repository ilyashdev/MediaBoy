package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"MediaBoy/internal/core"
	"MediaBoy/internal/gbdk"
	"MediaBoy/internal/imaging"
)

// Threading: widgets and appState fields are only touched on the UI thread.
// Background work receives copies of what it needs (source image, config) and
// hands results back through fyne.Do.

// loadImageFromPath runs on a background goroutine (file dialog callback).
func (s *appState) loadImageFromPath(path string) {
	img, err := imaging.DecodeImageFile(path)
	if err != nil {
		s.setStatus("Cannot open image: " + err.Error())
		return
	}
	uiDo(func() {
		s.srcImage = img
		s.cfg.CropRect = image.Rectangle{}
		s.cfg.CropEnabled = false
		s.bilCache.Reset()

		s.cropWidget.SetImage(img)
		s.result = nil
		for _, b := range s.outputBtns {
			b.Enable()
		}
		s.cfg.Name = sanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))

		b := img.Bounds()
		s.setImageLayout(b.Dx() > b.Dy())
		if s.rebuildPresetsFunc != nil {
			s.rebuildPresetsFunc()
		}
		if s.settingsRefresh != nil {
			s.settingsRefresh()
		}
		s.setStatus(fmt.Sprintf("Loaded: %s  (%dx%d)", filepath.Base(path), b.Dx(), b.Dy()))
		s.scheduleAutoConvert()
	})
}

// scheduleAutoConvert debounces preview updates after a setting change.
func (s *appState) scheduleAutoConvert() {
	if s.srcImage == nil {
		return
	}
	s.autoTimerMu.Lock()
	defer s.autoTimerMu.Unlock()
	if s.autoTimer != nil {
		s.autoTimer.Stop()
	}
	s.autoTimer = time.AfterFunc(120*time.Millisecond, func() { uiDo(s.convertPreview) })
}

// convertPreview (UI thread) renders the preview for the active tab. Results
// of an older run that finishes after a newer one started are dropped.
func (s *appState) convertPreview() {
	if s.srcImage == nil {
		return
	}
	s.previewGen++
	gen, src, cfg, gb := s.previewGen, s.srcImage, s.cfg, s.activeTab == tabGB
	if gb {
		s.setStatus("Converting (GB/GBC)…")
	} else {
		s.setStatus("Converting (pixel art)…")
	}
	goSafe(s.setStatus, func() {
		var result imaging.ConvertResult
		var err error
		if gb {
			result, err = imaging.RunGBPipeline(src, cfg, &s.bilCache)
		} else {
			result, err = imaging.RunPixelArtPipeline(src, cfg, &s.bilCache)
		}
		uiDo(func() {
			if gen != s.previewGen {
				return
			}
			if err != nil {
				s.setStatus("Error: " + err.Error())
				return
			}
			s.result = &result
			shown, what := result.ProcessedImage, "Done — output"
			if gb {
				shown, what = result.FullColor, "Preview ready —"
			}
			if shown != nil {
				s.outputCanvas.Image = shown
				s.outputCanvas.Refresh()
				b := shown.Bounds()
				s.setStatus(fmt.Sprintf("%s %dx%d", what, b.Dx(), b.Dy()))
			}
		})
	})
}

func (s *appState) doApplyBilateral() {
	s.bilCache.Reset()
	s.scheduleAutoConvert()
}

// exportImage writes the GBDK sources for a converted full-colour image.
func exportImage(cfg core.ConvertConfig, full image.Image) error {
	dir, name := cfg.OutputDir, cfg.Name
	var err error
	switch {
	case cfg.Mode == core.ModeDMG:
		tiles, pal := imaging.TileficationDMG(full)
		err = gbdk.ExportGBDKDMG(dir, name, tiles, pal[0])
	case cfg.HiColor:
		err = gbdk.ExportGBDKHiColor(dir, name, cfg.GBDKHome, full)
	default:
		tiles, pals := imaging.Tilefication(full)
		err = gbdk.ExportGBDK(dir, name, tiles, pals)
	}
	if err != nil {
		return err
	}
	_ = gbdk.GenerateBatchFile(cfg)
	_ = imaging.SaveImage(full, filepath.Join(dir, name+"_preview.jpg"))
	return nil
}

func (s *appState) doCompile() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	src, cfg := s.srcImage, s.cfg
	runJob(&s.busy, s.setStatus, func() {
		s.setStatus("Converting…")
		result, err := imaging.RunGBPipeline(src, cfg, &s.bilCache)
		if err != nil || result.FullColor == nil {
			s.setStatus(fmt.Sprintf("Error: %v", err))
			return
		}
		uiDo(func() {
			s.result = &result
			s.outputCanvas.Image = result.FullColor
			s.outputCanvas.Refresh()
		})

		s.setStatus("Exporting…")
		if err := exportImage(cfg, result.FullColor); err != nil {
			s.setStatus("Export error: " + err.Error())
			return
		}

		s.setStatus("Compiling…")
		res := gbdk.CompileGB(cfg)
		if res.Success {
			s.setStatus("Compiled OK → " + res.ROMPath)
		} else {
			s.setStatus("Compile failed — see output.")
		}
		uiDo(func() { showOutputDialog(s.win, "Compile Output", res.Output) })
	})
}

func (s *appState) doAddToGallery() {
	if s.srcImage == nil {
		s.setStatus("Open an image first.")
		return
	}
	src, cfg := s.srcImage, s.cfg
	goSafe(s.setStatus, func() {
		result, err := imaging.RunGBPipeline(src, cfg, &s.bilCache)
		if err != nil || result.FullColor == nil {
			s.setStatus("Could not process image for the gallery.")
			return
		}
		uiDo(func() {
			s.galleryImages = append(s.galleryImages, result.FullColor)
			s.refreshGalleryLabel()
			s.setStatus(fmt.Sprintf("Added to gallery (%d image(s)).", len(s.galleryImages)))
		})
	})
}

func (s *appState) doCompileGallery() {
	if len(s.galleryImages) == 0 {
		s.setStatus("Add images to the gallery first.")
		return
	}
	images := append([]image.Image(nil), s.galleryImages...)
	cfg := s.cfg
	cfg.HiColor = true
	cfg.Mode = core.ModeCGB
	runJob(&s.busy, s.setStatus, func() {
		s.setStatus("Exporting gallery…")
		banks, err := gbdk.ExportGBDKImageGallery(cfg, images)
		if err != nil {
			s.setStatus("Gallery export error: " + err.Error())
			return
		}
		cfg.ROMBanks = banks
		s.setStatus("Compiling gallery ROM…")
		res := gbdk.CompileGB(cfg)
		if res.Success {
			s.setStatus(fmt.Sprintf("Gallery ROM (%d images) → %s", len(images), res.ROMPath))
		} else {
			s.setStatus("Compile failed — see output.")
		}
		uiDo(func() { showOutputDialog(s.win, "Gallery Compile Output", res.Output) })
	})
}

func (s *appState) copyToClipboard() {
	if s.result == nil || s.result.ProcessedImage == nil {
		s.setStatus("Nothing to copy yet — wait for the preview.")
		return
	}
	img := s.result.ProcessedImage
	goSafe(s.setStatus, func() {
		tmp, err := os.CreateTemp("", "mediaboy_clip_*.png")
		if err != nil {
			s.setStatus("Copy failed: " + err.Error())
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if err := png.Encode(tmp, img); err != nil {
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
	})
}
