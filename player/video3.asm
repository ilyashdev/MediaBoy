; GBVP3 video player. The stream format is described in
; src/internal/video/gbvp3enc.go and gbvp3audio.go. The built ROM is checked in
; as src/internal/video/video3.gbc and embedded into MediaBoy; to rebuild it
; with rgbds 1.0:
;
;   rgbasm -o video3.o video3.asm
;   rgblink -o ../src/internal/video/video3.gbc video3.o
;
; The encoder relies on the addresses of RunStub, ReturnStub, Patches and
; RunReturn (TestPlayerROM checks them).
;
; Derived from GBVideoPlayer2's (GBVP2) video.asm by Lior Halphon (MIT). The
; CPU speed switch, the timer sync, the per-scanline prologue and the 20 SCY
; writes are GBVP2's, cycle for cycle: GBVP2's released video.gbc was built by
; an older rgbasm that put a nop after every halt and turned "ld [rDIV], a"
; into ldh, so both are written out here. Cycle counts below are M-cycles (double speed: 228 per
; scanline) from the timer wake-up; GBVP2's worst line halts at 221.
;
; What changed: every line of both fields has its own 20-byte slot of SCY
; values in WRAM, patched in place against what it showed in the previous
; frame, so the per-line pass that re-based GBVP2's single line buffer is gone.
; Audio is mono, 4 bits per sample, unpacked in VBlank through two tables.

DEF rDIV  EQU $FF04
DEF rTIMA EQU $FF05
DEF rTMA  EQU $FF06
DEF rTAC  EQU $FF07
DEF rIF   EQU $FF0F
DEF rNR12 EQU $FF12
DEF rNR22 EQU $FF17
DEF rNR30 EQU $FF1A
DEF rNR42 EQU $FF21
DEF rNR50 EQU $FF24
DEF rNR51 EQU $FF25
DEF rNR52 EQU $FF26
DEF rLCDC EQU $FF40
DEF rSCY  EQU $FF42
DEF rSCX  EQU $FF43
DEF rLY   EQU $FF44
DEF rKEY1 EQU $FF4D
DEF rVBK  EQU $FF4F
DEF rBGPD EQU $FF69
DEF rIE   EQU $FFFF

DEF MBC5_bank_low  EQU $2000
DEF MBC5_bank_high EQU $3000

DEF CHUNK_BYTES EQU 78 ; audio bytes per GB frame (156 4-bit samples)

; Line slots: 12 per 256-byte page, 20 bytes each. Field A is lines 0-143,
; field B lines 144-287 from page $CC. WRAM bank 1 is never switched.
DEF line_slots    EQU $C000
DEF field_b_slots EQU $CC00
DEF slots_end     EQU $D800
; NR50 value for every LY of the current GB frame.
DEF play_buffer   EQU $D800

; HRAM
DEF current_line   EQU $FF94 ; video SP while VBlank reads audio
DEF current_bank   EQU $FF96 ; video bank: low byte (in VBlank), high byte
DEF frame_repeat   EQU $FFA0
DEF repeat_bank    EQU $FFA1 ; where a repeated frame starts again
DEF repeat_line    EQU $FFA3
DEF audio_bank     EQU $FFA5 ; audio stays below bank 256
DEF audio_address  EQU $FFA6
DEF run_return     EQU $FFA8 ; stream position after a run op
DEF compression_jr EQU $FFFD ; "jr n" to the op's code

DEF OP_SAME   EQU ((9 - 0) * 3 + 7) << 1 ; patch op without patches
DEF OP_RETURN EQU 8
DEF PALETTE_SAME EQU $80 ; count bit: no colours follow, keep the last ones

SECTION "Player", ROM0[0]
; Ops other than raw and bank are "jr n" offsets from compression_jr, which
; wraps from $FFFF to here; n < 64 (op = n << 1 must stay positive for sra).
RunStub::            ; op 2
    jp Run
ReturnStub::         ; op 8
    jp Return

; Patch op: up to 9 (SCY value, slot address low byte) pairs, h is the slot's
; page. op = ((9 - n) * 3 + 7) << 1
Patches::
REPT 9
    pop de
    ld l, d
    ld [hl], e
ENDR
    ld l, a          ; back to the start of the slot

WaitForInterrupt::
    halt
    nop
    xor a
    ldh [rIF], a

    ; Update PCM sample (GBVP2 used h and l here; the cycles are the same)
    ld d, HIGH(play_buffer)
    ldh a, [rLY]
    ld e, a
    cp 143
    ld a, [de]
    ldh [rNR50], a
    nop              ; GBVP2: ld l, h

    ; Render the line from its slot by modifying SCY
REPT 20
    ld a, [hli]
    ldh [c], a
