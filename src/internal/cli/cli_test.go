package cli

import (
	"flag"
	"image"
	"io"
	"slices"
	"testing"
)

func TestParseFlagsAroundFiles(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("o", "out", "")
	files, err := parse(fs, []string{"a.png", "-o", "roms", "b.png", "--", "-c.png"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.png", "b.png", "-c.png"}; !slices.Equal(files, want) {
		t.Errorf("files = %q, want %q", files, want)
	}
	if *out != "roms" {
		t.Errorf("-o = %q, want roms", *out)
	}
}

func TestCropFlag(t *testing.T) {
	var c cropFlag
	if err := c.Set("10, 20,320,288"); err != nil {
		t.Fatal(err)
	}
	if want := image.Rect(10, 20, 330, 308); c.r != want {
		t.Errorf("crop = %v, want %v", c.r, want)
	}
	for _, bad := range []string{"1,2,3", "a,b,c,d", "0,0,0,10", "-1,0,10,10"} {
		if err := c.Set(bad); err == nil {
			t.Errorf("Set(%q) accepted", bad)
		}
	}
}

func TestChoice(t *testing.T) {
	c := newChoice("trim", videoFitWords...)
	if err := c.Set("Split"); err != nil || c.index() != videoFitSplit {
		t.Errorf("Set(Split): err %v, index %d", err, c.index())
	}
	if err := c.Set("cut"); err == nil {
		t.Error("Set(cut) accepted")
	}
}
