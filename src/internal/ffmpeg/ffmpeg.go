package ffmpeg

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func Ensure() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found on PATH (needed to decode audio for PCM/auto codecs)")
	}
	return nil
}

func DecodePCMU8(path string, sampleRate int) ([]byte, error) {
	if err := Ensure(); err != nil {
		return nil, err
	}
	cmd := exec.Command("ffmpeg",
		"-v", "error",
		"-i", path,
		"-ac", "2",
		"-ar", strconv.Itoa(sampleRate),
		"-f", "u8",
		"-acodec", "pcm_u8",
		"-")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode failed: %v\n%s", err, errb.String())
	}
	return out.Bytes(), nil
}

func DecodeMonoS16(path string, sampleRate int) ([]byte, error) {
	if err := Ensure(); err != nil {
		return nil, err
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-ac", "1", "-ar", strconv.Itoa(sampleRate),
		"-f", "s16le", "-acodec", "pcm_s16le", "-")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode failed: %v\n%s", err, errb.String())
	}
	return out.Bytes(), nil
}

func ProbeFPS(path string) (float64, error) {
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

// ProbeDuration returns the container duration in seconds.
func ProbeDuration(path string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
