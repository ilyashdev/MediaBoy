package ui

import (
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"MediaBoy/internal/core"
)

const (
	dmNew  = 0
	dmMove = 1
	dmNW   = 2
	dmN    = 3
	dmNE   = 4
	dmE    = 5
	dmSE   = 6
	dmS    = 7
	dmSW   = 8
	dmW    = 9
)

const handleRadius float32 = 6

type CropWidget struct {
	widget.BaseWidget

	SrcImage  image.Image
	CropRect  image.Rectangle
	FixAspect bool

	// ImgSize, when set, is the size of the coordinate space CropRect is in;
	// SrcImage is then a scaled-down preview of that image.
	ImgSize image.Point

	// Placeholder is shown while no image is loaded.
	Placeholder string

	SnapFunc func(drawn image.Rectangle, imgBounds image.Rectangle) image.Rectangle

	OnChanged func(image.Rectangle)

	dragging  bool
	dragMode  int
	dragStart fyne.Position
	dragInit  image.Rectangle

	dispX, dispY float32
	dispW, dispH float32
	wW, wH       float32
}

var _ desktop.Mouseable = (*CropWidget)(nil)
var _ fyne.Draggable = (*CropWidget)(nil)

func NewCropWidget(onChange func(image.Rectangle)) *CropWidget {
	w := &CropWidget{OnChanged: onChange, Placeholder: "Open a file to begin"}
	w.ExtendBaseWidget(w)
	return w
}

func (w *CropWidget) SetImage(img image.Image) {
	w.SrcImage = img
	w.CropRect = image.Rectangle{}
	w.Refresh()
}

// imgBounds is the rectangle CropRect is expressed in.
func (w *CropWidget) imgBounds() image.Rectangle {
	if w.ImgSize != (image.Point{}) {
		return image.Rectangle{Max: w.ImgSize}
	}
	return w.SrcImage.Bounds()
}

func (w *CropWidget) updateDispBounds(size fyne.Size) {
	w.wW = size.Width
	w.wH = size.Height
	if w.SrcImage == nil {
		return
	}
	b := w.imgBounds()
	iw := float32(b.Dx())
	ih := float32(b.Dy())
	if iw == 0 || ih == 0 {
		return
	}
	imgAspect := iw / ih
	wAspect := size.Width / size.Height
	if imgAspect > wAspect {
		w.dispW = size.Width
		w.dispH = size.Width / imgAspect
		w.dispX = 0
		w.dispY = (size.Height - w.dispH) / 2
	} else {
		w.dispH = size.Height
		w.dispW = size.Height * imgAspect
		w.dispX = (size.Width - w.dispW) / 2
		w.dispY = 0
	}
}

func (w *CropWidget) imgToCanvas(ix, iy int) (float32, float32) {
	if w.SrcImage == nil || w.dispW == 0 || w.dispH == 0 {
		return 0, 0
	}
	b := w.imgBounds()
	fx := w.dispX + float32(ix-b.Min.X)/float32(b.Dx())*w.dispW
	fy := w.dispY + float32(iy-b.Min.Y)/float32(b.Dy())*w.dispH
	return fx, fy
}

