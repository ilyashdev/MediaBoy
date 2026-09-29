package ui

import (
	"fmt"
	"image"
	"math"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"MediaBoy/internal/core"
)

func buildSettingsPanel(s *appState) (fyne.CanvasObject, func()) {
	paTab := buildPixelArtTab(s)
	gbTab, gbRefresh := buildGBTab(s)

	tabs := container.NewAppTabs(
		container.NewTabItem(tabPixelArt, paTab),
		container.NewTabItem(tabGB, gbTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	s.cropWidget.FixAspect = false
	s.cropWidget.SnapFunc = s.snapPixelArt
	tabs.OnSelected = func(tab *container.TabItem) {
		s.activeTab = tab.Text
		if tab.Text == tabGB {
			s.cropWidget.FixAspect = true
			s.cropWidget.SnapFunc = snapGBRect
		} else {
			s.cropWidget.FixAspect = false
			s.cropWidget.SnapFunc = s.snapPixelArt
		}
		s.scheduleAutoConvert()
	}

	refresh := func() {
		gbRefresh()
		s.refreshCropInfo()
	}
	return tabs, refresh
}

func buildDitheringBody(s *appState) fyne.CanvasObject {
	ditherLabels := []string{"None", "Bayer", "Floyd-Steinberg", "Atkinson"}

	strengthVal := widget.NewLabel(fmt.Sprintf("%.2f", s.cfg.DitheringStrength))
	strengthSlider := widget.NewSlider(0, 1)
	strengthSlider.Step = 0.05
	strengthSlider.SetValue(s.cfg.DitheringStrength)
	strengthSlider.OnChanged = func(v float64) {
		s.cfg.DitheringStrength = v
		strengthVal.SetText(fmt.Sprintf("%.2f", v))
		s.scheduleAutoConvert()
	}

	levelsVal := widget.NewLabel(strconv.Itoa(s.cfg.DitheringLevels))
	levelsSlider := widget.NewSlider(2, 16)
	levelsSlider.Step = 1
	levelsSlider.SetValue(float64(s.cfg.DitheringLevels))
	levelsSlider.OnChanged = func(v float64) {
		s.cfg.DitheringLevels = int(v)
		levelsVal.SetText(strconv.Itoa(int(v)))
		s.scheduleAutoConvert()
	}
	levelsForm := widget.NewForm(widget.NewFormItem("Levels", sliderRow(levelsSlider, levelsVal)))
	showLevels := func(t core.DitheringType) {
		if t == core.DitheringFloydSteinberg || t == core.DitheringAtkinson {
			levelsForm.Show()
		} else {
			levelsForm.Hide()
		}
	}
	showLevels(s.cfg.Dithering)

	ditherSelect := widget.NewSelect(ditherLabels, func(v string) {
		switch v {
		case "Bayer":
			s.cfg.Dithering = core.DitheringBayer
		case "Floyd-Steinberg":
			s.cfg.Dithering = core.DitheringFloydSteinberg
		case "Atkinson":
			s.cfg.Dithering = core.DitheringAtkinson
		default:
			s.cfg.Dithering = core.DitheringNone
		}
		showLevels(s.cfg.Dithering)
		s.scheduleAutoConvert()
	})
	ditherSelect.SetSelectedIndex(int(s.cfg.Dithering))

	return container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Type", ditherSelect),
			widget.NewFormItem("Strength", sliderRow(strengthSlider, strengthVal)),
		),
		levelsForm,
	)
}

