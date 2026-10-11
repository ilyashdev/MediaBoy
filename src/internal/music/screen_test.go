package music

import (
	"image"
	"image/color"
	"math/rand"
	"strings"
	"testing"
)

// TestScreenBudget builds the screen of a noise cover, whose tiles are all
// different, and checks they were merged into the song's tiles and the map
// points only at tiles that exist.
func TestScreenBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, 160, 140))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.Intn(256))
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	for _, dmg := range []bool{false, true} {
		s := buildScreen(img, strings.Repeat("Long title ", 4), dmg)
		if len(s.tiles) > songTileBudget {
			t.Fatalf("dmg %v: %d tiles, budget %d", dmg, len(s.tiles), songTileBudget)
		}
		for y := range scrH {
			for x := range scrW {
				id := int(s.tileMap[y][x])
				if id >= len(s.tiles) && id < glyphBase {
					t.Fatalf("dmg %v: cell %d,%d shows tile %d of %d", dmg, x, y, id, len(s.tiles))
				}
				if !dmg && y >= coverY && y < coverY+coverTilesH && x >= coverX && x < coverX+coverTilesW && s.attrs[y][x] == uiPaletteIdx {
					t.Fatalf("cover cell %d,%d uses the UI palette", x, y)
				}
			}
		}
		if b := s.render(dmg, 0, 3, 125).Bounds(); b.Dx() != 160 || b.Dy() != 144 {
			t.Fatalf("render is %v", b)
		}
	}
}

// TestScreenDeterministic: the preview must show what the ROM will.
func TestScreenDeterministic(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), uint8(x * y), 255})
		}
	}
	a, b := buildScreen(img, "x", false), buildScreen(img, "x", false)
	if a.tileMap != b.tileMap || a.palettes != b.palettes || len(a.tiles) != len(b.tiles) {
		t.Fatal("two builds of the same screen differ")
	}
}

// TestTransparentCover: transparency shows the UI background, not black.
func TestTransparentCover(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32)) // all transparent
	out := resizeCoverFill(img, color.RGBA{14, 14, 22, 255})
	if c := out.RGBAAt(5, 5); c.R != 14 || c.B != 22 {
		t.Fatalf("transparent pixel came out %v", c)
	}
}