func (w *CropWidget) canvasToImg(cx, cy float32) (int, int) {
	if w.SrcImage == nil || w.dispW == 0 || w.dispH == 0 {
		return 0, 0
	}
	b := w.imgBounds()
	ix := b.Min.X + int((cx-w.dispX)/w.dispW*float32(b.Dx()))
	iy := b.Min.Y + int((cy-w.dispY)/w.dispH*float32(b.Dy()))
	return clampInt(ix, b.Min.X, b.Max.X), clampInt(iy, b.Min.Y, b.Max.Y)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func (w *CropWidget) hitTest(p fyne.Position) int {
	if w.CropRect.Empty() {
		return dmNew
	}
	x0, y0 := w.imgToCanvas(w.CropRect.Min.X, w.CropRect.Min.Y)
	x1, y1 := w.imgToCanvas(w.CropRect.Max.X, w.CropRect.Max.Y)
	xm, ym := (x0+x1)/2, (y0+y1)/2

	type hp struct {
		hx, hy float32
		mode   int
	}
	handles := []hp{
		{x0, y0, dmNW}, {xm, y0, dmN}, {x1, y0, dmNE},
		{x1, ym, dmE}, {x1, y1, dmSE}, {xm, y1, dmS},
		{x0, y1, dmSW}, {x0, ym, dmW},
	}
	for _, h := range handles {
		if abs32(p.X-h.hx) <= handleRadius+2 && abs32(p.Y-h.hy) <= handleRadius+2 {
			return h.mode
		}
	}
	if p.X >= x0 && p.X <= x1 && p.Y >= y0 && p.Y <= y1 {
		return dmMove
	}
	return dmNew
}

func (w *CropWidget) MouseDown(e *desktop.MouseEvent) {
	w.dragging = true
	w.dragStart = e.Position
	w.dragInit = w.CropRect
	w.dragMode = w.hitTest(e.Position)
}

func (w *CropWidget) MouseUp(*desktop.MouseEvent) {
	w.dragging = false
}

func (w *CropWidget) Dragged(e *fyne.DragEvent) {
	if !w.dragging || w.SrcImage == nil {
		return
	}
	cur := e.Position
	ix, iy := w.canvasToImg(cur.X, cur.Y)
	sx, sy := w.canvasToImg(w.dragStart.X, w.dragStart.Y)
	dx := ix - sx
	dy := iy - sy
	b := w.imgBounds()

	switch w.dragMode {
	case dmNew:
		startX, startY := w.canvasToImg(w.dragStart.X, w.dragStart.Y)
		r := image.Rect(startX, startY, ix, iy).Canon()
		if w.FixAspect {
			r = enforceGB169(r)
		}
		w.CropRect = r.Intersect(b)

	case dmMove:
		r := w.dragInit.Add(image.Pt(dx, dy))
		r = clampRect(r, b)
		w.CropRect = r

	case dmNW:
		r := image.Rect(w.dragInit.Min.X+dx, w.dragInit.Min.Y+dy, w.dragInit.Max.X, w.dragInit.Max.Y).Canon()
		w.CropRect = r.Intersect(b)
	case dmNE:
		r := image.Rect(w.dragInit.Min.X, w.dragInit.Min.Y+dy, w.dragInit.Max.X+dx, w.dragInit.Max.Y).Canon()
		w.CropRect = r.Intersect(b)
	case dmSE:
		r := image.Rect(w.dragInit.Min.X, w.dragInit.Min.Y, w.dragInit.Max.X+dx, w.dragInit.Max.Y+dy).Canon()
		w.CropRect = r.Intersect(b)
	case dmSW:
		r := image.Rect(w.dragInit.Min.X+dx, w.dragInit.Min.Y, w.dragInit.Max.X, w.dragInit.Max.Y+dy).Canon()
		w.CropRect = r.Intersect(b)
	case dmN:
		r := image.Rect(w.dragInit.Min.X, w.dragInit.Min.Y+dy, w.dragInit.Max.X, w.dragInit.Max.Y).Canon()
		w.CropRect = r.Intersect(b)
	case dmS:
		r := image.Rect(w.dragInit.Min.X, w.dragInit.Min.Y, w.dragInit.Max.X, w.dragInit.Max.Y+dy).Canon()
		w.CropRect = r.Intersect(b)
	case dmE:
		r := image.Rect(w.dragInit.Min.X, w.dragInit.Min.Y, w.dragInit.Max.X+dx, w.dragInit.Max.Y).Canon()
		w.CropRect = r.Intersect(b)
	case dmW:
		r := image.Rect(w.dragInit.Min.X+dx, w.dragInit.Min.Y, w.dragInit.Max.X, w.dragInit.Max.Y).Canon()
		w.CropRect = r.Intersect(b)
	}

	w.Refresh()
}

func (w *CropWidget) DragEnd() {
	w.dragging = false
	if !w.CropRect.Empty() && w.SrcImage != nil && w.SnapFunc != nil {
		snapped := w.SnapFunc(w.CropRect, w.imgBounds())
		if !snapped.Empty() {
			w.CropRect = snapped
			w.Refresh()
		}
	}
	if w.OnChanged != nil && !w.CropRect.Empty() {
		w.OnChanged(w.CropRect)
	}
}

func enforceGB169(r image.Rectangle) image.Rectangle {
	ww := r.Dx()
	if ww < 8 {
		ww = 8
	}
	h := ww * core.ScreenH / core.ScreenW
	if h < 8 {
		h = 8
	}
	return image.Rect(r.Min.X, r.Min.Y, r.Min.X+ww, r.Min.Y+h)
}

func clampRect(r, bounds image.Rectangle) image.Rectangle {
	dx, dy := 0, 0
	if r.Min.X < bounds.Min.X {
		dx = bounds.Min.X - r.Min.X
	}
	if r.Min.Y < bounds.Min.Y {
		dy = bounds.Min.Y - r.Min.Y
	}
	if r.Max.X > bounds.Max.X {
		dx = bounds.Max.X - r.Max.X
	}
	if r.Max.Y > bounds.Max.Y {
		dy = bounds.Max.Y - r.Max.Y
	}
	return r.Add(image.Pt(dx, dy))
}

func (w *CropWidget) MinSize() fyne.Size {
	return fyne.NewSize(320, 240)
}

func (w *CropWidget) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewImageFromImage(nil)
	bg.FillMode = canvas.ImageFillContain

	dim := make([]*canvas.Rectangle, 4)
	for i := range dim {
		dim[i] = canvas.NewRectangle(color.NRGBA{0, 0, 0, 130})
	}
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = colPrimary
	border.StrokeWidth = 2

	handles := make([]*canvas.Rectangle, 8)
	for i := range handles {
		handles[i] = canvas.NewRectangle(colPrimary)
	}

	label := canvas.NewText(w.Placeholder, colMuted)
	label.Alignment = fyne.TextAlignCenter
	label.TextSize = 14

	objects := []fyne.CanvasObject{bg}
	for _, d := range dim {
		objects = append(objects, d)
	}
	objects = append(objects, border)
	for _, h := range handles {
		objects = append(objects, h)
	}
	objects = append(objects, label)

	return &cropRenderer{
		w:       w,
		bg:      bg,
		dim:     dim,
		border:  border,
		handles: handles,
		label:   label,
		objects: objects,
	}
}

