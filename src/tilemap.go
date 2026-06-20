package main

import (
	"image"
	"image/color"
	"math"
	"sort"
)

func tilefication(inp image.Image) ([][]Tile, [8]Palette) {
	b := inp.Bounds()
	tileX := b.Dx() / 8
	tileY := b.Dy() / 8
	if tileX == 0 || tileY == 0 {
		return nil, [8]Palette{}
	}
	tilePixels := make([][][]RGB, tileY)
	for ty := 0; ty < tileY; ty++ {
		tilePixels[ty] = make([][]RGB, tileX)
		for tx := 0; tx < tileX; tx++ {
			pixels := make([]RGB, 0, 64)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					c := toRGBFull(inp.At(b.Min.X+tx*8+x, b.Min.Y+ty*8+y))
					pixels = append(pixels, c)
				}
			}
			tilePixels[ty][tx] = pixels
		}
	}
	tileMeans := make([]RGB, tileY*tileX)
	for ty := 0; ty < tileY; ty++ {
		for tx := 0; tx < tileX; tx++ {
			var r, g, bl float64
			for _, p := range tilePixels[ty][tx] {
				r += p.R
				g += p.G
				bl += p.B
			}
			n := float64(len(tilePixels[ty][tx]))
			tileMeans[ty*tileX+tx] = RGB{r / n, g / n, bl / n}
		}
	}

	groupCenters := kmeans(tileMeans, 8, 20)

	tileAssign := make([][]int, tileY)
	for ty := range tileAssign {
		tileAssign[ty] = make([]int, tileX)
		for tx := 0; tx < tileX; tx++ {
			tileAssign[ty][tx] = nearestIdx(tileMeans[ty*tileX+tx], groupCenters)
		}
	}

	var palettes [8]Palette
	for pi := range palettes {
		var bucket []RGB
		for ty := 0; ty < tileY; ty++ {
			for tx := 0; tx < tileX; tx++ {
				if tileAssign[ty][tx] == pi {
					bucket = append(bucket, tilePixels[ty][tx]...)
				}
			}
		}
		var colors []RGB
		switch {
		case len(bucket) == 0:
			colors = []RGB{{0, 0, 0}, {85, 85, 85}, {170, 170, 170}, {255, 255, 255}}
		case len(bucket) <= 4:
			colors = make([]RGB, 4)
			for j := 0; j < 4; j++ {
				colors[j] = bucket[j%len(bucket)]
			}
		default:
			colors = kmeans(bucket, 4, 20)
		}
		copy(palettes[pi].Colors[:], colors[:4])
	}
	for iter := 0; iter < 10; iter++ {
		for ty := 0; ty < tileY; ty++ {
			for tx := 0; tx < tileX; tx++ {
				bestPal, bestErr := 0, math.MaxFloat64
				for pi, pal := range palettes {
					var err float64
					for _, px := range tilePixels[ty][tx] {
						best := math.MaxFloat64
						for _, pc := range pal.Colors {
							if d := colorDist(px, pc); d < best {
								best = d
							}
						}
						err += best
					}
					if err < bestErr {
						bestErr = err
						bestPal = pi
					}
				}
				tileAssign[ty][tx] = bestPal
			}
		}
		for pi := range palettes {
			var bucket []RGB
			for ty := 0; ty < tileY; ty++ {
				for tx := 0; tx < tileX; tx++ {
					if tileAssign[ty][tx] == pi {
						bucket = append(bucket, tilePixels[ty][tx]...)
					}
				}
			}
			if len(bucket) == 0 {
				continue
			}
			colors := kmeans(bucket, 4, 10)
			copy(palettes[pi].Colors[:], colors[:4])
		}
	}

	for pi := range palettes {
		for ci := range palettes[pi].Colors {
			palettes[pi].Colors[ci] = roundRGB555(palettes[pi].Colors[ci])
		}
	}

	tiles := make([][]Tile, tileY)
	for ty := 0; ty < tileY; ty++ {
		tiles[ty] = make([]Tile, tileX)
		for tx := 0; tx < tileX; tx++ {
			var t Tile
			t.Palette = uint8(tileAssign[ty][tx])
			pal := palettes[t.Palette]
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					px := tilePixels[ty][tx][y*8+x]
					best, bestDist := 0, math.MaxFloat64
					for ci, pc := range pal.Colors {
						if d := colorDist(px, pc); d < bestDist {
							bestDist = d
							best = ci
						}
					}
					t.Pixels[y][x] = uint8(best)
				}
			}
			tiles[ty][tx] = t
		}
	}

	return tiles, palettes
}

