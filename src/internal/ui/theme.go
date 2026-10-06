package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Palette: dark green-black backdrop with translucent green tiles on top.
var (
	colBackground = color.NRGBA{0x0A, 0x14, 0x0F, 0xFF}
	colBackdropLo = color.NRGBA{0x06, 0x0D, 0x09, 0xFF}
	colOverlay    = color.NRGBA{0x10, 0x20, 0x18, 0xFF}
	colButton     = color.NRGBA{0x1A, 0x33, 0x26, 0xFF}
	colButtonOff  = color.NRGBA{0x13, 0x22, 0x1A, 0xFF}
	colInput      = color.NRGBA{0x0D, 0x1B, 0x14, 0xFF}
	colInputEdge  = color.NRGBA{0x2A, 0x4C, 0x39, 0xFF}
	colPrimary    = color.NRGBA{0x5F, 0xD0, 0x68, 0xFF}
	colOnPrimary  = color.NRGBA{0x06, 0x12, 0x0A, 0xFF}
	colForeground = color.NRGBA{0xE2, 0xF3, 0xE6, 0xFF}
	colMuted      = color.NRGBA{0x80, 0xA6, 0x8E, 0xFF}
	colDisabled   = color.NRGBA{0x5A, 0x76, 0x64, 0xFF}
	colSeparator  = color.NRGBA{0x1F, 0x3B, 0x2C, 0xFF}

	colTileTop    = color.NRGBA{0x5F, 0xD0, 0x68, 0x30}
	colTileBottom = color.NRGBA{0x2E, 0x8B, 0x57, 0x0C}
	colTileEdge   = color.NRGBA{0x7C, 0xFF, 0x9A, 0x30}
)

type mbTheme struct{}

var _ fyne.Theme = mbTheme{}

func (mbTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
		return colOverlay
	case theme.ColorNameHeaderBackground:
		return colButtonOff
	case theme.ColorNameButton:
		return colButton
	case theme.ColorNameDisabledButton:
		return colButtonOff
	case theme.ColorNameInputBackground:
		return colInput
	case theme.ColorNameInputBorder:
		return colInputEdge
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return colPrimary
	case theme.ColorNameForegroundOnPrimary:
		return colOnPrimary
	case theme.ColorNameFocus:
		return color.NRGBA{0x5F, 0xD0, 0x68, 0x80}
	case theme.ColorNameSelection:
		return color.NRGBA{0x5F, 0xD0, 0x68, 0x40}
	case theme.ColorNameHover:
		return color.NRGBA{0x7C, 0xFF, 0x9A, 0x1A}
	case theme.ColorNamePressed:
		return color.NRGBA{0x7C, 0xFF, 0x9A, 0x30}
	case theme.ColorNameForeground:
		return colForeground
	case theme.ColorNamePlaceHolder:
		return colMuted
	case theme.ColorNameDisabled:
		return colDisabled
	case theme.ColorNameSeparator:
		return colSeparator
	case theme.ColorNameScrollBar:
		return color.NRGBA{0x7C, 0xFF, 0x9A, 0x40}
	case theme.ColorNameShadow:
		return color.NRGBA{0x00, 0x00, 0x00, 0x70}
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (mbTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (mbTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (mbTheme) Size(n fyne.ThemeSizeName) float32       { return theme.DefaultTheme().Size(n) }

// withBackdrop puts a subtle dark-green vertical gradient behind the whole window.
func withBackdrop(content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewStack(canvas.NewVerticalGradient(colBackground, colBackdropLo), content)
}

// newTile wraps content in a translucent green-gradient tile.
func newTile(content fyne.CanvasObject) *fyne.Container {
	bg := canvas.NewVerticalGradient(colTileTop, colTileBottom)
	edge := canvas.NewRectangle(color.Transparent)
	edge.StrokeColor = colTileEdge
	edge.StrokeWidth = 1
	return container.NewStack(bg, edge, container.NewPadded(content))
}

// section is a collapsible settings tile. Its expanded state is remembered
// across launches under prefKey. An optional enable switch in the header hides
// the body while the feature is off.
type section struct {
	tile     *fyne.Container
	header   *widget.Button
	switchAt *fyne.Container
	body     fyne.CanvasObject
	title    string
	expanded bool
	enabled  bool
	prefs    fyne.Preferences
	prefKey  string
}

func newSection(title string, body fyne.CanvasObject) *section {
	sec := &section{title: title, body: body, expanded: true, enabled: true}
	if app := fyne.CurrentApp(); app != nil {
		sec.prefs = app.Preferences()
		sec.prefKey = "section." + title
		sec.expanded = sec.prefs.BoolWithFallback(sec.prefKey, true)
	}
	sec.header = widget.NewButton(title, sec.toggle)
	sec.header.Alignment = widget.ButtonAlignLeading
	sec.header.Importance = widget.LowImportance
	sec.switchAt = container.NewStack()
	head := container.NewBorder(nil, nil, nil, sec.switchAt, sec.header)
	sec.tile = newTile(container.NewVBox(head, body))
	sec.apply()
	return sec
}

// withSwitch adds an on/off check to the header; onChange fires on user toggles.
func (sec *section) withSwitch(enabled bool, onChange func(bool)) *section {
	sec.enabled = enabled
	check := widget.NewCheck("", nil)
	check.SetChecked(enabled)
	check.OnChanged = func(v bool) {
		sec.enabled = v
		sec.apply()
		onChange(v)
	}
	sec.switchAt.Add(check)
	sec.apply()
	return sec
}

func (sec *section) toggle() {
	sec.expanded = !sec.expanded
	if sec.prefs != nil {
		sec.prefs.SetBool(sec.prefKey, sec.expanded)
	}
	sec.apply()
}

func (sec *section) apply() {
	if sec.expanded {
		sec.header.SetIcon(theme.MenuDropDownIcon())
	} else {
		sec.header.SetIcon(theme.MenuExpandIcon())
	}
	if sec.expanded && sec.enabled {
		sec.body.Show()
	} else {
		sec.body.Hide()
	}
	sec.tile.Refresh()
}

// panelColumn stacks tiles in a scrollable settings column.
func panelColumn(objs ...fyne.CanvasObject) *container.Scroll {
	scroll := container.NewVScroll(container.NewVBox(objs...))
	scroll.SetMinSize(fyne.NewSize(260, 100))
	return scroll
}

func secLabel(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func hintLabel(text string) *widget.Label {
	l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	l.Wrapping = fyne.TextWrapWord
	return l
}

// sliderRow keeps the value readout at a fixed width so the slider doesn't
// jump around as the number of digits changes.
func sliderRow(sl *widget.Slider, val *widget.Label) *fyne.Container {
	val.Alignment = fyne.TextAlignTrailing
	return container.NewBorder(nil, nil, nil, fixedWidth(48, val), sl)
}

func fixedWidth(w float32, obj fyne.CanvasObject) *fyne.Container {
	return container.New(&fixedWidthLayout{w: w}, obj)
}

type fixedWidthLayout struct{ w float32 }

func (l *fixedWidthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *fixedWidthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if o.Visible() {
			h = fyne.Max(h, o.MinSize().Height)
		}
	}
	return fyne.NewSize(l.w, h)
}

var _ fyne.Layout = (*fixedWidthLayout)(nil)
