package music

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"MediaBoy/internal/core"
	"MediaBoy/internal/imaging"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// The player's screen, 20×18 tiles:
//
//	row 0       ▶ 3/12                 4:05    status, in sprites
//	rows 1-14   the cover, 16×14 tiles from column 2
//	rows 15-16  the title, 16×2 tiles
//	row 17      ▬▬▬▬▬▬▬▬▬▬            1:23    progress bar, elapsed time (sprites)
//
// Background and sprites share the 256 tiles at $8000: a song's cover and
// title get the first songTileBudget, the rest are fixed (glyphs for the
// sprites, the bar, a blank). On CGB the cover has palettes 0-6 and the UI
// palette 7; on DMG everything is in the four shades of BGP.
const (
	scrW, scrH     = 20, 18
	coverX, coverY = 2, 1
	titleX, titleY = 2, 15
	barX, barY     = 2, 17
	barTiles       = 10 // 80 px
	barRow0        = 3  // the bar's two pixel rows in its tiles
	uiPaletteIdx   = 7

	glyphBase      = 238 // 0-9, ':', '/', play, pause
	glyphColon     = glyphBase + 10
	glyphSlash     = glyphBase + 11
	glyphPlay      = glyphBase + 12
	glyphPause     = glyphBase + 13
	tileBarPart    = 252 // rewritten as the bar grows
	tileBarFull    = 253
	tileBarEmpty   = 254
	tileBlank      = 255
	songTileBudget = glyphBase
)

// UI colours: background, dim (the empty bar), accent, foreground. On DMG
// they are BGP shades 3..0 (black to white).
type uiShade uint8

const (
	shadeBG uiShade = iota
	shadeDim
	shadeAccent
	shadeFG
)

func (s uiShade) index(dmg bool) uint8 {
	if dmg {
		return 3 - uint8(s)
	}
	return uint8(s)
}

var uiPalette = core.Palette{Colors: [4]core.RGB{
	{R: 14, G: 14, B: 22}, {R: 60, G: 62, B: 86}, {R: 112, G: 196, B: 255}, {R: 248, G: 248, B: 248},
}}

// dmgShades is how the preview shows BGP's four shades.
var dmgShades = core.Palette{Colors: [4]core.RGB{
	{R: 232, G: 232, B: 224}, {R: 160, G: 160, B: 152}, {R: 88, G: 88, B: 84}, {R: 24, G: 24, B: 24},
}}