ENDR
    jr z, _VBlank

    ; hl is past the slot just shown: the next one, or the gap at the end of a
    ; page (240 + 16 = 256)
    ld a, l
    add 16
    jr nc, Main
    ld l, a
    inc h

; Decode the next line's op. On entry: c is rSCY, b the bank, sp the stream,
; hl the slot of the line rendered next.
Main::
    pop de
    dec sp
    ; e: $00 raw, $01 next bank then another op, else a jr offset << 1
    sra e
    jr nz, Compressed
    jr nc, Raw

    inc b
    ld a, b
    ld [MBC5_bank_low], a
    ld sp, $4000
    pop de
    dec sp
    sra e
Compressed::
    ld a, e
    ldh [compression_jr + 1], a
    ld a, l
    jp compression_jr

    ; patch n: halts at 130 + 6n (147 + 6n after a bank switch); run: 155
    ; (172); return: 173 + 6n. Every op +1 when the render pointer changed page.

Raw::
REPT 10
    pop de
    ld a, e
    ld [hli], a
    ld a, d
    ld [hli], a
ENDR
    ld a, l
    sub 20
    ld l, a
    jp WaitForInterrupt
    ; raw: halts at 217

_VBlank::
    jp VBlank

; Run op: this line and the next k-1 are unchanged. The op's word is where in
; RunTable those lines' "no change" ops start, so they cost nothing in the
; stream; RunTable ends with a return op that goes back to it. On entry a = l.
Run::
    ld c, a
    pop de
    ld [run_return], sp
    ld a, h
    ld h, d
    ld l, e
    ld sp, hl
    ld h, a
    ld l, c
    ld c, LOW(rSCY)
    jp WaitForInterrupt

; Return op: back to the stream, which holds this line's own op (a patch op
; with at most 7 patches). On entry a = l.
Return::
    ld c, a
    ld d, h
    ld hl, run_return
    ld a, [hli]
    ld h, [hl]
    ld l, a
    ld sp, hl
    ld h, d
    ld l, c
    ld c, LOW(rSCY)
    pop de
    dec sp
    sra e
    jp Compressed

    ds $100 - @
_Start::
    di
    jp Start

    ds $150 - @
Start::
    ; Increase the CPU speed from 4MHz to 8MHz
    ld a, 1
    ldh [rKEY1], a
    stop

    ; Init the stack for the initialization routines
    ld sp, $fffe

    call InitAPU
    call LCDOff
    call LoadGraphics
    call CreateMap
    call CreateAttributeMap
    jp StartPlayback

WaitVBlank::
    ldh a, [rLY]
    cp 144
    jr nz, WaitVBlank
    ret

InitAPU::
    ; Reset the APU
    xor a
    ldh [rNR52], a
    ld a, $80
    ldh [rNR52], a
    ; Turn all DACs on
    ldh [rNR12], a
    ldh [rNR22], a
    ldh [rNR30], a
    ldh [rNR42], a
    ; Put all channels on both left and right
    ld a, $FF
    ldh [rNR51], a
    ret

LCDOff::
    call WaitVBlank
    ldh a, [rLCDC]
    and $7F
    ldh [rLCDC], a
    ret

LoadGraphics::
    ld de, $8000
    ld b, PixelStructureEnd - PixelStructure
    ld hl, PixelStructure
.loop
    ld a, [hli]
    ld [de], a
    inc de
    dec b
    jr nz, .loop
    ret

CreateMap::
    ld hl, $9800
    ld c, 32
    xor a
.loopY
    ld b, 32
.loopX
    ld [hli], a
    dec b
    jr nz, .loopX
    inc a
    and 3
    dec c
    jr nz, .loopY
    ret

CreateAttributeMap::
    ld a, 1
    ldh [rVBK], a
    ld hl, $9800
    ld a, 7
    ld c, 32
.loopY
    ld b, 32
.loopX
    ld [hli], a
    dec b
    jr nz, .loopX
    dec c
    ld a, c
    dec a
    rra
    rra
    and 7
    jr nz, .loopY
    ret

PixelStructure::
    db %00000000, %00000000, %00011111, %00000000, %00000000, %00011111, %00011111, %00011111
    db %11100000, %00000000, %11111111, %00000000, %11100000, %00011111, %11111111, %00011111
    db %00000000, %11100000, %00011111, %11100000, %00000000, %11111111, %00011111, %11111111
    db %11100000, %11100000, %11111111, %11100000, %11100000, %11111111, %11111111, %11111111
    db %00110011, %00001111, %00000111, %00000000, %00000000, %00000111, %00000111, %00000111
    db %11111000, %00000000, %11001100, %00001111, %11111000, %00000111, %11111111, %00000111
    db %00000000, %11111000, %00000111, %11111000, %00110011, %11110000, %00000111, %11111111
    db %11111000, %11111000, %11111111, %11111000, %11111000, %11111111, %11001100, %11110000
