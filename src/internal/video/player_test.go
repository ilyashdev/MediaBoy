package video

import (
	"context"
	"fmt"
	"image"
	"math/rand"
	"testing"
)

// playerCPU runs the embedded player's machine code: a CGB CPU in double
// speed with the instructions video3.asm uses, an MBC5, the timer and LY. The
// simulator in sim_test.go follows the stream as the encoder means it; this
// checks that the player really plays it that way.
type playerCPU struct {
	rom                    []byte
	wram                   [0x2000]byte
	hram                   [0x80]byte
	io                     [0x80]byte
	ie, ifl                byte
	bankLo, bankHi         int
	a, b, c, d, e, h, l, f byte
	sp, pc                 uint16
	cyc, div               int64 // M-cycles
	lcdOn                  bool
	lcdStart               int64
	tima, tma              byte
	timerOn                bool
	err                    error
	late                   int // halts entered with the interrupt already pending
	scy                    []scyWrite
}

type scyWrite struct {
	cyc int64
	v   uint8
}

const cpuLine = 228 // M-cycles per scanline in double speed

func (p *playerCPU) ly() uint8 {
	if !p.lcdOn {
		return 0
	}
	t := p.cyc - p.lcdStart
	line := t / cpuLine % 154
	if line == 153 && t%cpuLine >= 2 {
		return 0 // LY reads 0 for most of line 153
	}
	return uint8(line)
}

func (p *playerCPU) rd(a uint16) uint8 {
	switch {
	case a < 0x4000:
		return p.rom[a]
	case a < 0x8000:
		if i := (p.bankHi<<8|p.bankLo)*0x4000 + int(a-0x4000); i < len(p.rom) {
			return p.rom[i]
		}
		return 0xFF
	case a >= 0xC000 && a < 0xE000:
		return p.wram[a-0xC000]
	case a == 0xFFFF:
		return p.ie
	case a >= 0xFF80:
		return p.hram[a-0xFF80]
	case a == 0xFF44:
		return p.ly()
	case a == 0xFF0F:
		return p.ifl
	case a == 0xFF05:
		return p.tima
	case a >= 0xFF00:
		return p.io[a-0xFF00]
	}
	return 0xFF
}

func (p *playerCPU) wr(a uint16, v uint8) {
	switch {
	case a >= 0x2000 && a < 0x3000:
		p.bankLo = int(v)
	case a >= 0x3000 && a < 0x4000:
		p.bankHi = int(v & 1)
	case a < 0x8000:
	case a >= 0xC000 && a < 0xE000:
		p.wram[a-0xC000] = v
	case a == 0xFFFF:
		p.ie = v
	case a >= 0xFF80:
		p.hram[a-0xFF80] = v
	case a == 0xFF0F:
		p.ifl = v
	case a == 0xFF04:
		p.div = 0
	case a == 0xFF05:
		p.tima = v
	case a == 0xFF06:
		p.tma = v
	case a == 0xFF07:
		p.timerOn = v&4 != 0
	case a == 0xFF40:
		if v&0x80 != 0 && !p.lcdOn {
			p.lcdStart = p.cyc
		}
		p.lcdOn = v&0x80 != 0
	case a == 0xFF42:
		p.scy = append(p.scy, scyWrite{p.cyc, v})
	case a >= 0xFF00:
		p.io[a-0xFF00] = v
	}
}

// tick advances n M-cycles; TAC 5 ticks TIMA every 4 of them.
func (p *playerCPU) tick(n int) {
	for range n {
		p.cyc++
		p.div++
		if p.timerOn && p.div%4 == 0 {
			if p.tima++; p.tima == 0 {
				p.tima = p.tma
				p.ifl |= 4
			}
		}
	}
}