func dedupTilesForPrint(tiles [][]Tile) (uniq []Tile, tmap []uint8, w, h int) {
	h = len(tiles)
	if h > 0 {
		w = len(tiles[0])
	}
	idxOf := map[Tile]int{}
	tmap = make([]uint8, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := tiles[y][x]
			id, ok := idxOf[t]
			if !ok {
				if len(uniq) < 256 {
					id = len(uniq)
					idxOf[t] = id
					uniq = append(uniq, t)
				} else {
					id = nearestTileIdx(t, uniq)
				}
			}
			tmap = append(tmap, uint8(id))
		}
	}
	return uniq, tmap, w, h
}

func nearestTileIdx(t Tile, pool []Tile) int {
	best, bestD := 0, 1<<30
	for i := range pool {
		d := 0
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				dd := int(t.Pixels[y][x]) - int(pool[i].Pixels[y][x])
				if dd < 0 {
					dd = -dd
				}
				d += dd
			}
		}
		if d < bestD {
			bestD = d
			best = i
		}
	}
	return best
}

func tileficationDMG(inp image.Image) ([][]Tile, [8]Palette) {
	gray := toGrayscale(inp)
	b := gray.Bounds()
	tileX := b.Dx() / 8
	tileY := b.Dy() / 8

	allPixels := make([]RGB, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			allPixels = append(allPixels, toRGBFull(gray.At(x, y)))
		}
	}

	centers := kmeans(allPixels, 4, 30)

	sort.Slice(centers, func(i, j int) bool {
		return centers[i].R > centers[j].R
	})

	for i := range centers {
		centers[i] = roundRGB555(centers[i])
	}

	var palettes [8]Palette
	copy(palettes[0].Colors[:], centers)
	for i := 1; i < 8; i++ {
		palettes[i] = palettes[0]
	}

	tiles := make([][]Tile, tileY)
	for ty := 0; ty < tileY; ty++ {
		tiles[ty] = make([]Tile, tileX)
		for tx := 0; tx < tileX; tx++ {
			var t Tile
			t.Palette = 0
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					px := toRGBFull(gray.At(b.Min.X+tx*8+x, b.Min.Y+ty*8+y))
					t.Pixels[y][x] = uint8(nearestIdx(px, centers))
				}
			}
			tiles[ty][tx] = t
		}
	}

	return tiles, palettes
}

func quantizeKmeans(inp image.Image, numColors int) image.Image {
	if numColors < 2 {
		numColors = 2
	}
	b := inp.Bounds()
	pixels := make([]RGB, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			pixels = append(pixels, toRGBFull(inp.At(x, y)))
		}
	}
	centers := kmeans(pixels, numColors, 20)

	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px := toRGBFull(inp.At(x, y))
			c := centers[nearestIdx(px, centers)]
			out.Set(x-b.Min.X, y-b.Min.Y, color.RGBA{
				R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: 255,
			})
		}
	}
	return out
}

func tilesToImage(tiles [][]Tile, palettes [8]Palette) image.Image {
	tileY := len(tiles)
	if tileY == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	tileX := len(tiles[0])

	out := image.NewRGBA(image.Rect(0, 0, tileX*8, tileY*8))

	for ty := 0; ty < tileY; ty++ {
		for tx := 0; tx < tileX; tx++ {
			t := tiles[ty][tx]
			pal := palettes[t.Palette]

			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					c := pal.Colors[t.Pixels[y][x]]
					out.Set(tx*8+x, ty*8+y, color.RGBA{
						R: uint8(c.R),
						G: uint8(c.G),
						B: uint8(c.B),
						A: 255,
					})
				}
			}
		}
	}

	return out
}

