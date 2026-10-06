package ui

import (
	"context"
	"fmt"
	"time"

	"MediaBoy/internal/video"
)

// The Export tab shows how large the ROM gets before compiling. A settings
// change re-encodes the clip in the background (after a pause, cancelled by
// the next change) into an unlimited stream, which gives the cartridge size
// and, for any Max ROM, how much of the clip fits. Everything here except the
// job itself runs on the UI thread.

const estimateDelay = 700 * time.Millisecond

// estimateSem runs one estimate at a time: preparing the frames (ffmpeg)
// cannot be cancelled, so a newer estimate waits for it instead.
var estimateSem = make(chan struct{}, 1)

func (gs *gifEditorState) cancelEstimate() {
	gs.estGen++
	if gs.estTimer != nil {
		gs.estTimer.Stop()
		gs.estTimer = nil
	}
	if gs.estCancel != nil {
		gs.estCancel()
		gs.estCancel = nil
	}
}

// scheduleEstimate drops the shown estimate and starts a new one after a pause.
func (gs *gifEditorState) scheduleEstimate() {
	gs.cancelEstimate()
	gs.estimate, gs.fitEst = nil, nil
	if gs.estimateLbl == nil || len(gs.srcFrames) == 0 {
		gs.refreshEstimate()
		return
	}
	gen := gs.estGen
	gs.estimateLbl.SetText("Estimating the ROM size…")
	gs.estTimer = time.AfterFunc(estimateDelay, func() { uiDo(func() { gs.startEstimate(gen) }) })
}

func (gs *gifEditorState) startEstimate(gen int) {
	if gen != gs.estGen || len(gs.srcFrames) == 0 || gs.busy.Load() {
		return // a compile schedules a new estimate when it is done
	}
	ctx, cancel := context.WithCancel(context.Background())
	gs.estCancel = cancel
	src, cfg, cached, procGen, cache := gs.srcFrames, gs.cfg, gs.procFrames, gs.procGen, gs.palCache
	fps, audio := gs.encodeParams()
	videoMode, videoPath, videoFPS, srcSize := gs.videoMode, gs.videoSrcPath, gs.targetFPS, gs.srcSize
	fit := gs.fitMode()

	goSafe(gs.setStatus, func() {
		estimateSem <- struct{}{}
		defer func() { <-estimateSem }()
		if ctx.Err() != nil {
			return
		}
		frames := cached
		var err error
		if frames == nil {
			frames, err = prepareFrames(videoMode, videoPath, videoFPS, srcSize, src, cfg, func(int, int) {})
			if err == nil {
				gs.keepFrames(frames, procGen)
			}
		}
		var est video.Estimate
		var fitEst video.FitEstimate
		if err == nil {
			if fit {
				fitEst, err = video.EstimateFit(ctx, frames, audio, fps, cfg.MaxVideoMB, cache, nil)
			} else {
				est, err = video.EstimateROM(ctx, frames, audio, fps, cfg.Quality, cache)
			}
		}
		uiDo(func() {
			if gen != gs.estGen {
				return
			}
			gs.estCancel = nil
			if err != nil {
				gs.estimateLbl.SetText("Could not estimate the ROM size: " + err.Error())
				return
			}
			if fit {
				gs.fitEst = &fitEst
			} else {
				gs.estimate = &est
			}
			gs.refreshEstimate()
		})
	})
}

// fitMode is "Fit by lowering the quality": the estimate is then the fit.
func (gs *gifEditorState) fitMode() bool {
	return gs.videoMode && gs.overflow == overflowQuality
}

// refreshEstimate words the estimate for the current Max ROM and overflow
// choice; those need no new encode.
func (gs *gifEditorState) refreshEstimate() {
	if gs.estimateLbl == nil {
		return
	}
	maxMB := gs.cfg.MaxVideoMB
	if maxMB <= 0 || !gs.videoMode {
		maxMB = 8
	}
	if f := gs.fitEst; f != nil && gs.fitMode() {
		if f.Truncated() {
			gs.estimateLbl.SetText(fmt.Sprintf("Even at %d %% quality %d MB holds only %.1f of %.1f s — the rest is dropped.",
				video.MinPercent, maxMB, float64(f.FramesUsed)/f.FPS, f.Seconds()))
		} else {
			gs.estimateLbl.SetText(fmt.Sprintf("ROM ≈ %s → %s cartridge. The whole %.1f s clip fits into %d MB at about %d %% quality.",
				formatBytes(f.Used), formatBytes(len(f.ROM)), f.Seconds(), maxMB, video.PercentFromQuality(f.Quality)))
		}
		return
	}
	est := gs.estimate
	if est == nil || gs.fitMode() {
		if len(gs.srcFrames) == 0 {
			gs.estimateLbl.SetText("Open a " + gs.kind() + " to see the ROM size.")
		}
		return
	}
	sec := est.Seconds()
	var text string
	if est.Fits8MB < est.Frames {
		text = fmt.Sprintf("Needs more than the largest (8 MB) cartridge: it holds %.1f of %.1f s.",
			float64(est.Fits8MB)/est.FPS, sec)
	} else {
		text = fmt.Sprintf("ROM ≈ %s → %s cartridge.", formatBytes(est.Bytes), formatBytes(est.Cart))
	}
	switch n := est.FramesFor(maxMB); {
	case n < 0:
		text += fmt.Sprintf(" The audio alone does not fit into %d MB.", maxMB)
	case n >= est.Frames:
		text += fmt.Sprintf(" The whole %.1f s clip fits into %d MB.", sec, maxMB)
	default:
		if maxMB < 8 || est.Fits8MB == est.Frames {
			text += fmt.Sprintf(" Max ROM %d MB holds %.1f of %.1f s", maxMB, float64(n)/est.FPS, sec)
		}
		text += overflowNotes[gs.overflow]
	}
	gs.estimateLbl.SetText(text)
}

var overflowNotes = map[overflowMode]string{
	overflowCut:     " — the rest is dropped.",
	overflowQuality: " — the quality will be lowered until it fits.",
	overflowTrim:    " — the clip will be trimmed.",
	overflowSplit:   " — it will be split into several ROMs.",
}

// formatBytes shows a size in KB below 1 MB, whole MB for cartridge sizes.
func formatBytes(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)>>10)
}