func (p *playerCPU) fetch() uint8 { v := p.rd(p.pc); p.pc++; return v }
func (p *playerCPU) fetch16() uint16 {
	lo := uint16(p.fetch())
	return lo | uint16(p.fetch())<<8
}
func (p *playerCPU) hl() uint16     { return uint16(p.h)<<8 | uint16(p.l) }
func (p *playerCPU) setHL(v uint16) { p.h, p.l = uint8(v>>8), uint8(v) }
func (p *playerCPU) de() uint16     { return uint16(p.d)<<8 | uint16(p.e) }
func (p *playerCPU) bc() uint16     { return uint16(p.b)<<8 | uint16(p.c) }
func (p *playerCPU) pop() uint16 {
	v := uint16(p.rd(p.sp)) | uint16(p.rd(p.sp+1))<<8
	p.sp += 2
	return v
}

const flagZ, flagN, flagH, flagC = 0x80, 0x40, 0x20, 0x10

func (p *playerCPU) r8(i uint8) uint8 {
	switch i {
	case 0:
		return p.b
	case 1:
		return p.c
	case 2:
		return p.d
	case 3:
		return p.e
	case 4:
		return p.h
	case 5:
		return p.l
	case 6:
		return p.rd(p.hl())
	}
	return p.a
}

func (p *playerCPU) setR8(i, v uint8) {
	switch i {
	case 0:
		p.b = v
	case 1:
		p.c = v
	case 2:
		p.d = v
	case 3:
		p.e = v
	case 4:
		p.h = v
	case 5:
		p.l = v
	case 6:
		p.wr(p.hl(), v)
	default:
		p.a = v
	}
}

func (p *playerCPU) zero(v uint8) {
	if v == 0 {
		p.f |= flagZ
	}
}

// alu runs add/sub/and/xor/or/cp; false for the ones the player does not use.
func (p *playerCPU) alu(op, v uint8) bool {
	a := p.a
	switch op {
	case 0:
		p.a, p.f = a+v, 0
		if int(a)+int(v) > 0xFF {
			p.f |= flagC
		}
	case 2, 7:
		p.f = flagN
		if v > a {
			p.f |= flagC
		}
		if op == 7 {
			p.zero(a - v)
			return true
		}
		p.a = a - v
	case 4:
		p.a, p.f = a&v, flagH
	case 5:
		p.a, p.f = a^v, 0
	case 6:
		p.a, p.f = a|v, 0
	default:
		return false
	}
	p.zero(p.a)
	return true
}

func (p *playerCPU) cond(cc uint8) bool {
	switch cc {
	case 0:
		return p.f&flagZ == 0
	case 1:
		return p.f&flagZ != 0
	case 2:
		return p.f&flagC == 0
	}
	return p.f&flagC != 0
}

