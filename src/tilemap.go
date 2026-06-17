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

	// Round palette colors to RGB555 — GBC hardware constraint.
	// All intermediate grouping was done in full float64 precision above.
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

// tileficationDMG converts to grayscale and builds a single 4-shade palette.
// All tiles are assigned to palette 0 (DMG has only one background palette).
func tileficationDMG(inp image.Image) ([][]Tile, [8]Palette) {
	gray := toGrayscale(inp)
	b := gray.Bounds()
	tileX := b.Dx() / 8
	tileY := b.Dy() / 8

	// Collect all pixels for global k-means — full precision
	allPixels := make([]RGB, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			allPixels = append(allPixels, toRGBFull(gray.At(x, y)))
		}
	}

	centers := kmeans(allPixels, 4, 30)

	// Sort light to dark (index 0 = lightest = DMG color 0 = white)
	sort.Slice(centers, func(i, j int) bool {
		return centers[i].R > centers[j].R
	})

	// Round to RGB555 — DMG hardware constraint
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

// quantizeKmeans reduces the image palette to numColors via k-means clustering.
// No tile constraints — works on the full image directly.
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

// ── Video tilefication ──────────────────────────────────────────────────────
//
// The pipeline has three stages:
//
//  1. Scene segmentation. Each frame gets a colour "signature" (8 representative
//     colours from a coarse k-means on its tile means). Walking frames in order,
//     a new scene starts whenever the signature diverges from the current scene
//     by more than cfg.SceneThreshold. Similar frames stay in one scene; a hard
//     colour-scheme change opens a new one.
//
//  2. Shared palette per scene. All tiles of all frames in a scene are pooled and
//     a single set of 8 CGB palettes is built (same algorithm as tilefication).
//     Because consecutive frames share palettes, identical regions produce
//     identical Tile structs — which is what makes the delta encoder below
//     actually deduplicate.
//
//  3. Delta encoding. A persistent tile→slot cache spans frames. Only new tiles
//     are uploaded; only changed map cells are rewritten. The cache (and screen
//     state) reset at each scene boundary, forcing a clean keyframe.
//
// For DMG mode the whole video uses one grayscale 4-shade palette (no scenes).
// tileficationVideo encodes inp into delta-compressed VideoData.
// origStride: spacing between original frames (origStride=1 means no interpolation;
// origStride=N+1 means N interpolated frames were inserted between each original pair).
// Scene detection and palette computation use only original frames; interpolated
// frames are assigned to the same scene as their preceding original frame and are
// tilified against that scene's palette, so their tiles are comparable with
// surrounding originals and the deduplication cache works correctly.
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

	// Precompute per-frame tile pixels + tile means (reused for signatures + palettes).
	framePixels := make([][][]RGB, count) // [frame][tileIdx][64]
	frameMeans := make([][]RGB, count)    // [frame][tileIdx]
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

	// ── Stage 1: scene segmentation (original frames only) ───────────────────
	// Original frames are at inp positions 0, origStride, 2*origStride, ...
	// Interpolated frames inherit the scene of their preceding original frame,
	// so their tiles use the same palette and are comparable in the dedup cache.
	origCount := 0
	for i := 0; i < count; i += origStride {
		origCount++
	}

	// scenes: lists of ORIGINAL frame indices (0..origCount-1), not inp indices.
	// When interpolation is active (origStride > 1), scene breaks are forbidden
	// mid-transition: a palette change between adjacent original frames forces
	// all tiles to be "new" (different Tile.Palette), defeating deduplication and
	// producing keyframes in the middle of blended transitions. So we merge any
	// scene that would start on an original frame that follows interpolated frames.
	var scenes [][]int
	if cfg.Mode == ModeDMG || origStride > 1 {
		// One scene for all original frames: shared palette, full dedup across all.
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

	// origSceneOf[oi] = scene index for original frame oi.
	origSceneOf := make([]int, origCount)
	for si, oframes := range scenes {
		for _, oi := range oframes {
			origSceneOf[oi] = si
		}
	}

	// sceneOf[i] for all inp frames: interpolated frame i → scene of floor(i/origStride).
	sceneOf := make([]int, count)
	for i := 0; i < count; i++ {
		oi := i / origStride
		if oi >= origCount {
			oi = origCount - 1
		}
		sceneOf[i] = origSceneOf[oi]
	}

	// ── Stage 2: shared palette (original frames only) + tilefy all frames ───
	// Palette is computed from original frames in each scene only; interpolated
	// frames in the same scene are then tilified against that palette so their
	// Tile structs are directly comparable with surrounding originals.
	frameTiles := make([][]Tile, count) // [frame][tileIdx] flat (row-major)
	scenePals := make([][8]Palette, len(scenes))
	for si, oframes := range scenes {
		// Convert original-frame indices to inp indices for pixel data lookup.
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
		// Tilefy every frame in this scene (original + interpolated).
		for i := 0; i < count; i++ {
			if sceneOf[i] == si {
				frameTiles[i] = tilefyFrameFixed(framePixels[i], size, pals, cfg.Mode)
			}
		}
	}

	// ── Stage 3: delta encoding (all-or-nothing per frame) ───────────────────
	//
	// VRAM holds maxSlots tiles; a full screen is `size` (≈360) tiles. New tiles
	// can only be uploaded into slots NOT currently on screen — otherwise the
	// in-progress upload would corrupt the displayed frame. So each frame is one
	// of two kinds, never a partial mix (which is what produced artifacts):
	//
	//   • Light frame: the new tiles fit in off-screen slots
	//     (newCount ≤ free capacity). Upload them all while the old frame stays
	//     fully shown, then swap the whole map in ONE GDMA → clean instant frame,
	//     display stays on.
	//   • Keyframe: the new tiles don't fit beside the shown frame. The player
	//     fades the palette to black, reloads VRAM from scratch while black, then
	//     fades back in (CGB). No white flash, no garbage — reads as a crossfade.
	//     DMG has no GDMA/colour fade, so its keyframes use a brief DISPLAY_OFF.
	//
	// DMG additionally caps a light frame's map writes to what fits one VBlank
	// (no GDMA → cells are written individually); over the cap it's a keyframe.
	const (
		dmgUpdateBudget = 80 // map cells/frame writable in one vblank (DMG)
	)
	maxSlots := 512
	if cfg.Mode == ModeDMG {
		maxSlots = 256
	}

	video := &VideoData{Frames: make([]FrameData, count)}

	tileToSlot := map[Tile]uint16{}
	var nextSlot uint16
	// freeSlots holds VRAM slots that were evicted from the visible screen and
	// can be reused for new tiles without advancing nextSlot further.
	freeSlots := make([]uint16, 0, 512)

	screenTile := make([]uint8, size) // bank-local tile index currently shown
	screenAttr := make([]uint8, size)
	cur := make([]Tile, size)    // Tile struct currently displayed per cell
	curSet := make([]bool, size) // false until a cell has ever been written

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

	// allocSlot returns a fresh VRAM slot, preferring reclaimed dead ones first.
	// Both sources are guaranteed off-screen, so uploading there is safe while a
	// frame is displayed.
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

	// compact evicts invisible tiles from tileToSlot, returning their slots to
	// freeSlots for reuse by subsequent frames without advancing nextSlot.
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

	// writeCell records cell j → tile in the screen state and emits a map Update.
	writeCell := func(f *FrameData, j int, tile Tile, slot uint16) {
		tileIdx := uint8(slot & 0xFF)
		attr := tile.Palette & 7
		if slot >= 256 {
			attr |= 0x08 // VRAM bank 1
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

	for i := 0; i < count; i++ {
		f := &video.Frames[i]
		f.Palette = scenePals[sceneOf[i]]
		f.Delay = 1
		f.NewPalette = i == 0 || sceneOf[i] != sceneOf[i-1]
		f.Uploads = f.Uploads[:0]
		f.Updates = f.Updates[:0]

		tiles := frameTiles[i]

		// Count distinct new tiles (not currently resident) and changed cells.
		need := make(map[Tile]struct{})
		changed := 0
		for j := 0; j < size; j++ {
			t := tiles[j]
			if curSet[j] && cur[j] == t {
				continue
			}
			changed++
			if _, ok := tileToSlot[t]; !ok {
				need[t] = struct{}{}
			}
		}
		avail := len(freeSlots) + (maxSlots - int(nextSlot))

		// Decide light frame vs keyframe.
		// Frame 0 is NOT forced: VRAM is empty so avail == maxSlots, need always fits.
		// CGB keyframe = tiles don't fit beside the current screen → fade-to-black.
		// DMG keyframe = tile slots OR map-cell writes exceed one-vblank budget.
		keyframe := len(need) > avail
		if cfg.Mode == ModeDMG && changed > dmgUpdateBudget {
			keyframe = true
		}
		f.Keyframe = keyframe

		if keyframe {
			// Full reload: during the black fade / blank, any slot may be reused.
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
						slot = 0 // DMG with >256 unique tiles — clamp (rare)
					}
				}
				writeCell(f, j, t, slot)
			}
		} else {
			// Light frame: every new tile fits in an off-screen slot. Upload all,
			// then the player swaps the whole map atomically (clean, display on).
			for j := 0; j < size; j++ {
				t := tiles[j]
				if curSet[j] && cur[j] == t {
					continue
				}
				slot, ok := tileToSlot[t]
				if !ok {
					s, _ := allocSlot() // guaranteed to fit: len(need) ≤ avail
					slot = s
					tileToSlot[t] = slot
					f.Uploads = append(f.Uploads, Upload{Slot: slot, Tile: t})
				}
				writeCell(f, j, t, slot)
			}
		}

		compact()
	}

	return video
}