func buildPixelArtTab(s *appState) fyne.CanvasObject {
	// Crop
	s.paCropLabel = widget.NewLabel(cropInfoText(s.cfg.CropRect))
	s.paCropLabel.Wrapping = fyne.TextWrapWord
	cropSec := newSection("Crop", container.NewVBox(
		s.paCropLabel,
		widget.NewButtonWithIcon("Reset crop", theme.ContentUndoIcon(), s.resetCrop),
		hintLabel("The selection snaps to the target aspect ratio."),
	))

	// Target resolution
	paOutputLabel := widget.NewLabel("")
	updatePAOutput := func() {
		sc := s.cfg.OutputScale
		if sc < 1 {
			sc = 1
		}
		paOutputLabel.SetText(fmt.Sprintf("Saved PNG: %d×%d px", s.cfg.TargetW*sc, s.cfg.TargetH*sc))
	}
	sizeChanged := func() {
		updatePAOutput()
		s.scheduleAutoConvert()
	}

	linkAspect := true
	var linkGuard bool
	sourceAspect := func() float64 {
		if s.cfg.CropEnabled && !s.cfg.CropRect.Empty() {
			if dy := s.cfg.CropRect.Dy(); dy > 0 {
				return float64(s.cfg.CropRect.Dx()) / float64(dy)
			}
		}
		if s.srcImage != nil {
			if b := s.srcImage.Bounds(); b.Dy() > 0 {
				return float64(b.Dx()) / float64(b.Dy())
			}
		}
		if s.cfg.TargetH > 0 {
			return float64(s.cfg.TargetW) / float64(s.cfg.TargetH)
		}
		return 1.0
	}

	targetWEntry := widget.NewEntry()
	targetHEntry := widget.NewEntry()
	targetWEntry.SetText(strconv.Itoa(s.cfg.TargetW))
	targetHEntry.SetText(strconv.Itoa(s.cfg.TargetH))
	targetWEntry.OnChanged = func(v string) {
		w, err := strconv.Atoi(v)
		if err != nil || w <= 0 {
			return
		}
		s.cfg.TargetW = w
		if linkAspect && !linkGuard {
			newH := int(math.Max(1, math.Round(float64(w)/sourceAspect())))
			s.cfg.TargetH = newH
			linkGuard = true
			targetHEntry.SetText(strconv.Itoa(newH))
			linkGuard = false
		}
		sizeChanged()
	}
	targetHEntry.OnChanged = func(v string) {
		h, err := strconv.Atoi(v)
		if err != nil || h <= 0 {
			return
		}
		s.cfg.TargetH = h
		if linkAspect && !linkGuard {
			newW := int(math.Max(1, math.Round(float64(h)*sourceAspect())))
			s.cfg.TargetW = newW
			linkGuard = true
			targetWEntry.SetText(strconv.Itoa(newW))
			linkGuard = false
		}
		sizeChanged()
	}

	linkCheck := widget.NewCheck("Keep aspect ratio", func(v bool) { linkAspect = v })
	linkCheck.SetChecked(linkAspect)

	scaleSelect := widget.NewSelect([]string{"1×", "2×", "3×", "4×", "6×", "8×"}, func(v string) {
		if n, err := strconv.Atoi(v[:len(v)-len("×")]); err == nil {
			s.cfg.OutputScale = n
			sizeChanged()
		}
	})
	scaleSelect.SetSelected(fmt.Sprintf("%d×", s.cfg.OutputScale))

	type resOption struct{ w, h int }
	var currentOptions []resOption
	presetsSelect := widget.NewSelect(nil, nil)
	presetsSelect.PlaceHolder = "Presets (open an image)"
	presetsSelect.OnChanged = func(sel string) {
		for _, opt := range currentOptions {
			if fmt.Sprintf("%d×%d", opt.w, opt.h) == sel {
				linkGuard = true
				targetWEntry.SetText(strconv.Itoa(opt.w))
				targetHEntry.SetText(strconv.Itoa(opt.h))
				linkGuard = false
				s.cfg.TargetW, s.cfg.TargetH = opt.w, opt.h
				sizeChanged()
				return
			}
		}
	}
	s.rebuildPresetsFunc = func() {
		var srcW, srcH int
		if s.cfg.CropEnabled && !s.cfg.CropRect.Empty() {
			srcW, srcH = s.cfg.CropRect.Dx(), s.cfg.CropRect.Dy()
		} else if s.srcImage != nil {
			b := s.srcImage.Bounds()
			srcW, srcH = b.Dx(), b.Dy()
		}
		currentOptions = nil
		var labels []string
		if srcW > 0 && srcH > 0 {
			aspect := float64(srcW) / float64(srcH)
			for v := 48; v <= 192; v += 24 {
				pw, ph := v, int(math.Round(float64(v)/aspect))
				if srcW > srcH {
					pw, ph = int(math.Round(float64(v)*aspect)), v
				}
				if pw > 0 && ph > 0 {
					currentOptions = append(currentOptions, resOption{pw, ph})
					labels = append(labels, fmt.Sprintf("%d×%d", pw, ph))
				}
			}
			presetsSelect.PlaceHolder = "Presets"
		}
		presetsSelect.Options = labels
		presetsSelect.ClearSelected()
		presetsSelect.Refresh()
	}
	updatePAOutput()

	sizeSec := newSection("Target Resolution", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Width", targetWEntry),
			widget.NewFormItem("Height", targetHEntry),
		),
		linkCheck,
		presetsSelect,
		widget.NewForm(widget.NewFormItem("PNG scale", scaleSelect)),
		paOutputLabel,
	))

	// Scaling
	scalingSelect := widget.NewSelect([]string{"Median", "Bilinear", "Nearest"}, func(v string) {
		s.cfg.Scaling = scalingFromString(v)
		s.scheduleAutoConvert()
	})
	scalingSelect.SetSelectedIndex(int(s.cfg.Scaling))
	upscalerSelect := widget.NewSelect([]string{"Nearest", "Scale2x"}, func(v string) {
		if v == "Scale2x" {
			s.cfg.Upscaler = core.UpscalerScale2x
		} else {
			s.cfg.Upscaler = core.UpscalerNearest
		}
		s.scheduleAutoConvert()
	})
	upscalerSelect.SetSelectedIndex(int(s.cfg.Upscaler))
	scalingSec := newSection("Scaling", widget.NewForm(
		widget.NewFormItem("Downscale", scalingSelect),
		widget.NewFormItem("Upscale", upscalerSelect),
	))

	// Bilateral (expensive: applied on demand)
	radiusVal := widget.NewLabel(strconv.Itoa(s.cfg.BilateralRadius))
	radiusSlider := widget.NewSlider(0, 32)
	radiusSlider.Step = 1
	radiusSlider.SetValue(float64(s.cfg.BilateralRadius))
	radiusSlider.OnChanged = func(v float64) {
		s.cfg.BilateralRadius = int(v)
		radiusVal.SetText(strconv.Itoa(int(v)))
	}
	sigmaVal := widget.NewLabel(fmt.Sprintf("%.0f", s.cfg.BilateralSigma))
	sigmaSlider := widget.NewSlider(1, 150)
	sigmaSlider.Step = 1
	sigmaSlider.SetValue(s.cfg.BilateralSigma)
	sigmaSlider.OnChanged = func(v float64) {
		s.cfg.BilateralSigma = v
		sigmaVal.SetText(fmt.Sprintf("%.0f", v))
	}
	bilSec := newSection("Bilateral Filter", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Radius", sliderRow(radiusSlider, radiusVal)),
			widget.NewFormItem("Sigma σ", sliderRow(sigmaSlider, sigmaVal)),
		),
		widget.NewButtonWithIcon("Apply", theme.ViewRefreshIcon(), s.doApplyBilateral),
		hintLabel("Slow filter — press Apply after changing Radius or Sigma."),
	)).withSwitch(s.cfg.BilateralEnabled, func(v bool) {
		s.cfg.BilateralEnabled = v
		s.scheduleAutoConvert()
	})

	// Sharpen
	sharpenVal := widget.NewLabel(fmt.Sprintf("%.1f", s.cfg.SharpenAmount))
	sharpenSlider := widget.NewSlider(0, 3)
	sharpenSlider.Step = 0.1
	sharpenSlider.SetValue(s.cfg.SharpenAmount)
	sharpenSlider.OnChanged = func(v float64) {
		s.cfg.SharpenAmount = v
		sharpenVal.SetText(fmt.Sprintf("%.1f", v))
		s.scheduleAutoConvert()
	}
	sharpenSec := newSection("Sharpen", widget.NewForm(
		widget.NewFormItem("Amount", sliderRow(sharpenSlider, sharpenVal)),
	)).withSwitch(s.cfg.SharpenEnabled, func(v bool) {
		s.cfg.SharpenEnabled = v
		s.scheduleAutoConvert()
	})

	// Posterize
	postVal := widget.NewLabel(strconv.Itoa(s.cfg.PosterizeLevels))
	postSlider := widget.NewSlider(2, 16)
	postSlider.Step = 1
	postSlider.SetValue(float64(s.cfg.PosterizeLevels))
	postSlider.OnChanged = func(v float64) {
		s.cfg.PosterizeLevels = int(v)
		postVal.SetText(strconv.Itoa(int(v)))
		s.scheduleAutoConvert()
	}
	posterizeSec := newSection("Posterize", widget.NewForm(
		widget.NewFormItem("Levels", sliderRow(postSlider, postVal)),
	)).withSwitch(s.cfg.PosterizeEnabled, func(v bool) {
		s.cfg.PosterizeEnabled = v
		s.scheduleAutoConvert()
	})

	// Dithering
	ditherSec := newSection("Dithering", buildDitheringBody(s)).withSwitch(s.cfg.DitheringEnabled, func(v bool) {
		s.cfg.DitheringEnabled = v
		s.scheduleAutoConvert()
	})

	// Color reduction: k-means and tilefication are mutually exclusive.
	kmeansVal := widget.NewLabel(strconv.Itoa(s.cfg.KmeansColors))
	kmeansSlider := widget.NewSlider(2, 64)
	kmeansSlider.Step = 1
	kmeansSlider.SetValue(float64(s.cfg.KmeansColors))
	kmeansSlider.OnChanged = func(v float64) {
		s.cfg.KmeansColors = int(v)
		kmeansVal.SetText(strconv.Itoa(int(v)))
		s.scheduleAutoConvert()
	}
	kmeansForm := widget.NewForm(widget.NewFormItem("Colors", sliderRow(kmeansSlider, kmeansVal)))

	const (
		colorOff    = "Off"
		colorKmeans = "K-means palette"
		colorTiles  = "Game Boy tiles"
	)
	colorRadio := widget.NewRadioGroup([]string{colorOff, colorKmeans, colorTiles}, func(v string) {
		s.cfg.GBCFilter = v == colorKmeans
		s.cfg.TileficationFilter = v == colorTiles
		if s.cfg.GBCFilter {
			kmeansForm.Show()
		} else {
			kmeansForm.Hide()
		}
		s.scheduleAutoConvert()
	})
	colorRadio.Required = true
	switch {
	case s.cfg.GBCFilter:
		colorRadio.SetSelected(colorKmeans)
	case s.cfg.TileficationFilter:
		colorRadio.SetSelected(colorTiles)
	default:
		colorRadio.SetSelected(colorOff)
	}
	colorSec := newSection("Color Reduction", container.NewVBox(colorRadio, kmeansForm))

	return panelColumn(
		cropSec.tile, sizeSec.tile, scalingSec.tile, bilSec.tile,
		sharpenSec.tile, posterizeSec.tile, ditherSec.tile, colorSec.tile,
	)
}

