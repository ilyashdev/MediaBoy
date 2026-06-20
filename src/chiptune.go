package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"sort"
	"strconv"
)

const chipFPS = 60

func gbFreqValues(wave bool) []uint16 {
	clock := 131072.0
	if wave {
		clock = 65536.0
	}
	out := make([]uint16, 128)
	for n := 0; n < 128; n++ {
		f := 440.0 * math.Pow(2, float64(n-69)/12.0)
		x := 2048.0 - clock/f
		if x < 0 {
			x = 0
		}
		if x > 2047 {
			x = 2047
		}
		out[n] = uint16(x + 0.5)
	}
	return out
}

type midiNote struct {
	note, vel, ch int
	onF, offF     int
}

type rawEvent struct {
	absTick int
	status  byte
	d1, d2  byte
	tempo   int
}

func parseMIDI(data []byte) ([]midiNote, error) {
	if len(data) < 14 || string(data[0:4]) != "MThd" {
		return nil, fmt.Errorf("not a Standard MIDI File")
	}
	division := int(binary.BigEndian.Uint16(data[12:14]))
	if division <= 0 {
		return nil, fmt.Errorf("unsupported SMPTE time division")
	}

	var events []rawEvent
	pos := 14
	for pos+8 <= len(data) {
		if string(data[pos:pos+4]) != "MTrk" {
			break
		}
		length := int(binary.BigEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		end := pos + length
		if end > len(data) {
			end = len(data)
		}
		parseTrack(data[pos:end], &events)
		pos = end
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].absTick < events[j].absTick })

	tempo := 500000
	lastTick := 0
	seconds := 0.0
	tickSeconds := func(toTick int) float64 {
		seconds += float64(toTick-lastTick) * float64(tempo) / float64(division) / 1e6
		lastTick = toTick
		return seconds
	}

	type active struct{ onF, vel int }
	open := map[int]active{}
	var notes []midiNote
	for _, e := range events {
		sec := tickSeconds(e.absTick)
		if e.tempo > 0 {
			tempo = e.tempo
			continue
		}
		frame := int(sec*chipFPS + 0.5)
		switch e.status & 0xF0 {
		case 0x90:
			ch := int(e.status & 0x0F)
			key := ch<<8 | int(e.d1)
			if e.d2 == 0 {
				if a, ok := open[key]; ok {
					notes = append(notes, midiNote{int(e.d1), a.vel, ch, a.onF, frame})
					delete(open, key)
				}
			} else {
				open[key] = active{frame, int(e.d2)}
			}
		case 0x80:
			ch := int(e.status & 0x0F)
			key := ch<<8 | int(e.d1)
			if a, ok := open[key]; ok {
				notes = append(notes, midiNote{int(e.d1), a.vel, ch, a.onF, frame})
				delete(open, key)
			}
		}
	}
	if len(notes) == 0 {
		return nil, fmt.Errorf("no notes found in MIDI")
	}
	return notes, nil
}

func parseTrack(tr []byte, out *[]rawEvent) {
	pos, tick := 0, 0
	var status byte
	readVar := func() int {
		v := 0
		for pos < len(tr) {
			b := tr[pos]
			pos++
			v = (v << 7) | int(b&0x7F)
			if b&0x80 == 0 {
				break
			}
		}
		return v
	}
	for pos < len(tr) {
		tick += readVar()
		if pos >= len(tr) {
			break
		}
		b := tr[pos]
		if b&0x80 != 0 {
			status = b
			pos++
		}
		switch {
		case status == 0xFF:
			mtype := tr[pos]
			pos++
			length := readVar()
			if mtype == 0x51 && length == 3 && pos+3 <= len(tr) {
				t := int(tr[pos])<<16 | int(tr[pos+1])<<8 | int(tr[pos+2])
				*out = append(*out, rawEvent{absTick: tick, tempo: t})
			}
			pos += length
		case status == 0xF0 || status == 0xF7:
			length := readVar()
			pos += length
		case status&0xF0 == 0xC0 || status&0xF0 == 0xD0:
			d1 := tr[pos]
			pos++
			*out = append(*out, rawEvent{absTick: tick, status: status, d1: d1})
		default:
			if pos+2 > len(tr) {
				return
			}
			d1, d2 := tr[pos], tr[pos+1]
			pos += 2
			*out = append(*out, rawEvent{absTick: tick, status: status, d1: d1, d2: d2})
		}
	}
}