func tileficationVideo(inp []image.Image, cfg ConvertConfig, origStride int) *VideoData {
	count := len(inp)
	if count == 0 {
		return &VideoData{}
	}
	if origStride < 1 {
		origStride = 1
	}

	b := inp[0].Bounds()
	tileX := b.Dx() / 8
	tileY := b.Dy() / 8
	if tileX == 0 || tileY == 0 {
		return &VideoData{}
	}
	size := tileX * tileY

	framePixels := make([][][]RGB, count)
	frameMeans := make([][]RGB, count)
	for i := range inp {
		bb := inp[i].Bounds()
		px := make([][]RGB, size)
		means := make([]RGB, size)
		for ty := 0; ty < tileY; ty++ {
			for tx := 0; tx < tileX; tx++ {
				idx := ty*tileX + tx
				pix := make([]RGB, 0, 64)
				var r, g, bl float64
				for y := 0; y < 8; y++ {
					for x := 0; x < 8; x++ {
						c := toRGBFull(inp[i].At(bb.Min.X+tx*8+x, bb.Min.Y+ty*8+y))
						pix = append(pix, c)
						r += c.R
						g += c.G
						bl += c.B
					}
				}
				px[idx] = pix
				means[idx] = RGB{r / 64, g / 64, bl / 64}
			}
		}
		framePixels[i] = px
		frameMeans[i] = means
	}

	origCount := 0
	for i := 0; i < count; i += origStride {
		origCount++
	}

	var scenes [][]int
	if cfg.Mode == ModeDMG || origStride > 1 {

		all := make([]int, origCount)
		for i := range all {
			all[i] = i
		}
		scenes = [][]int{all}
	} else {
		origSigs := make([][]RGB, origCount)
		for oi := 0; oi < origCount; oi++ {
			fi := oi * origStride
			if fi >= count {
				fi = count - 1
			}
			origSigs[oi] = kmeans(frameMeans[fi], 8, 8)
		}
		start := 0
		sceneSig := origSigs[0]
		for oi := 1; oi < origCount; oi++ {
			if paletteSetDist(origSigs[oi], sceneSig) > cfg.SceneThreshold {
				scenes = append(scenes, intRange(start, oi))
				start = oi
				sceneSig = origSigs[oi]
			}
		}
		scenes = append(scenes, intRange(start, origCount))
	}

	origSceneOf := make([]int, origCount)
	for si, oframes := range scenes {
		for _, oi := range oframes {
			origSceneOf[oi] = si
		}
	}

	sceneOf := make([]int, count)
	for i := 0; i < count; i++ {
		oi := i / origStride
		if oi >= origCount {
			oi = origCount - 1
		}
		sceneOf[i] = origSceneOf[oi]
	}

	frameTiles := make([][]Tile, count)
	scenePals := make([][8]Palette, len(scenes))
	for si, oframes := range scenes {

		inpFrames := make([]int, len(oframes))
		for k, oi := range oframes {
			fi := oi * origStride
			if fi >= count {
				fi = count - 1
			}
			inpFrames[k] = fi
		}
		var pals [8]Palette
		if cfg.Mode == ModeDMG {
			pals = sceneGrayscalePalette(framePixels, inpFrames)
		} else {
			pals = buildScenePalettes(framePixels, frameMeans, inpFrames, size)
		}
		scenePals[si] = pals

		for i := 0; i < count; i++ {
			if sceneOf[i] == si {
				frameTiles[i] = tilefyFrameFixed(framePixels[i], size, pals, cfg.Mode)
			}
		}
	}

	const (
		dmgUpdateBudget = 80

		dissolveBudget = 32
	)
	maxSlots := 512
	if cfg.Mode == ModeDMG {
		maxSlots = 256
	}
	reuseTol := cfg.TileReuseTol
	if reuseTol < 0 {
		reuseTol = 0
	}

	tilesClose := func(a, b Tile) bool {
		if a.Palette != b.Palette {
			return false
		}
		diff := 0
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				if a.Pixels[y][x] != b.Pixels[y][x] {
					diff++
					if diff > reuseTol {
						return false
					}
				}
			}
		}
		return true
	}

	video := &VideoData{}

	tileToSlot := map[Tile]uint16{}
	var nextSlot uint16

	freeSlots := make([]uint16, 0, 512)

	screenTile := make([]uint8, size)
	screenAttr := make([]uint8, size)
	cur := make([]Tile, size)
	curSet := make([]bool, size)

	resetVRAM := func() {
		tileToSlot = map[Tile]uint16{}
		nextSlot = 0
		freeSlots = freeSlots[:0]
		for j := range screenTile {
			screenTile[j] = 0xFF
			screenAttr[j] = 0xFF
			curSet[j] = false
		}
	}
	resetVRAM()

	allocSlot := func() (uint16, bool) {
		if n := len(freeSlots); n > 0 {
			s := freeSlots[n-1]
			freeSlots = freeSlots[:n-1]
			return s, true
		}
		if int(nextSlot) < maxSlots {
			s := nextSlot
			nextSlot++
			return s, true
		}
		return 0, false
	}

	compact := func() {
		refSlots := make(map[uint16]struct{}, maxSlots)
		for j := 0; j < size; j++ {
			if !curSet[j] {
				continue
			}
			s := uint16(screenTile[j])
			if screenAttr[j]&0x08 != 0 {
				s += 256
			}
			refSlots[s] = struct{}{}
		}
		for tile, slot := range tileToSlot {
			if _, ok := refSlots[slot]; !ok {
				freeSlots = append(freeSlots, slot)
				delete(tileToSlot, tile)
			}
		}
	}

	writeCell := func(f *FrameData, j int, tile Tile, slot uint16) {
		tileIdx := uint8(slot & 0xFF)
		attr := tile.Palette & 7
		if slot >= 256 {
			attr |= 0x08
		}
		mapOff := (j/tileX)*32 + j%tileX
		f.Updates = append(f.Updates, Update{
			ScreenPos: uint16(mapOff),
			TileIndex: tileIdx,
			Attr:      attr,
		})
		screenTile[j] = tileIdx
		screenAttr[j] = attr
		cur[j] = tile
		curSet[j] = true
	}

	fillKeyframe := func(f *FrameData, tiles []Tile) {
		resetVRAM()
		for j := 0; j < size; j++ {
			t := tiles[j]
			slot, ok := tileToSlot[t]
			if !ok {
				if s, alloc := allocSlot(); alloc {
					slot = s
					tileToSlot[t] = slot
					f.Uploads = append(f.Uploads, Upload{Slot: slot, Tile: t})
				} else {
					slot = 0
				}
			}
			writeCell(f, j, t, slot)
		}
	}

	bayer8 := [64]int{
		0, 32, 8, 40, 2, 34, 10, 42,
		48, 16, 56, 24, 50, 18, 58, 26,
		12, 44, 4, 36, 14, 46, 6, 38,
		60, 28, 52, 20, 62, 30, 54, 22,
		3, 35, 11, 43, 1, 33, 9, 41,
		51, 19, 59, 27, 49, 17, 57, 25,
		15, 47, 7, 39, 13, 45, 5, 37,
		63, 31, 55, 23, 61, 29, 53, 21,
	}
	type ck struct{ key, j int }
	ord := make([]ck, size)
	for j := 0; j < size; j++ {
		tx := j % tileX
		ty := j / tileX
		ord[j] = ck{bayer8[(ty&7)*8+(tx&7)], j}
	}
	sort.SliceStable(ord, func(a, b int) bool { return ord[a].key < ord[b].key })
	scatterOrder := make([]int, size)
	for k := range ord {
		scatterOrder[k] = ord[k].j
	}

	emitting := false
	lastEmitIdx := -1
	emitFrame := func(f FrameData) {
		if !emitting {
			return
		}
		video.Frames = append(video.Frames, f)
		lastEmitIdx = len(video.Frames) - 1
	}

	processFrame := func(i int) {
		pal := scenePals[sceneOf[i]]
		prevSrc := (i - 1 + count) % count
		newPalette := sceneOf[i] != sceneOf[prevSrc]
		lastEmitIdx = -1

		eff := make([]Tile, size)
		for j := 0; j < size; j++ {
			t := frameTiles[i][j]
			if curSet[j] && t != cur[j] && tilesClose(cur[j], t) {
				t = cur[j]
			}
			eff[j] = t
		}

		need := make(map[Tile]struct{})
		changed := 0
		for j := 0; j < size; j++ {
			t := eff[j]
			if curSet[j] && cur[j] == t {
				continue
			}
			changed++
			if _, ok := tileToSlot[t]; !ok {
				need[t] = struct{}{}
			}
		}
		avail := len(freeSlots) + (maxSlots - int(nextSlot))

		exhausted := len(need) > avail
		if cfg.Mode == ModeDMG && changed > dmgUpdateBudget {
			exhausted = true
		}

		if !exhausted {
			f := FrameData{Palette: pal, NewPalette: newPalette, SrcIndex: i, Settle: true, Delay: 1}
			for j := 0; j < size; j++ {
				t := eff[j]
				if curSet[j] && cur[j] == t {
					continue
				}
				slot, ok := tileToSlot[t]
				if !ok {
					s, _ := allocSlot()
					slot = s
					tileToSlot[t] = slot
					f.Uploads = append(f.Uploads, Upload{Slot: slot, Tile: t})
				}
				writeCell(&f, j, t, slot)
			}
			emitFrame(f)
			compact()
			return
		}

		if newPalette {
			f := FrameData{Palette: pal, NewPalette: true, Keyframe: true, SrcIndex: i, Settle: true, Delay: 1}
			fillKeyframe(&f, eff)
			emitFrame(f)
			compact()
			return
		}

		pending := make([]int, 0, size)
		for _, j := range scatterOrder {
			t := eff[j]
			if curSet[j] && cur[j] == t {
				continue
			}
			pending = append(pending, j)
		}
		perStep := dissolveBudget
		if cfg.Mode == ModeDMG && perStep > dmgUpdateBudget {
			perStep = dmgUpdateBudget
		}

		idx := 0
		for idx < len(pending) {
			f := FrameData{Palette: pal, NewPalette: false, SrcIndex: i, Settle: false, Delay: 1}
			flipped := 0
			for idx < len(pending) && flipped < perStep {
				j := pending[idx]
				t := eff[j]
				slot, ok := tileToSlot[t]
				if !ok {
					s, alloc := allocSlot()
					if !alloc {
						break
					}
					slot = s
					tileToSlot[t] = slot
					f.Uploads = append(f.Uploads, Upload{Slot: slot, Tile: t})
				}
				writeCell(&f, j, t, slot)
				idx++
				flipped++
			}

			if flipped == 0 {

				kf := FrameData{Palette: pal, NewPalette: true, Keyframe: true, SrcIndex: i, Settle: true, Delay: 1}
				fillKeyframe(&kf, eff)
				emitFrame(kf)
				compact()
				break
			}

			emitFrame(f)
			compact()
		}

		if emitting && lastEmitIdx >= 0 {
			video.Frames[lastEmitIdx].Settle = true
		}
	}

	buildPrologue := func() FrameData {
		f := FrameData{
			Palette:    scenePals[sceneOf[count-1]],
			NewPalette: true,
			Keyframe:   true,
			BootOnly:   true,
			SrcIndex:   count - 1,
			Delay:      1,
		}
		type ts struct {
			slot uint16
			tile Tile
		}
		arr := make([]ts, 0, len(tileToSlot))
		for tile, slot := range tileToSlot {
			arr = append(arr, ts{slot, tile})
		}
		sort.Slice(arr, func(a, b int) bool { return arr[a].slot < arr[b].slot })
		for _, e := range arr {
			f.Uploads = append(f.Uploads, Upload{Slot: e.slot, Tile: e.tile})
		}
		for j := 0; j < size; j++ {
			mapOff := (j/tileX)*32 + j%tileX
			f.Updates = append(f.Updates, Update{
				ScreenPos: uint16(mapOff),
				TileIndex: screenTile[j],
				Attr:      screenAttr[j],
			})
		}
		return f
	}

	processAll := func() {
		for i := 0; i < count; i++ {
			processFrame(i)
		}
	}
	processAll()
	processAll()
	prologue := buildPrologue()
	emitting = true
	processAll()

	video.Frames = append([]FrameData{prologue}, video.Frames...)

	return video
}

