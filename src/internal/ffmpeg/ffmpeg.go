package ffmpeg

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
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

// StreamFrames decodes the video stream of path through the filter chain vf
// and calls fn with every frame, in order. fn may keep the frame; a non-nil
// error from fn stops decoding and is returned.
func StreamFrames(path, vf string, fn func(*image.RGBA) error) error {
	if err := Ensure(); err != nil {
		return err
	}
	args := []string{"-v", "error", "-i", path, "-an", "-sn"}
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	args = append(args, "-f", "image2pipe", "-c:v", "pam", "-pix_fmt", "rgba", "-")
	cmd := exec.Command("ffmpeg", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReaderSize(stdout, 1<<20)
	var loopErr error
	for {
		img, err := readPAM(r)
		if err == io.EOF {
			break
		}
		if err == nil {
			err = fn(img)
		}
		if err != nil {
			loopErr = err
			_ = cmd.Process.Kill()
			break
		}
	}
	waitErr := cmd.Wait()
	if loopErr != nil {
		return loopErr
	}
	if waitErr != nil {
		return fmt.Errorf("ffmpeg frame extraction failed: %v\n%s", waitErr, errb.String())
	}
	return nil
}

// ProbeFrameSize returns the size of the decoded (rotation-applied) frames.
func ProbeFrameSize(path string) (image.Point, error) {
	var size image.Point
	errStop := errors.New("stop")
	err := StreamFrames(path, "", func(img *image.RGBA) error {
		size = img.Bounds().Size()
		return errStop
	})
	if err != nil && err != errStop {
		return image.Point{}, err
	}
	if size == (image.Point{}) {
		return image.Point{}, fmt.Errorf("no video frames in %s", path)
	}
	return size, nil
}

// readPAM reads one RGBA frame of ffmpeg's PAM (P7) image2pipe output.
func readPAM(r *bufio.Reader) (*image.RGBA, error) {
	magic, err := r.ReadString('\n')
	if err != nil {
		if err == io.EOF && magic == "" {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("pam: truncated header")
	}
	if strings.TrimSpace(magic) != "P7" {
		return nil, fmt.Errorf("pam: bad magic %q", magic)
	}
	w, h, depth := 0, 0, 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("pam: truncated header")
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "ENDHDR" {
			break
		}
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.Atoi(f[1])
		switch f[0] {
		case "WIDTH":
			w = n
		case "HEIGHT":
			h = n
		case "DEPTH":
			depth = n
		}
	}
	if w <= 0 || h <= 0 || depth != 4 {
		return nil, fmt.Errorf("pam: unsupported frame %dx%d depth %d", w, h, depth)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if _, err := io.ReadFull(r, img.Pix); err != nil {
		return nil, fmt.Errorf("pam: truncated frame: %v", err)
	}
	return img, nil
}