// glyphs are the sprite characters, 5×7 in an 8×8 tile.
var glyphRows = [14][7]string{
	{".###.", "#...#", "#..##", "#.#.#", "##..#", "#...#", ".###."},
	{"..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."},
	{".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	{"####.", "....#", "....#", ".###.", "....#", "....#", "####."},
	{"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	{"#####", "#....", "####.", "....#", "....#", "#...#", ".###."},
	{".###.", "#....", "#....", "####.", "#...#", "#...#", ".###."},
	{"#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."},
	{".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."},
	{".###.", "#...#", "#...#", ".####", "....#", "....#", ".###."},
	{".....", "..#..", "..#..", ".....", "..#..", "..#..", "....."},
	{"....#", "...#.", "...#.", "..#..", ".#...", ".#...", "#...."},
	{"#....", "##...", "###..", "####.", "###..", "##...", "#...."},
	{"##.##", "##.##", "##.##", "##.##", "##.##", "##.##", "##.##"},
}

// glyphTiles are the sprite tiles, colour 3 on transparent.
func glyphTiles() []core.Tile {
	out := make([]core.Tile, len(glyphRows))
	for g, rows := range glyphRows {
		for y, row := range rows {
			for x, c := range row {
				if c == '#' {
					out[g].Pixels[y][x+1] = 3
				}
			}
		}
	}
	return out
}

// barTile is the bar's tile with filled of its 8 pixels lit.
func barTile(filled int, dmg bool) core.Tile {
	var t core.Tile
	for y := range 8 {
		for x := range 8 {
			s := shadeBG
			if y == barRow0 || y == barRow0+1 {
				s = shadeDim
				if x < filled {
					s = shadeFG
				}
			}
			t.Pixels[y][x] = s.index(dmg)
		}
	}
	return t
}

func blankTile(dmg bool) core.Tile {
	var t core.Tile
	for y := range 8 {
		for x := range 8 {
			t.Pixels[y][x] = shadeBG.index(dmg)
		}
	}
	return t
}

// screen is everything a song's screen needs in VRAM besides the fixed tiles.
type screen struct {
	tiles    []core.Tile // tiles 0..len-1
	tileMap  [scrH][scrW]uint8
	attrs    [scrH][scrW]uint8 // CGB palette per cell
	palettes [8]core.Palette   // CGB; on DMG only for the preview
}

// buildScreen lays out the cover and title of a song.
func buildScreen(cover image.Image, title string, dmg bool) *screen {
	s := &screen{}
	bg := uiPalette.Colors[0]
	if cover == nil {
		cover = placeholderCover(title)
	}
	img := resizeCoverFill(cover, color.RGBA{uint8(bg.R), uint8(bg.G), uint8(bg.B), 255})
	var coverT [][]core.Tile
	if dmg {
		coverT, s.palettes = imaging.TileficationDMG(img)
		for i := range s.palettes {
			s.palettes[i] = dmgShades
		}
	} else {
		coverT, s.palettes = imaging.TileficationN(img, uiPaletteIdx)
		s.palettes[uiPaletteIdx] = uiPalette
	}

	type cell struct{ x, y int }
	var cells []cell
	var cellTiles []core.Tile
	for y := range coverTilesH {
		for x := range coverTilesW {
			cells = append(cells, cell{coverX + x, coverY + y})
			cellTiles = append(cellTiles, coverT[y][x])
		}
	}
	for y, row := range titleTiles(title, dmg) {
		for x, t := range row {
			cells = append(cells, cell{titleX + x, titleY + y})
			cellTiles = append(cellTiles, t)
		}
	}

	for y := range scrH {
		for x := range scrW {
			s.tileMap[y][x] = tileBlank
			s.attrs[y][x] = uiPaletteIdx
		}
	}
	for x := range barTiles {
		s.tileMap[barY][barX+x] = tileBarEmpty
	}

	pals := make([]core.Palette, len(cells))
	for i, t := range cellTiles {
		pals[i] = s.palettes[t.Palette]
		if dmg {
			pals[i] = dmgShades
		}
	}
	ids := dedupTiles(cellTiles, pals, blankTile(dmg), songTileBudget)
	for i, c := range cells {
		s.tileMap[c.y][c.x] = ids.cell[i]
		s.attrs[c.y][c.x] = cellTiles[i].Palette & 7
		if dmg {
			s.attrs[c.y][c.x] = 0
		}
	}
	s.tiles = ids.tiles
	return s
}

type dedup struct {
	tiles []core.Tile // pixels only
	cell  []uint8     // tile index per cell (tileBlank for the blank tile)
}

// dedupTiles gives each distinct tile (by pixels) one index, the blank tile
// tileBlank. Over budget, it merges the tile whose replacement by another
// costs least, as each cell shows it in its palette, until the rest fit.
func dedupTiles(cellTiles []core.Tile, pals []core.Palette, blank core.Tile, budget int) dedup {
	type px = [8][8]uint8
	const blankID = -1
	ids := map[px]int{blank.Pixels: blankID}
	var uniq []px
	cellID := make([]int, len(cellTiles))
	for i, t := range cellTiles {
		id, ok := ids[t.Pixels]
		if !ok {
			id = len(uniq)
			ids[t.Pixels] = id
			uniq = append(uniq, t.Pixels)
		}
		cellID[i] = id
	}

	alive := make([]bool, len(uniq))
	for i := range alive {
		alive[i] = true
	}
	tileOf := func(id int) *px {
		if id == blankID {
			return &blank.Pixels
		}
		return &uniq[id]
	}
	cellsOf := make([][]int, len(uniq))
	for c, id := range cellID {
		if id != blankID {
			cellsOf[id] = append(cellsOf[id], c)
		}
	}
	// cost of showing tile b in the cells of tile a
	cost := func(a, b int) float64 {
		ta, tb := tileOf(a), tileOf(b)
		sum := 0.0
		for _, c := range cellsOf[a] {
			for y := range 8 {
				for x := range 8 {
					sum += core.ColorDist(pals[c].Colors[ta[y][x]&3], pals[c].Colors[tb[y][x]&3])
				}
			}
		}
		return sum
	}
	for n := len(uniq); n > budget; n-- {
		bestA, bestB, best := -1, blankID, math.MaxFloat64
		for a := range uniq {
			if !alive[a] {
				continue
			}
			if c := cost(a, blankID); c < best {
				bestA, bestB, best = a, blankID, c
			}
			for b := range uniq {
				if b == a || !alive[b] {
					continue
				}
				if c := cost(a, b); c < best {
					bestA, bestB, best = a, b, c
				}
			}
		}
		alive[bestA] = false
		for _, c := range cellsOf[bestA] {
			cellID[c] = bestB
		}
		if bestB != blankID {
			cellsOf[bestB] = append(cellsOf[bestB], cellsOf[bestA]...)
		}
		cellsOf[bestA] = nil
	}

	var out dedup
	index := make([]int, len(uniq))
	for id, ok := range alive {
		if ok {
			index[id] = len(out.tiles)
			out.tiles = append(out.tiles, core.Tile{Pixels: uniq[id]})
		}
	}
	for _, id := range cellID {
		if id == blankID {
			out.cell = append(out.cell, tileBlank)
		} else {
			out.cell = append(out.cell, uint8(index[id]))
		}
	}
	return out
}

// fixedTile is one of the tiles every screen shares.
func fixedTile(i int, dmg bool) core.Tile {
	switch {
	case i >= glyphBase && i < tileBarPart:
		return glyphTiles()[i-glyphBase]
	case i == tileBarPart || i == tileBarEmpty:
		return barTile(0, dmg)
	case i == tileBarFull:
		return barTile(8, dmg)
	}
	return blankTile(dmg)
}

// formatTime is m:ss.
func formatTime(sec int) string { return fmt.Sprintf("%d:%02d", sec/60, sec%60) }

// glyphOf is the glyph tile for a status character.
func glyphOf(c rune) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return glyphBase + int(c-'0'), true
	case c == ':':
		return glyphColon, true
	case c == '/':
		return glyphSlash, true
	}
	return 0, false
}

