package main

import (
	"fmt"
	"image"
	"math"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func toggleSection(label string, enabled bool, onChange func(bool), body fyne.CanvasObject) fyne.CanvasObject {
	if !enabled {
		body.Hide()
	}
	check := widget.NewCheck(label, func(v bool) {
		if v {
			body.Show()
		} else {
			body.Hide()
		}
		onChange(v)
	})
	check.SetChecked(enabled)
	return container.NewVBox(check, body)
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

	levelsEntry := widget.NewEntry()
	levelsEntry.SetText(strconv.Itoa(s.cfg.DitheringLevels))
	levelsEntry.SetPlaceHolder("4")
	levelsEntry.OnChanged = func(v string) {
		if l, err := strconv.Atoi(v); err == nil && l >= 2 && l <= 64 {
			s.cfg.DitheringLevels = l
		}
	}

	levelsForm := widget.NewForm(
		widget.NewFormItem("Levels (FS/Atk)", levelsEntry),
	)
	if s.cfg.Dithering == DitheringNone || s.cfg.Dithering == DitheringBayer {
		levelsForm.Hide()
	}

	ditherSelect := widget.NewSelect(ditherLabels, func(v string) {
		switch v {
		case "Bayer":
			s.cfg.Dithering = DitheringBayer
			levelsForm.Hide()
		case "Floyd-Steinberg":
			s.cfg.Dithering = DitheringFloydSteinberg
			levelsForm.Show()
		case "Atkinson":
			s.cfg.Dithering = DitheringAtkinson
			levelsForm.Show()
		default:
			s.cfg.Dithering = DitheringNone
			levelsForm.Hide()
		}
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

func buildSettingsPanel(s *appState) (fyne.CanvasObject, func()) {
	paTab := buildPixelArtTab(s)
	gbTab, gbRefresh := buildGBTab(s)

	tabs := container.NewAppTabs(
		container.NewTabItem("Pixel Art", paTab),
		container.NewTabItem("GB / GBC", gbTab),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	if s.cropWidget != nil {
		s.cropWidget.FixAspect = false
		s.cropWidget.SnapFunc = s.snapPixelArt
	}
	tabs.OnChanged = func(tab *container.TabItem) {
		s.activeTab = tab.Text
		if s.cropWidget == nil {
			return
		}
		if tab.Text == "GB / GBC" {
			s.cropWidget.FixAspect = true
			s.cropWidget.SnapFunc = s.snapGB
		} else {
			s.cropWidget.FixAspect = false
			s.cropWidget.SnapFunc = s.snapPixelArt
		}
	}

	refresh := func() {
		gbRefresh()
		s.refreshCropInfo()
	}
	return tabs, refresh
}

func buildPixelArtTab(s *appState) fyne.CanvasObject {

	radiusVal := widget.NewLabel(fmt.Sprintf("%d", s.cfg.BilateralRadius))
	radiusSlider := widget.NewSlider(0, 32)
	radiusSlider.Step = 1
	radiusSlider.SetValue(float64(s.cfg.BilateralRadius))
	radiusSlider.OnChanged = func(v float64) {
		s.cfg.BilateralRadius = int(v)
		radiusVal.SetText(fmt.Sprintf("%d", int(v)))
	}

	sigmaVal := widget.NewLabel(fmt.Sprintf("%.0f", s.cfg.BilateralSigma))
	sigmaSlider := widget.NewSlider(1, 150)
	sigmaSlider.Step = 1
	sigmaSlider.SetValue(s.cfg.BilateralSigma)
	sigmaSlider.OnChanged = func(v float64) {
		s.cfg.BilateralSigma = v
		sigmaVal.SetText(fmt.Sprintf("%.0f", v))
	}

	applyBilBtn := widget.NewButton("Apply Bilateral", func() { s.doApplyBilateral() })
	applyBilBtn.Importance = widget.LowImportance

	bilBody := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Radius", sliderRow(radiusSlider, radiusVal)),
			widget.NewFormItem("Sigma σ", sliderRow(sigmaSlider, sigmaVal)),
		),
		container.NewHBox(applyBilBtn),
	)
	bilSection := toggleSection("Bilateral Filter", s.cfg.BilateralEnabled, func(v bool) {
		s.cfg.BilateralEnabled = v
	}, bilBody)

	scalingSelect := widget.NewSelect(
		[]string{"Median", "Bilinear", "Nearest"},
		func(v string) {
			switch v {
			case "Bilinear":
				s.cfg.Scaling = ScalingBilinear
			case "Nearest":
				s.cfg.Scaling = ScalingNearest
			default:
				s.cfg.Scaling = ScalingMedian
			}
			s.scheduleAutoConvert()
		},
	)
	scalingSelect.SetSelectedIndex(int(s.cfg.Scaling))

	sharpenVal := widget.NewLabel(fmt.Sprintf("%.1f", s.cfg.SharpenAmount))
	sharpenSlider := widget.NewSlider(0, 3)
	sharpenSlider.Step = 0.1
	sharpenSlider.SetValue(s.cfg.SharpenAmount)
	sharpenSlider.OnChanged = func(v float64) {
		s.cfg.SharpenAmount = v
		sharpenVal.SetText(fmt.Sprintf("%.1f", v))
		s.scheduleAutoConvert()
	}
	sharpenBody := widget.NewForm(
		widget.NewFormItem("Amount", sliderRow(sharpenSlider, sharpenVal)),
	)
	sharpenSection := toggleSection("Sharpen", s.cfg.SharpenEnabled, func(v bool) {
		s.cfg.SharpenEnabled = v
		s.scheduleAutoConvert()
	}, sharpenBody)

	postVal := widget.NewLabel(strconv.Itoa(s.cfg.PosterizeLevels))
	postSlider := widget.NewSlider(2, 16)
	postSlider.Step = 1
	postSlider.SetValue(float64(s.cfg.PosterizeLevels))
	postSlider.OnChanged = func(v float64) {
		s.cfg.PosterizeLevels = int(v)
		postVal.SetText(strconv.Itoa(int(v)))
		s.scheduleAutoConvert()
	}
	posterizeBody := widget.NewForm(
		widget.NewFormItem("Levels", sliderRow(postSlider, postVal)),
	)
	posterizeSection := toggleSection("Posterize", s.cfg.PosterizeEnabled, func(v bool) {
		s.cfg.PosterizeEnabled = v
		s.scheduleAutoConvert()
	}, posterizeBody)

	ditherBody := buildDitheringBody(s)
	ditherSection := toggleSection("Dithering", s.cfg.DitheringEnabled, func(v bool) {
		s.cfg.DitheringEnabled = v
		s.scheduleAutoConvert()
	}, ditherBody)

	kmeansVal := widget.NewLabel(fmt.Sprintf("%d", s.cfg.KmeansColors))
	kmeansSlider := widget.NewSlider(2, 64)
	kmeansSlider.Step = 1
	kmeansSlider.SetValue(float64(s.cfg.KmeansColors))
	kmeansSlider.OnChanged = func(v float64) {
		s.cfg.KmeansColors = int(v)
		kmeansVal.SetText(fmt.Sprintf("%d", int(v)))
		s.scheduleAutoConvert()
	}

	kmeansBody := widget.NewForm(
		widget.NewFormItem("Colors", sliderRow(kmeansSlider, kmeansVal)),
	)
	if !s.cfg.GBCFilter {
		kmeansBody.Hide()
	}

	var quantizeCheck *widget.Check
	var tileFilterCheck *widget.Check

	quantizeCheck = widget.NewCheck("K-means quantize", func(v bool) {
		s.cfg.GBCFilter = v
		if v {
			kmeansBody.Show()
			if tileFilterCheck != nil {
				s.cfg.TileficationFilter = false
				tileFilterCheck.SetChecked(false)
			}
		} else {
			kmeansBody.Hide()
		}
		s.scheduleAutoConvert()
	})
	quantizeCheck.SetChecked(s.cfg.GBCFilter)

	tileFilterCheck = widget.NewCheck("Tilefication filter", func(v bool) {
		s.cfg.TileficationFilter = v
		if v && quantizeCheck != nil {
			s.cfg.GBCFilter = false
			quantizeCheck.SetChecked(false)
			kmeansBody.Hide()
		}
		s.scheduleAutoConvert()
	})
	tileFilterCheck.SetChecked(s.cfg.TileficationFilter)

	upscalerSelect := widget.NewSelect(
		[]string{"Nearest", "Scale2x"},
		func(v string) {
			if v == "Scale2x" {
				s.cfg.Upscaler = UpscalerScale2x
			} else {
				s.cfg.Upscaler = UpscalerNearest
			}
			s.scheduleAutoConvert()
		},
	)
	upscalerSelect.SetSelectedIndex(int(s.cfg.Upscaler))

	paCropLabel := widget.NewLabel("Draw on the image to crop")
	s.paCropLabel = paCropLabel

	var rebuildPresets func()

	resetCropBtn := widget.NewButton("Reset Crop", func() {
		s.cfg.CropRect = image.Rectangle{}
		s.cfg.CropEnabled = false
		if s.cropWidget != nil {
			s.cropWidget.CropRect = image.Rectangle{}
			s.cropWidget.Refresh()
		}
		s.refreshCropInfo()
		rebuildPresets()
	})

	paOutputLabel := widget.NewLabel("")
	updatePAOutput := func() {
		w, h := s.cfg.TargetW, s.cfg.TargetH
		sc := s.cfg.OutputScale
		if sc < 1 {
			sc = 1
		}
		paOutputLabel.SetText(fmt.Sprintf("Output: %d×%d px", w*sc, h*sc))
	}

	linkAspect := true
	var linkGuard bool

	sourceAspect := func() float64 {
		if s.cfg.CropEnabled && !s.cfg.CropRect.Empty() {
			dy := s.cfg.CropRect.Dy()
			if dy > 0 {
				return float64(s.cfg.CropRect.Dx()) / float64(dy)
			}
		}
		if s.srcImage != nil {
			b := s.srcImage.Bounds()
			if b.Dy() > 0 {
				return float64(b.Dx()) / float64(b.Dy())
			}
		}
		if s.cfg.TargetH > 0 {
			return float64(s.cfg.TargetW) / float64(s.cfg.TargetH)
		}
		return 1.0
	}

	var targetWEntry *widget.Entry
	var targetHEntry *widget.Entry

	targetWEntry = widget.NewEntry()
	targetWEntry.SetText(strconv.Itoa(s.cfg.TargetW))
	targetWEntry.SetPlaceHolder("160")
	targetWEntry.OnChanged = func(v string) {
		w, err := strconv.Atoi(v)
		if err != nil || w <= 0 {
			return
		}
		if linkAspect && !linkGuard {
			if ar := sourceAspect(); ar > 0 {
				newH := int(math.Round(float64(w) / ar))
				if newH < 1 {
					newH = 1
				}
				s.cfg.TargetW = w
				s.cfg.TargetH = newH
				linkGuard = true
				targetHEntry.SetText(strconv.Itoa(newH))
				linkGuard = false
			} else {
				s.cfg.TargetW = w
			}
		} else {
			s.cfg.TargetW = w
		}
		updatePAOutput()
	}

	targetHEntry = widget.NewEntry()
	targetHEntry.SetText(strconv.Itoa(s.cfg.TargetH))
	targetHEntry.SetPlaceHolder("144")
	targetHEntry.OnChanged = func(v string) {
		h, err := strconv.Atoi(v)
		if err != nil || h <= 0 {
			return
		}
		if linkAspect && !linkGuard {
			if ar := sourceAspect(); ar > 0 {
				newW := int(math.Round(float64(h) * ar))
				if newW < 1 {
					newW = 1
				}
				s.cfg.TargetH = h
				s.cfg.TargetW = newW
				linkGuard = true
				targetWEntry.SetText(strconv.Itoa(newW))
				linkGuard = false
			} else {
				s.cfg.TargetH = h
			}
		} else {
			s.cfg.TargetH = h
		}
		updatePAOutput()
	}

	linkCheck := widget.NewCheck("Link W:H", func(v bool) { linkAspect = v })
	linkCheck.SetChecked(linkAspect)

	paScaleEntry := widget.NewEntry()
	paScaleEntry.SetText(strconv.Itoa(s.cfg.OutputScale))
	paScaleEntry.SetPlaceHolder("1")
	paScaleEntry.OnChanged = func(v string) {
		if sc, err := strconv.Atoi(v); err == nil && sc >= 1 {
			s.cfg.OutputScale = sc
			updatePAOutput()
		}
	}

	updatePAOutput()

	type resOption struct{ w, h int }
	var currentOptions []resOption

	presetsSelect := widget.NewSelect(nil, nil)

	rebuildPresets = func() {
		var srcW, srcH int
		if s.cfg.CropEnabled && !s.cfg.CropRect.Empty() {
			srcW, srcH = s.cfg.CropRect.Dx(), s.cfg.CropRect.Dy()
		} else if s.srcImage != nil {
			b := s.srcImage.Bounds()
			srcW, srcH = b.Dx(), b.Dy()
		}
		if srcW <= 0 || srcH <= 0 {
			currentOptions = nil
			presetsSelect.Options = nil
			presetsSelect.ClearSelected()
			presetsSelect.Refresh()
			return
		}
		aspect := float64(srcW) / float64(srcH)
		wIsSmaller := srcW <= srcH

		currentOptions = nil
		var labels []string
		for v := 48; v <= 192; v += 24 {
			var pw, ph int
			if wIsSmaller {
				pw = v
				ph = int(math.Round(float64(v) / aspect))
			} else {
				ph = v
				pw = int(math.Round(float64(v) * aspect))
			}
			if pw > 0 && ph > 0 {
				currentOptions = append(currentOptions, resOption{pw, ph})
				labels = append(labels, fmt.Sprintf("%d×%d", pw, ph))
			}
		}

		presetsSelect.Options = labels
		presetsSelect.OnChanged = func(sel string) {
			for _, opt := range currentOptions {
				if fmt.Sprintf("%d×%d", opt.w, opt.h) == sel {
					s.cfg.TargetW = opt.w
					s.cfg.TargetH = opt.h
					linkGuard = true
					targetWEntry.SetText(strconv.Itoa(opt.w))
					targetHEntry.SetText(strconv.Itoa(opt.h))
					linkGuard = false
					updatePAOutput()
					break
				}
			}
		}
		presetsSelect.ClearSelected()
		presetsSelect.Refresh()
	}
	s.rebuildPresetsFunc = rebuildPresets

	panel := container.NewVBox(
		secLabel("Crop  (auto-snap to target ratio)"),
		paCropLabel,
		container.NewHBox(resetCropBtn),

		widget.NewSeparator(),
		secLabel("Target Resolution"),
		widget.NewForm(
			widget.NewFormItem("W", targetWEntry),
			widget.NewFormItem("H", targetHEntry),
			widget.NewFormItem("", linkCheck),
			widget.NewFormItem("Output ×", paScaleEntry),
		),
		paOutputLabel,
		presetsSelect,

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
		secLabel("Color"),
		quantizeCheck,
		kmeansBody,
		tileFilterCheck,

		widget.NewSeparator(),
		widget.NewForm(widget.NewFormItem("Upscaler", upscalerSelect)),
	)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(220, 100))
	return container.NewBorder(nil, nil, nil, nil, scroll)
}

func buildGBTab(s *appState) (fyne.CanvasObject, func()) {

	s.cfg.Mode = ModeCGB
	s.cfg.HiColor = true

	gbCropLabel := widget.NewLabel("Draw on the image to crop")
	s.gbCropLabel = gbCropLabel

	resetCropBtn := widget.NewButton("Reset Crop", func() {
		s.cfg.CropRect = image.Rectangle{}
		s.cfg.CropEnabled = false
		if s.cropWidget != nil {
			s.cropWidget.CropRect = image.Rectangle{}
			s.cropWidget.Refresh()
		}
		s.refreshCropInfo()
		s.scheduleAutoConvert()
	})

	nameEntry := widget.NewEntry()
	nameEntry.SetText(s.cfg.Name)
	nameEntry.SetPlaceHolder("project")
	nameEntry.OnChanged = func(v string) { s.cfg.Name = sanitizeName(v) }

	gbdkEntry := widget.NewEntry()
	gbdkEntry.SetText(s.cfg.GBDKHome)
	gbdkEntry.SetPlaceHolder(`C:\Bin\gbdk`)
	gbdkEntry.OnChanged = func(v string) { s.cfg.GBDKHome = v }

	outDirEntry := widget.NewEntry()
	outDirEntry.SetText(s.cfg.OutputDir)
	outDirEntry.SetPlaceHolder("out")
	outDirEntry.OnChanged = func(v string) { s.cfg.OutputDir = v }

	panel := container.NewVBox(
		secLabel("Crop  (auto-snap 10:9)"),
		gbCropLabel,
		container.NewHBox(resetCropBtn),

		widget.NewSeparator(),
		secLabel("Output"),
		widget.NewForm(
			widget.NewFormItem("Name", nameEntry),
			widget.NewFormItem("GBDK Home", gbdkEntry),
			widget.NewFormItem("Output Dir", outDirEntry),
		),
	)

	compileBtn := widget.NewButton("Compile ROM", func() { s.doCompile() })
	compileBtn.Importance = widget.HighImportance
	openDirBtn := widget.NewButton("Open output folder", func() { openOutputFolder(s.cfg.OutputDir) })

	s.galleryLabel = widget.NewLabel("Gallery: 0 image(s)")
	addGalBtn := widget.NewButton("Add current to gallery", func() { s.doAddToGallery() })
	clearGalBtn := widget.NewButton("Clear", func() { s.doClearGallery() })
	galCompileBtn := widget.NewButton("Compile Gallery ROM", func() { s.doCompileGallery() })
	galCompileBtn.Importance = widget.HighImportance

	actionBox := container.NewVBox(
		compileBtn,
		openDirBtn,
		widget.NewSeparator(),
		secLabel("Image Gallery  (◀▶ switch · Start = print)"),
		s.galleryLabel,
		container.NewHBox(addGalBtn, clearGalBtn),
		galCompileBtn,
	)

	scroll := container.NewVScroll(panel)
	scroll.SetMinSize(fyne.NewSize(220, 100))

	refresh := func() {
		nameEntry.SetText(s.cfg.Name)
		gbdkEntry.SetText(s.cfg.GBDKHome)
		outDirEntry.SetText(s.cfg.OutputDir)
		s.refreshCropInfo()
	}
	return container.NewBorder(nil, actionBox, nil, nil, scroll), refresh
}

func secLabel(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func sliderRow(sl *widget.Slider, val *widget.Label) *fyne.Container {
	val.Alignment = fyne.TextAlignTrailing
	return container.NewBorder(nil, nil, nil, val, sl)
}