func midiToFrames(notes []midiNote) [][4]uint8 {
	maxF := 0
	for _, n := range notes {
		if n.offF > maxF {
			maxF = n.offF
		}
	}
	frames := make([][4]uint8, maxF+1)
	for f := 0; f <= maxF; f++ {
		var tonal []int
		drum := false
		for _, n := range notes {
			if f < n.onF || f >= n.offF {
				continue
			}
			if n.ch == 9 {
				if n.onF == f {
					drum = true
				}
				continue
			}
			tonal = append(tonal, n.note)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(tonal)))
		var ch [4]uint8
		if len(tonal) > 0 {
			ch[0] = uint8(tonal[0])
		}
		if len(tonal) > 1 {
			ch[1] = uint8(tonal[1])
		}
		if len(tonal) > 0 {
			ch[2] = uint8(tonal[len(tonal)-1])
		}
		if drum {
			ch[3] = 1
		}
		frames[f] = ch
	}
	return frames
}

func audioToFrames(path string) ([][4]uint8, error) {
	const sr = 22050
	raw, err := decodeMonoS16(path, sr)
	if err != nil {
		return nil, err
	}
	samples := make([]float64, len(raw)/2)
	for i := range samples {
		samples[i] = float64(int16(binary.LittleEndian.Uint16(raw[i*2:]))) / 32768.0
	}
	win := sr / chipFPS
	nframes := len(samples) / win
	frames := make([][4]uint8, nframes)
	minLag, maxLag := sr/1000, sr/60
	for fi := 0; fi < nframes; fi++ {
		seg := samples[fi*win : fi*win+win]
		var energy float64
		for _, s := range seg {
			energy += s * s
		}
		if energy/float64(win) < 1e-4 {
			continue
		}
		bestLag, bestCorr := 0, 0.0
		for lag := minLag; lag <= maxLag && lag < len(seg); lag++ {
			var c float64
			for i := 0; i+lag < len(seg); i++ {
				c += seg[i] * seg[i+lag]
			}
			if c > bestCorr {
				bestCorr, bestLag = c, lag
			}
		}
		if bestLag == 0 {
			continue
		}
		freq := float64(sr) / float64(bestLag)
		note := int(math.Round(69 + 12*math.Log2(freq/440.0)))
		if note < 1 || note > 127 {
			continue
		}
		frames[fi] = [4]uint8{uint8(note), 0, 0, 0}
	}
	return frames, nil
}

func decodeMonoS16(path string, sampleRate int) ([]byte, error) {
	if err := ensureFFmpeg(); err != nil {
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

func encodeChipStream(frames [][4]uint8) ([]byte, int) {
	var b bytes.Buffer
	var prev [4]uint8
	for fi, fr := range frames {
		var ctrl byte
		var vals []byte
		for c := 0; c < 4; c++ {
			if fi == 0 || fr[c] != prev[c] {
				ctrl |= 1 << uint(c)
				vals = append(vals, fr[c])
			}
		}
		b.WriteByte(ctrl)
		b.Write(vals)
		prev = fr
	}
	return b.Bytes(), len(frames)
}

func chunkChipStream(stream []byte, maxChunk int) [][]byte {
	var chunks [][]byte
	start, i := 0, 0
	for i < len(stream) {
		recLen := 1 + bits.OnesCount8(stream[i]&0x0F)
		if i+recLen > len(stream) {
			recLen = len(stream) - i
		}
		if i > start && i-start+recLen > maxChunk {
			chunks = append(chunks, stream[start:i])
			start = i
		}
		i += recLen
	}
	if start < len(stream) {
		chunks = append(chunks, stream[start:])
	}
	return chunks
}

func encodeSongChiptune(path string, isMIDI bool) (stream []byte, frames int, err error) {
	var fr [][4]uint8
	if isMIDI {
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, 0, e
		}
		notes, e := parseMIDI(data)
		if e != nil {
			return nil, 0, e
		}
		fr = midiToFrames(notes)
	} else {
		fr, err = audioToFrames(path)
		if err != nil {
			return nil, 0, err
		}
	}
	if len(fr) == 0 {
		return nil, 0, fmt.Errorf("no playable notes produced")
	}
	stream, frames = encodeChipStream(fr)
	return stream, frames, nil
}