// render draws the screen with the status sprites as the player shows them
// before the song starts: track idx+1 of n, its length, 0:00 elapsed.
func (s *screen) render(dmg bool, idx, n, seconds int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, scrW*8, scrH*8))
	put := func(px, py int, c core.RGB) {
		out.Set(px, py, color.RGBA{uint8(c.R), uint8(c.G), uint8(c.B), 255})
	}
	tile := func(i int) core.Tile {
		if i < len(s.tiles) {
			return s.tiles[i]
		}
		return fixedTile(i, dmg)
	}
	for ty := range scrH {
		for tx := range scrW {
			t := tile(int(s.tileMap[ty][tx]))
			pal := s.palettes[s.attrs[ty][tx]]
			if dmg {
				pal = dmgShades
			}
			for y := range 8 {
				for x := range 8 {
					put(tx*8+x, ty*8+y, pal.Colors[t.Pixels[y][x]&3])
				}
			}
		}
	}
	white := uiPalette.Colors[3]
	if dmg {
		white = dmgShades.Colors[0]
	}
	sprite := func(g, px, py int) {
		t := glyphTiles()[g-glyphBase]
		for y := range 8 {
			for x := range 8 {
				if t.Pixels[y][x] != 0 {
					put(px+x, py+y, white)
				}
			}
		}
	}
	text := func(str string, px, py int) {
		for _, c := range str {
			if g, ok := glyphOf(c); ok {
				sprite(g, px, py)
			}
			px += 8
		}
	}
	sprite(glyphPlay, coverX*8, 0)
	track := fmt.Sprintf("%d/%d", idx+1, n)
	text(track, (coverX+2)*8, 0)
	total := formatTime(seconds)
	if 1+len(track)+len(total) <= 10 { // sprites per line
		text(total, (coverX+coverTilesW)*8-len(total)*8, 0)
	}
	elapsed := formatTime(0)
	text(elapsed, (coverX+coverTilesW)*8-len(elapsed)*8, barY*8)
	return out
}

// RenderDevicePreview is the player's screen for song idx of n, as the ROM
// for target dmg shows it.
func RenderDevicePreview(sg *core.Song, idx, n int, dmg bool) image.Image {
	return buildScreen(sg.Cover, sg.DisplayTitle(), dmg).render(dmg, idx, n, int(math.Round(sg.Seconds)))
}

// titleImage draws title centred in the title box, white on black.
func titleTiles(title string, dmg bool) [][]core.Tile {
	img := renderTitleImage(title)
	tiles := make([][]core.Tile, titleTilesH)
	for ty := range titleTilesH {
		tiles[ty] = make([]core.Tile, titleTilesW)
		for tx := range titleTilesW {
			t := core.Tile{Palette: uiPaletteIdx}
			for y := range 8 {
				for x := range 8 {
					s := shadeBG
					if img.GrayAt(tx*8+x, ty*8+y).Y > 127 {
						s = shadeFG
					}
					t.Pixels[y][x] = s.index(dmg)
				}
			}
			tiles[ty][tx] = t
		}
	}
	return tiles
}

// flatten draws src over an opaque bg, so transparent covers do not turn
// black.
func flatten(src image.Image, bg color.RGBA) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Over)
	return out
}

// cgbOnlyScreen is what a Game Boy Color ROM shows on an older Game Boy: a
// note on black, in BGP shades. Tile 0 is the blank one.
func cgbOnlyScreen() (tiles []core.Tile, tileMap [scrH][scrW]uint8) {
	img := image.NewGray(image.Rect(0, 0, scrW*8, scrH*8))
	face := basicfont.Face7x13
	for k, line := range []string{"This cartridge", "needs a", "Game Boy Color"} {
		w := font.MeasureString(face, line).Ceil()
		d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Gray{Y: 255}), Face: face,
			Dot: fixed.P((scrW*8-w)/2, 56+k*16)}
		d.DrawString(line)
	}
	ids := map[[8][8]uint8]uint8{}
	tiles = append(tiles, blankTile(true))
	ids[tiles[0].Pixels] = 0
	for ty := range scrH {
		for tx := range scrW {
			var t core.Tile
			for y := range 8 {
				for x := range 8 {
					s := shadeBG
					if img.GrayAt(tx*8+x, ty*8+y).Y > 127 {
						s = shadeFG
					}
					t.Pixels[y][x] = s.index(true)
				}
			}
			id, ok := ids[t.Pixels]
			if !ok {
				id = uint8(len(tiles))
				ids[t.Pixels] = id
				tiles = append(tiles, t)
			}
			tileMap[ty][tx] = id
		}
	}
	return tiles, tileMap
}