// step runs one instruction and returns its M-cycles, 0 on an opcode the
// player does not use (p.err says which).
func (p *playerCPU) step() int {
	pc := p.pc
	op := p.fetch()
	cost := func(n, hlCost int, r uint8) int {
		if r == 6 {
			return hlCost
		}
		return n
	}
	switch {
	case op == 0x00, op == 0xF3:
		return 1
	case op == 0x10: // stop: the speed switch
		p.fetch()
		return 1
	case op == 0x76: // halt
		if p.ifl&p.ie != 0 {
			p.late++
		}
		for p.ifl&p.ie == 0 {
			p.tick(1)
		}
		return 1
	case op >= 0x40 && op < 0x80:
		dst, src := op>>3&7, op&7
		p.setR8(dst, p.r8(src))
		if dst == 6 || src == 6 {
			return 2
		}
		return 1
	case op&0xC7 == 0x06:
		p.setR8(op>>3&7, p.fetch())
		return cost(2, 3, op>>3&7)
	case op&0xC6 == 0x04: // inc r, dec r
		r := op >> 3 & 7
		v := p.r8(r)
		if op&1 == 0 {
			v++
			p.f &= flagC
		} else {
			v--
			p.f = p.f&flagC | flagN
		}
		p.zero(v)
		p.setR8(r, v)
		return cost(1, 3, r)
	case op >= 0x80 && op < 0xC0:
		if p.alu(op>>3&7, p.r8(op&7)) {
			return cost(1, 2, op&7)
		}
	case op&0xC7 == 0xC6:
		if p.alu(op>>3&7, p.fetch()) {
			return 2
		}
	}
	switch op {
	case 0x01:
		v := p.fetch16()
		p.b, p.c = uint8(v>>8), uint8(v)
		return 3
	case 0x11:
		v := p.fetch16()
		p.d, p.e = uint8(v>>8), uint8(v)
		return 3
	case 0x21:
		p.setHL(p.fetch16())
		return 3
	case 0x31:
		p.sp = p.fetch16()
		return 3
	case 0x02:
		p.wr(p.bc(), p.a)
		return 2
	case 0x12:
		p.wr(p.de(), p.a)
		return 2
	case 0x1A:
		p.a = p.rd(p.de())
		return 2
	case 0x22:
		p.wr(p.hl(), p.a)
		p.setHL(p.hl() + 1)
		return 2
	case 0x2A:
		p.a = p.rd(p.hl())
		p.setHL(p.hl() + 1)
		return 2
	case 0x13:
		v := p.de() + 1
		p.d, p.e = uint8(v>>8), uint8(v)
		return 2
	case 0x3B:
		p.sp--
		return 2
	case 0x07: // rlca
		c := p.a >> 7
		p.a, p.f = p.a<<1|c, c<<4
		return 1
	case 0x1F: // rra
		c := p.a & 1
		p.a, p.f = p.a>>1|(p.f&flagC)<<3, c<<4
		return 1
	case 0x08:
		a := p.fetch16()
		p.wr(a, uint8(p.sp))
		p.wr(a+1, uint8(p.sp>>8))
		return 5
	case 0x18:
		d := int8(p.fetch())
		p.pc = uint16(int(p.pc) + int(d))
		return 3
	case 0x20, 0x28, 0x30, 0x38:
		d := int8(p.fetch())
		if p.cond(op >> 3 & 3) {
			p.pc = uint16(int(p.pc) + int(d))
			return 3
		}
		return 2
	case 0xC3:
		p.pc = p.fetch16()
		return 4
	case 0xC2, 0xCA, 0xD2, 0xDA:
		a := p.fetch16()
		if p.cond(op >> 3 & 3) {
			p.pc = a
			return 4
		}
		return 3
	case 0xE9:
		p.pc = p.hl()
		return 1
	case 0xCD:
		a := p.fetch16()
		p.sp -= 2
		p.wr(p.sp, uint8(p.pc))
		p.wr(p.sp+1, uint8(p.pc>>8))
		p.pc = a
		return 6
	case 0xC9:
		p.pc = p.pop()
		return 4
	case 0xD1:
		v := p.pop()
		p.d, p.e = uint8(v>>8), uint8(v)
		return 3
	case 0xE0:
		p.wr(0xFF00|uint16(p.fetch()), p.a)
		return 3
	case 0xF0:
		p.a = p.rd(0xFF00 | uint16(p.fetch()))
		return 3
	case 0xE2:
		p.wr(0xFF00|uint16(p.c), p.a)
		return 2
	case 0xEA:
		p.wr(p.fetch16(), p.a)
		return 4
	case 0xFA:
		p.a = p.rd(p.fetch16())
		return 4
	case 0xF9:
		p.sp = p.hl()
		return 2
	case 0xF8: // ld hl, sp + e (only the carry is used)
		d := int8(p.fetch())
		p.f = 0
		if int(p.sp&0xFF)+int(uint8(d)) > 0xFF {
			p.f |= flagC
		}
		p.setHL(uint16(int(p.sp) + int(d)))
		return 3
	case 0xCB:
		cb := p.fetch()
		r := cb & 7
		switch {
		case cb&0xF8 == 0x28: // sra
			v := p.r8(r)
			p.f = (v & 1) << 4
			v = uint8(int8(v) >> 1)
			p.zero(v)
			p.setR8(r, v)
			return cost(2, 4, r)
		case cb >= 0x40 && cb < 0x80: // bit
			p.f = p.f&flagC | flagH
			if p.r8(r)&(1<<(cb>>3&7)) == 0 {
				p.f |= flagZ
			}
			return cost(2, 3, r)
		}
	}
	p.err = fmt.Errorf("opcode %#02x at %#04x, bank %#x, LY %d", op, pc, p.bankHi<<8|p.bankLo, p.ly())
	return 0
}