func intRange(lo, hi int) []int {
	out := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

func paletteSetDist(a, b []RGB) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 1
	}
	const maxD = 255.0 * 255.0 * 3.0
	var sum float64
	for _, c := range a {
		best := math.MaxFloat64
		for _, d := range b {
			if dd := colorDist(c, d); dd < best {
				best = dd
			}
		}
		sum += best
	}
	return (sum / float64(len(a))) / maxD
}

func sampleRGB(pts []RGB, maxN int) []RGB {
	if len(pts) <= maxN || maxN <= 0 {
		return pts
	}
	step := len(pts) / maxN
	if step < 1 {
		step = 1
	}
	out := make([]RGB, 0, maxN)
	for i := 0; i < len(pts); i += step {
		out = append(out, pts[i])
	}
	return out
}

func buildScenePalettes(framePixels [][][]RGB, frameMeans [][]RGB, frames []int, size int) [8]Palette {

	pool := make([]RGB, 0, len(frames)*size)
	for _, fi := range frames {
		pool = append(pool, frameMeans[fi]...)
	}
	groupCenters := kmeans(pool, 8, 16)

	buckets := make([][]RGB, 8)
	for _, fi := range frames {
		means := frameMeans[fi]
		px := framePixels[fi]
		for idx := 0; idx < size; idx++ {
			g := nearestIdx(means[idx], groupCenters)
			buckets[g] = append(buckets[g], px[idx]...)
		}
	}

	var palettes [8]Palette
	for pi := range palettes {
		bucket := sampleRGB(buckets[pi], 20000)
		var colors []RGB
		switch {
		case len(bucket) == 0:
			colors = []RGB{{0, 0, 0}, {85, 85, 85}, {170, 170, 170}, {255, 255, 255}}
		case len(bucket) <= 4:
			colors = make([]RGB, 4)
			for j := 0; j < 4; j++ {
				colors[j] = bucket[j%len(bucket)]
			}
		default:
			colors = kmeans(bucket, 4, 16)
		}
		copy(palettes[pi].Colors[:], colors[:4])
	}

	for pi := range palettes {
		for ci := range palettes[pi].Colors {
			palettes[pi].Colors[ci] = roundRGB555(palettes[pi].Colors[ci])
		}
	}
	return palettes
}