PixelStructureEnd::

; Shared by start and restart. Must not call: sp is the video stream on restart.
MACRO INIT_PLAYBACK
    ; Every line slot and the play buffer start as zeros
    ld hl, line_slots
    xor a
    ld c, HIGH(slots_end - line_slots) + 1 ; whole pages, play buffer included
    ld b, 0
.clear\@
    ld [hli], a
    dec b
    jr nz, .clear\@
    dec c
    jr nz, .clear\@

    ; Set SCX to 4. FrameStart will see it at LY 144 and start a video frame.
    ld a, 4
    ldh [rSCX], a
    ld a, $18
    ldh [compression_jr], a

    xor a
    ldh [current_bank + 1], a
    ldh [repeat_bank + 1], a
    ld [MBC5_bank_high], a
    ; Audio starts at 1:4001
    inc a
    ldh [audio_bank], a
    ldh [audio_address], a
    ldh [frame_repeat], a ; 1: FrameStart reads a new frame
    ld [MBC5_bank_low], a
    ld a, $40
    ldh [audio_address + 1], a

    ld c, LOW(rSCY) ; c is rSCY when entering Main
    ; Audio comes first, the first byte at ROM1 is the first video bank
    ld a, [$4000]
    ld b, a
    ld [MBC5_bank_low], a
    ld sp, $4000
ENDM

StartPlayback:
    ; Enable timer interrupt
    ld a, 4
    ldh [rIE], a
    INIT_PLAYBACK

    ; Enable LCD
    ldh a, [rLCDC]
    or $80
    ldh [rLCDC], a

.lyloop
    ldh a, [rLY]
    cp 143
    jr nz, .lyloop

    ; Exactly 2 NOPs are needed to sync properly on both CGB-D and newer and CGB-C and older
    nop
    nop
    ; Configure timer
    ldh [rDIV], a ; Synchronize DIV
    ld a, $100-(912 / 16) ; Configure modulo so we tick every 912 clocks (The length of a scanline in double speed mode)
    ldh [rTMA], a
    ld a, $c8 ; Configure the initial value of TIMA so we overflow at the right timing
    ldh [rTIMA], a
    ld a, 5 ; Enable, tick every 16 CPU clocks
    ldh [rTAC], a
    ; Clear interrupts
    xor a
    ldh [rIF], a
    jp VBlank

RestartPlayback::
    ; Clear palette
    ld hl, rBGPD
    xor a
REPT 64
    ld [hl], a
ENDR
    INIT_PLAYBACK

    ; The timer keeps running and ticks some cycles into each line: once LY
    ; reads 144, the next tick is still LY 144's (as in GBVP2).
.lyloop
    ldh a, [rLY]
    cp 144
    jr nz, .lyloop
    xor a
    ldh [rIF], a
    jp VBlank

; NR50 for each 4-bit sample: level k (0-14) is l = ceil(k/2), r = floor(k/2),
; which the speaker sums. 15 never occurs; it plays the middle level.
MACRO NR50_TABLE ; shift
FOR X, 256
    DEF K = (X >> \1) & 15
    IF K == 15
        DEF K = 7
    ENDC
    db ((K + 1) / 2) << 4 | (K / 2)
ENDR
ENDM

    ASSERT RunStub == 0 && ReturnStub == 3 && Patches == 6

; "No change" ops for the lines of a run, then the way back to the stream.
SECTION "Run table", ROM0
RunTable::
    ds 142, OP_SAME
RunReturn::
    db OP_RETURN

SECTION "NR50 tables", ROM0, ALIGN[8]
NR50Low::
    NR50_TABLE 0
NR50High::
    NR50_TABLE 4

SECTION "VBlank", ROM0, ALIGN[8]
VBlankJumpTable::
    dw FrameStart    ; 144
    dw SecondPalette ; 145
    dw UnpackPCM6    ; 146
    dw UnpackPCM6    ; 147
    dw UnpackPCM6    ; 148
    dw UnpackPCM6    ; 149
    dw UnpackPCM6    ; 150
    dw UnpackPCM6    ; 151
    dw UnpackPCM3    ; 152
    ; During line 153 LY almost always reads 0, so it's handled outside of this table

VBlank::
    halt
    nop
    xor a
    ldh [rIF], a

    ; Update PCM sample
    ld d, HIGH(play_buffer)
    ldh a, [rLY]
    ld e, a
    cp 143 ; Result not used, only here to match the cycle count in the main loop
    ld a, [de]
    ldh [rNR50], a

    ld a, e
    sub 144
    jr c, ReturnToMain
    add a
    ld l, a
    ld h, HIGH(VBlankJumpTable)
    ld a, [hli]
    ld h, [hl]
    ld l, a
    jp hl
    ; 33 cycles to here