// intRange returns [lo, hi).
func intRange(lo, hi int) []int {
	out := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

// paletteSetDist measures how different two colour signatures are, normalised to
// 0..1. For each colour in a, the nearest colour in b is found; the mean squared
// distance is divided by the maximum possible squared distance (255²·3).
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

// sampleRGB returns at most maxN evenly-spaced samples from pts (for bounding
// k-means cost over large scene pixel pools).
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

// buildScenePalettes pools the tiles of all frames in a scene and builds 8 shared
// CGB palettes — the same group-then-refine algorithm as tilefication.
func buildScenePalettes(framePixels [][][]RGB, frameMeans [][]RGB, frames []int, size int) [8]Palette {
	// Pool tile means across the scene for the 8-way grouping.
	pool := make([]RGB, 0, len(frames)*size)
	for _, fi := range frames {
		pool = append(pool, frameMeans[fi]...)
	}
	groupCenters := kmeans(pool, 8, 16)

	// Assign every (frame,tile) to a group; gather its pixels.
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

// sceneGrayscalePalette builds a single 4-shade grayscale palette (DMG) from all
// frames in the scene, replicated across all 8 palette slots.
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

// tilefyFrameFixed quantises one frame's tiles against an already-built palette
// set, returning a flat row-major slice of Tiles.
func tilefyFrameFixed(framePx [][]RGB, size int, palettes [8]Palette, mode ConvertMode) []Tile {
	tiles := make([]Tile, size)
	for idx := 0; idx < size; idx++ {
		px := framePx[idx]
		var t Tile

		if mode == ModeDMG {
			t.Palette = 0
		} else {
			// Pick the palette with the lowest total quantisation error.
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
