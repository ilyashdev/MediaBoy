package main

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const gbvp2AudioRate = 9198

func probeVideoFPS(path string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=r_frame_rate",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	parts := strings.SplitN(s, "/", 2)
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}
	den := 1.0
	if len(parts) == 2 {
		if d, e := strconv.ParseFloat(parts[1], 64); e == nil && d != 0 {
			den = d
		}
	}
	return num / den, nil
}

func extractVideoFrames(path string, fps int, progress func(done, total int)) ([]*image.RGBA, error) {
	if err := ensureFFmpeg(); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "mb_vframes_*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-vf", fmt.Sprintf("fps=%d", fps), "-y", filepath.Join(tmp, "%06d.png"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extraction failed: %v\n%s", err, out)
	}

	files, _ := filepath.Glob(filepath.Join(tmp, "*.png"))
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no frames extracted from %s", filepath.Base(path))
	}
	frames := make([]*image.RGBA, 0, len(files))
	for i, f := range files {
		img, err := decodeImageFile(f)
		if err != nil {
			return nil, err
		}
		frames = append(frames, toRGBA(img))
		if progress != nil {
			progress(i+1, len(files))
		}
	}
	return frames, nil
}

func extractVideoAudio(path string) []byte {
	audio, err := decodePCMU8(path, gbvp2AudioRate)
	if err != nil || len(audio) < 2 {
		return nil
	}
	return audio
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(b)
	draw.Draw(r, b, img, b.Min, draw.Src)
	return r
}