// runPlayer boots rom and runs it for gbFrames GB frames.
func runPlayer(rom []byte, gbFrames int) (*playerCPU, error) {
	p := &playerCPU{rom: rom, pc: 0x100, sp: 0xFFFE, bankLo: 1, lcdOn: true}
	for limit := int64(cpuLine * 154 * gbFrames); p.cyc < limit; {
		n := p.step()
		if p.err != nil {
			return p, p.err
		}
		p.tick(n)
	}
	return p, nil
}

// checkPlayer runs the player on rom and checks that it shows what the
// simulator shows: every line of every video frame, each written at the
// same point of its scanline, with no line running into the next.
func checkPlayer(t testing.TB, name string, rom []byte, sim []simFrame) {
	t.Helper()
	e := &encoder{}
	e.buildCombinations()
	var shown []int
	for i, f := range sim {
		for range f.count {
			shown = append(shown, i)
		}
	}
	p, err := runPlayer(rom, 2*len(shown)+8)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if p.late != 0 {
		t.Fatalf("%s: %d lines ran past their scanline", name, p.late)
	}
	// The SCY writes by GB frame and line, from the first GB frame that shows
	// a line.
	type line struct{ frame, ly int64 }
	writes := map[line][]uint8{}
	start := map[int64]bool{}
	first := int64(-1)
	for _, w := range p.scy {
		t0 := w.cyc - p.lcdStart
		k := line{t0 / (cpuLine * 154), t0 / cpuLine % 154}
		if k.ly >= 144 {
			continue
		}
		if first < 0 {
			first = k.frame
		}
		if len(writes[k]) == 0 {
			start[t0%cpuLine] = true
		}
		writes[k] = append(writes[k], w.v)
	}
	if len(start) != 1 {
		t.Fatalf("%s: lines start at different points of the scanline: %v", name, start)
	}
	for u, fi := range shown {
		f := &sim[fi]
		for field := range 2 {
			for ly := range 144 {
				ws := writes[line{first + int64(2*u+field), int64(ly)}]
				if len(ws) != 20 {
					t.Fatalf("%s: video frame %d field %d line %d: %d SCY writes", name, u, field, ly, len(ws))
				}
				for j, v := range ws {
					comb := int(v + uint8(ly))
					for x := range 8 {
						if e.combos[comb*8+x] != f.fields[field][ly][j*8+x] {
							t.Fatalf("%s: video frame %d field %d line %d strip %d differs from the simulator", name, u, field, ly, j)
						}
					}
				}
			}
		}
	}
}

// TestHighBanks plays a stream past bank $100, where the bank number's low
// byte repeats the MBC1 holes ($11F, $120, ...) but there are none: the
// player once skipped field B from $11F to $121 there and crashed.
func TestHighBanks(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var frames []image.Image
	for range 120 {
		frames = append(frames, noiseFrame(rng))
	}
	// Silence that fills the audio up to bank $FD (210 chunks a bank, $20,
	// $40 and $60 skipped; the video bank is a byte), so the video starts at
	// $FE.
	pcm := make([]byte, (0xFD-3)*210*gbvp3FrameSamples*2)
	for i := range pcm {
		pcm[i] = 128
	}
	res, err := BuildROM(context.Background(), frames, pcm, 30, 0, 8, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated() {
		t.Fatalf("truncated")
	}
	start, end := int(res.ROM[0x4000]), res.Used/0x4000
	if start != 0xFE || end <= 0x121 {
		t.Fatalf("video in banks %#x-%#x, want across $11F-$121", start, end)
	}
	sim, _ := checkROM(t, "highbanks", res, 8)
	checkPlayer(t, "highbanks", res.ROM, sim)
}
