package video

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"MediaBoy/internal/ffmpeg"
	"MediaBoy/internal/imaging"
)

const gbvp2AudioRate = 9198

func ExtractFrames(path string, fps int, progress func(done, total int)) ([]*image.RGBA, error) {
	if err := ffmpeg.Ensure(); err != nil {
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
		img, err := imaging.DecodeImageFile(f)
		if err != nil {
			return nil, err
		}
		frames = append(frames, imaging.ToRGBA(img))
		if progress != nil {
			progress(i+1, len(files))
		}
	}
	return frames, nil
}

func ExtractAudio(path string) []byte {
	audio, err := ffmpeg.DecodePCMU8(path, gbvp2AudioRate)
	if err != nil || len(audio) < 2 {
		return nil
	}
	return audio
}
