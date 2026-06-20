package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
)

const defaultPCMRate = 4096

func ensureFFmpeg() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found on PATH (needed to decode audio for PCM/auto codecs)")
	}
	return nil
}

func decodePCMU8(path string, sampleRate int) ([]byte, error) {
	if err := ensureFFmpeg(); err != nil {
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

func quantize3(s int) byte {
	switch {
	case s < 18:
		return 0
	case s < 55:
		return 1
	case s < 91:
		return 2
	case s < 130:
		return 3
	case s < 164:
		return 4
	case s < 200:
		return 5
	case s < 237:
		return 6
	default:
		return 7
	}
}

const pcmChunkSize = 0x4000

func packPCM3bitNR50(rawU8 []byte) []byte {
	n := len(rawU8) / 2
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		l := quantize3(int(rawU8[i*2]))
		r := quantize3(int(rawU8[i*2+1]))
		out[i] = (l << 4) | r
	}
	return out
}

func encodeSongPCMChunks(path string, rate int) (chunks [][]byte, samples int, err error) {
	raw, err := decodePCMU8(path, rate)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) < 2 {
		return nil, 0, fmt.Errorf("no audio decoded from %s", path)
	}
	packed := packPCM3bitNR50(raw)
	for off := 0; off < len(packed); off += pcmChunkSize {
		end := off + pcmChunkSize
		if end > len(packed) {
			end = len(packed)
		}
		chunks = append(chunks, packed[off:end])
	}
	return chunks, len(packed), nil
}

func audioTimerTMA(rate int, doubleSpeed bool) int {
	if rate <= 0 {
		rate = defaultPCMRate
	}
	base := 262144
	if doubleSpeed {
		base = 524288
	}
	div := base / rate
	if div < 1 {
		div = 1
	}
	if div > 255 {
		div = 255
	}
	return 256 - div
}