func sceneGrayscalePalette(framePixels [][][]RGB, frames []int) [8]Palette {
	pool := make([]RGB, 0)
	for _, fi := range frames {
		for _, px := range framePixels[fi] {
			for _, c := range px {
				lum := 0.299*c.R + 0.587*c.G + 0.114*c.B
				pool = append(pool, RGB{lum, lum, lum})
			}
		}
	}
	centers := kmeans(sampleRGB(pool, 40000), 4, 24)
	sort.Slice(centers, func(i, j int) bool { return centers[i].R > centers[j].R })
	for i := range centers {
		centers[i] = roundRGB555(centers[i])
	}
	var palettes [8]Palette
	copy(palettes[0].Colors[:], centers)
	for i := 1; i < 8; i++ {
		palettes[i] = palettes[0]
	}
	return palettes
}

func tilefyFrameFixed(framePx [][]RGB, size int, palettes [8]Palette, mode ConvertMode) []Tile {
	tiles := make([]Tile, size)
	for idx := 0; idx < size; idx++ {
		px := framePx[idx]
		var t Tile

		if mode == ModeDMG {
			t.Palette = 0
		} else {

			bestPal, bestErr := 0, math.MaxFloat64
			for pi := range palettes {
				var e float64
				for _, p := range px {
					best := math.MaxFloat64
					for _, pc := range palettes[pi].Colors {
						if d := colorDist(p, pc); d < best {
							best = d
						}
					}
					e += best
				}
				if e < bestErr {
					bestErr = e
					bestPal = pi
				}
			}
			t.Palette = uint8(bestPal)
		}

		pal := palettes[t.Palette]
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				p := px[y*8+x]
				best, bestDist := 0, math.MaxFloat64
				for ci, pc := range pal.Colors {
					if d := colorDist(p, pc); d < bestDist {
						bestDist = d
						best = ci
					}
				}
				t.Pixels[y][x] = uint8(best)
			}
		}
		tiles[idx] = t
	}
	return tiles
}