; LY 153 (reads 0): back to the video, then decode the first line of the next
; field. Halts by 200 (raw).
ReturnToMain::
    ; The next audio chunk must not cross the bank
    ld hl, sp + CHUNK_BYTES - 1
    ld a, h
    cp $80
    jr nz, .noAudioBankSwitch
    ldh a, [audio_bank]
    inc a
    ldh [audio_bank], a
    ld sp, $4000
.noAudioBankSwitch
    ld [audio_address], sp

    ldh a, [current_bank]
    ld b, a
    ld [MBC5_bank_low], a
    ldh a, [current_bank + 1]
    ld [MBC5_bank_high], a
    ld hl, current_line
    ld a, [hli]
    ld h, [hl]
    ld l, a
    ld sp, hl
    ld c, LOW(rSCY)

    ; SCX 0: field A comes next
    ldh a, [rSCX]
    and a
    ld hl, line_slots
    jp z, Main
    ld h, HIGH(field_b_slots)
    jp Main

; LY 144: after the second field, the next video frame: this one again, or a
; new count; then the first half of the palette unless the count has
; PALETTE_SAME. Halts by 219.
FrameStart::
    ldh a, [rSCX]
    and a
    jp z, VBlank ; A video frame is 2 GB frames
    ; frame_repeat: shows left in bits 0-6, PALETTE_SAME in bit 7
    ldh a, [frame_repeat]
    dec a
    ld e, a
    and $7F
    jr nz, .repeat
    ld a, b
    inc a
    jr nz, .bankNotFF
    ; A frame must not start at bank $xFF
    ld [MBC5_bank_low], a
    ld b, a
    inc a
    ld [MBC5_bank_high], a
    ldh [current_bank + 1], a
    ldh [repeat_bank + 1], a ; the high byte changes only here
    ld sp, $4000
.bankNotFF
    pop de
    dec sp
    ld a, e
    and a
    jp z, RestartPlayback
    ldh [frame_repeat], a
    ld [repeat_line], sp
    ld a, b
    ldh [repeat_bank], a
.palette
    bit 7, e
    jp nz, VBlank
    ; The frame header never crosses a bank
    ld hl, rBGPD
REPT 16
    pop de
    ld [hl], d
    ld [hl], e
ENDR
    jp VBlank
.repeat
    ld a, e
    ldh [frame_repeat], a
    ld hl, repeat_bank
    ld a, [hli]
    ld [MBC5_bank_low], a
    ld b, a
    ld a, [hli]
    ld [MBC5_bank_high], a
    ldh [current_bank + 1], a
    ld a, [hli]
    ld h, [hl]
    ld l, a
    ld sp, hl
    jr .palette

; LY 145: flip the field; on a new frame the second half of the palette. Then
; park the video stream and point sp at this GB frame's audio. Halts by 199.
SecondPalette::
    ldh a, [rSCX]
    xor 4
    ldh [rSCX], a
    jr nz, .audio ; second field next
    ldh a, [frame_repeat]
    bit 7, a
    jr nz, .audio
    ld hl, rBGPD
REPT 16
    pop de
    ld [hl], d
    ld [hl], e
ENDR
.audio
    ld [current_line], sp
    ld a, b
    ldh [current_bank], a
    ldh a, [audio_bank]
    ld [MBC5_bank_low], a
    xor a
    ld [MBC5_bank_high], a
    ld c, a ; next play_buffer entry
    ld hl, audio_address
    ld a, [hli]
    ld h, [hl]
    ld l, a
    ld sp, hl
    jp VBlank

; Unpack 4 samples from the byte pair (e, d): low(e), high(e), high(d), low(d)
MACRO UNPACK4
    pop de
    ld l, e
    ld a, [hl]
    ld [bc], a
    inc c
    inc h
    ld a, [hl]
    ld [bc], a
    inc c
    ld l, d
    ld a, [hl]
    ld [bc], a
    inc c
    dec h
    ld a, [hl]
    ld [bc], a
    inc c
ENDM

; LY 146-151: 24 samples each. Halts at 203.
UnpackPCM6::
    ld b, HIGH(play_buffer)
    ld h, HIGH(NR50Low)
REPT 6
    UNPACK4
ENDR
    jp VBlank

; LY 152: the last 12 samples of the 156.
UnpackPCM3::
    ld b, HIGH(play_buffer)
    ld h, HIGH(NR50Low)
REPT 3
    UNPACK4
ENDR
    jp VBlank