type cropRenderer struct {
	w       *CropWidget
	bg      *canvas.Image
	dim     []*canvas.Rectangle
	border  *canvas.Rectangle
	handles []*canvas.Rectangle
	label   *canvas.Text
	objects []fyne.CanvasObject
}

func (r *cropRenderer) Layout(size fyne.Size) {
	r.w.updateDispBounds(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.bg.Resize(size)
	r.label.Move(fyne.NewPos(0, size.Height/2-8))
	r.label.Resize(fyne.NewSize(size.Width, 20))
	r.layoutOverlay()
}

func (r *cropRenderer) layoutOverlay() {
	w := r.w
	cr := w.CropRect

	if cr.Empty() || w.SrcImage == nil {
		for _, d := range r.dim {
			d.Hide()
		}
		r.border.Hide()
		for _, h := range r.handles {
			h.Hide()
		}
		r.label.Show()
		return
	}
	r.label.Hide()

	x0, y0 := w.imgToCanvas(cr.Min.X, cr.Min.Y)
	x1, y1 := w.imgToCanvas(cr.Max.X, cr.Max.Y)

	r.dim[0].Move(fyne.NewPos(0, 0))
	r.dim[0].Resize(fyne.NewSize(w.wW, y0))
	r.dim[1].Move(fyne.NewPos(0, y1))
	r.dim[1].Resize(fyne.NewSize(w.wW, w.wH-y1))
	r.dim[2].Move(fyne.NewPos(0, y0))
	r.dim[2].Resize(fyne.NewSize(x0, y1-y0))
	r.dim[3].Move(fyne.NewPos(x1, y0))
	r.dim[3].Resize(fyne.NewSize(w.wW-x1, y1-y0))
	for _, d := range r.dim {
		d.Show()
	}

	r.border.Move(fyne.NewPos(x0, y0))
	r.border.Resize(fyne.NewSize(x1-x0, y1-y0))
	r.border.Show()

	xm, ym := (x0+x1)/2, (y0+y1)/2
	hpts := [][2]float32{
		{x0, y0}, {xm, y0}, {x1, y0},
		{x1, ym}, {x1, y1}, {xm, y1},
		{x0, y1}, {x0, ym},
	}
	hs := handleRadius * 2
	for i, hp := range hpts {
		r.handles[i].Move(fyne.NewPos(hp[0]-handleRadius, hp[1]-handleRadius))
		r.handles[i].Resize(fyne.NewSize(hs, hs))
		r.handles[i].Show()
	}
}

func (r *cropRenderer) MinSize() fyne.Size { return fyne.NewSize(320, 240) }

func (r *cropRenderer) Refresh() {
	r.label.Text = r.w.Placeholder
	if r.w.SrcImage != nil {
		r.bg.Image = r.w.SrcImage
		r.label.Hide()
	} else {
		r.label.Show()
	}
	r.bg.Refresh()
	r.layoutOverlay()
	canvas.Refresh(r.w)
}

func (r *cropRenderer) Destroy() {}

func (r *cropRenderer) Objects() []fyne.CanvasObject { return r.objects }