func buildGBTab(s *appState) (fyne.CanvasObject, func()) {
	s.cfg.Mode = core.ModeCGB
	s.cfg.HiColor = true

	s.gbCropLabel = widget.NewLabel(cropInfoText(s.cfg.CropRect))
	s.gbCropLabel.Wrapping = fyne.TextWrapWord
	cropSec := newSection("Crop", container.NewVBox(
		s.gbCropLabel,
		widget.NewButtonWithIcon("Reset crop", theme.ContentUndoIcon(), s.resetCrop),
		hintLabel("The selection snaps to the Game Boy screen (160×144)."),
	))

	nameEntry := widget.NewEntry()
	nameEntry.SetText(s.cfg.Name)
	nameEntry.SetPlaceHolder("project")
	nameEntry.OnChanged = func(v string) { s.cfg.Name = sanitizeName(v) }

	gbdkEntry := widget.NewEntry()
	gbdkEntry.SetText(s.cfg.GBDKHome)
	gbdkEntry.SetPlaceHolder(core.DefaultConfig().GBDKHome)
	gbdkEntry.OnChanged = func(v string) { s.cfg.GBDKHome = v }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(s.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("out")
	outDirEntry.OnChanged = func(v string) { s.cfg.OutputDir = v }

	outputSec := newSection("Output", widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("GBDK Home", gbdkEntry),
		widget.NewFormItem("Output Dir", outDirEntry),
	))

	s.galleryLabel = widget.NewLabel("")
	s.refreshGalleryLabel()
	galCompileBtn := widget.NewButton("Compile Gallery ROM", func() { s.doCompileGallery() })
	gallerySec := newSection("Image Gallery", container.NewVBox(
		hintLabel("Collect several images into one ROM. On the device: ◀ ▶ switch, Start prints."),
		s.galleryLabel,
		container.NewGridWithColumns(2,
			widget.NewButtonWithIcon("Add current", theme.ContentAddIcon(), func() { s.doAddToGallery() }),
			widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), func() { s.doClearGallery() }),
		),
		galCompileBtn,
	))

	compileBtn := widget.NewButtonWithIcon("Compile ROM", theme.MediaPlayIcon(), func() { s.doCompile() })
	compileBtn.Importance = widget.HighImportance
	openDirBtn := widget.NewButtonWithIcon("Open output folder", theme.FolderIcon(), func() { openOutputFolder(s.cfg.OutputDir) })
	s.outputBtns = append(s.outputBtns, compileBtn)

	actionBox := container.NewVBox(compileBtn, openDirBtn)

	refresh := func() {
		nameEntry.SetText(s.cfg.Name)
		gbdkEntry.SetText(s.cfg.GBDKHome)
		outDirEntry.SetText(s.cfg.OutputDir)
		s.refreshCropInfo()
	}
	return container.NewBorder(nil, actionBox, nil, nil,
		panelColumn(cropSec.tile, outputSec.tile, gallerySec.tile)), refresh
}

func scalingFromString(v string) core.ScalingType {
	switch v {
	case "Bilinear":
		return core.ScalingBilinear
	case "Nearest":
		return core.ScalingNearest
	default:
		return core.ScalingMedian
	}
}

// snapGBRect snaps a drawn crop to an integer multiple of the GB screen,
// centred on the drawn area.
func snapGBRect(drawn image.Rectangle, bounds image.Rectangle) image.Rectangle {
	if drawn.Empty() {
		return drawn
	}
	cx := (drawn.Min.X + drawn.Max.X) / 2
	cy := (drawn.Min.Y + drawn.Max.Y) / 2
	scale := min(drawn.Dx()/core.ScreenW, drawn.Dy()/core.ScreenH)
	if scale < 1 {
		scale = 1
	}
	nw, nh := core.ScreenW*scale, core.ScreenH*scale
	return clampRect(image.Rect(cx-nw/2, cy-nh/2, cx+nw/2, cy+nh/2), bounds)
}
