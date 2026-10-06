package ui

import (
	"fmt"
	"image"
	"image/gif"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"MediaBoy/internal/core"
	"MediaBoy/internal/ffmpeg"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/safe"
	"MediaBoy/internal/video"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
)

// Threading: gifEditorState fields and widgets are only touched on the UI
// thread. Loaders, playback and the encoder work on copies and report back
// through fyne.Do.

// loadGIF runs on a background goroutine (file dialog callback).
func (gs *gifEditorState) loadGIF(path string) {
	f, err := os.Open(path)
	if err != nil {
		gs.setStatus("Cannot open: " + err.Error())
		return
	}
	defer f.Close()

	m, err := gif.DecodeAll(f)
	if err != nil {
		gs.setStatus("GIF decode error: " + err.Error())
		return
	}
	frames := imaging.GIFToFrames(m)
	if len(frames) == 0 {
		gs.setStatus("GIF has no frames.")
		return
	}
	uiDo(func() {
		gs.setName(path)
		gs.setFrames(frames, image.Point{}, append([]int(nil), m.Delay...), true)
		gs.setStatus(fmt.Sprintf("Loaded: %s — %d frames", filepath.Base(path), len(frames)))
	})
}

func (gs *gifEditorState) invalidateFrames() {
	gs.procFrames = nil
	gs.procGen++
	gs.palCache = video.NewPaletteCache()
}

// scheduleAutoConvert invalidates the preprocessed frames after a setting change.
func (gs *gifEditorState) scheduleAutoConvert() {
	gs.scheduleEstimate() // at once: the estimate's ROM is no longer the one to compile
	gs.autoTimerMu.Lock()
	defer gs.autoTimerMu.Unlock()
	if gs.autoTimer != nil {
		gs.autoTimer.Stop()
	}
	gs.autoTimer = time.AfterFunc(120*time.Millisecond, func() {
		uiDo(func() {
			gs.invalidateFrames()
			if len(gs.srcFrames) > 0 {
				gs.showFrame(gs.currentFrame)
				gs.scheduleEstimate()
			}
		})
	})
}

// processFrames runs the GB pipeline on every frame in parallel.
func processFrames(src []*image.RGBA, cfg core.ConvertConfig, progress func(done, total int)) ([]image.Image, error) {
	n := len(src)
	out := make([]image.Image, n)
	var done int64
	var firstErr atomic.Value
	sem := make(chan struct{}, runtime.NumCPU())
	var wg safe.Group
	for i := range src {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			p, err := imaging.RunGBPipelineFrame(src[i], cfg)
			if err != nil {
				firstErr.Store(err)
				return
			}
			out[i] = p
			progress(int(atomic.AddInt64(&done, 1)), n)
		})
	}
	wg.Wait()
	if e := firstErr.Load(); e != nil {
		return nil, e.(error)
	}
	return out, nil
}

func (gs *gifEditorState) frameDelay(i int) time.Duration {
	delay := 10
	if i < len(gs.delays) && gs.delays[i] > 0 {
		delay = gs.delays[i]
	}
	return time.Duration(max(delay, 2)) * 10 * time.Millisecond
}

func (gs *gifEditorState) startPlay() {
	if len(gs.srcFrames) == 0 || gs.playing {
		return
	}
	gs.playing = true
	stop := make(chan struct{})
	gs.playStop = stop
	gs.playBtn.SetIcon(theme.MediaPauseIcon())

	// setFrames stops playback before replacing frames, so the snapshot stays valid.
	n := len(gs.srcFrames)
	delays := make([]time.Duration, n)
	for i := range delays {
		delays[i] = gs.frameDelay(i)
	}
	i := gs.currentFrame
	goSafe(gs.setStatus, func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(delays[i]):
			}
			i = (i + 1) % n
			next := i
			uiDo(func() {
				if gs.playStop != stop {
					return
				}
				gs.showFrame(next)
				gs.frameSlider.Value = float64(next)
				gs.frameSlider.Refresh()
			})
		}
	})
}

func (gs *gifEditorState) doCompile() {
	if len(gs.srcFrames) == 0 {
		gs.setStatus("Open a " + gs.kind() + " first.")
		return
	}
	src, cfg, cached, gen := gs.srcFrames, gs.cfg, gs.procFrames, gs.procGen
	fps, audio := gs.encodeParams()
	mode := gs.overflow
	if !gs.videoMode {
		mode = overflowCut
	}
	videoMode, videoPath, videoFPS, srcSize := gs.videoMode, gs.videoSrcPath, gs.targetFPS, gs.srcSize
	cache, est, fitEst := gs.palCache, gs.estimate, gs.fitEst
	if cache == nil {
		cache = video.NewPaletteCache()
	}

	gs.cancelEstimate()
	runJob(&gs.busy, gs.setStatus, func() {
		defer uiDo(gs.scheduleEstimate)
		// The encode is deterministic: when the live estimate already made
		// this ROM, the compile only writes it.
		var res video.Result
		quality, ready := cfg.Quality, false
		switch {
		case mode == overflowQuality && fitEst != nil && fitEst.MaxMB == cfg.MaxVideoMB:
			res, quality, ready = fitEst.Result, fitEst.Quality, true
		case mode != overflowQuality && est != nil:
			res, ready = est.ROM(cfg.MaxVideoMB)
		}
		if !ready {
			prog := newProgressDialog(gs.win, "Compiling "+gs.kind())
			defer prog.close()
			frames := cached
			if frames == nil {
				var err error
				pcb := prog.phase(0, 0.4, "Preprocessing")
				if frames, err = prepareFrames(videoMode, videoPath, videoFPS, srcSize, src, cfg, pcb); err != nil {
					gs.setStatus("Frame error: " + err.Error())
					return
				}
				gs.keepFrames(frames, gen)
			}
			if mode == overflowSplit {
				gs.compileParts(cfg, frames, audio, fps, prog)
				return
			}
			var err error
			switch mode {
			case overflowQuality:
				res, quality, err = video.BuildROMFit(frames, audio, fps, cfg.MaxVideoMB, cache, nil, prog.phase(0.4, 1.0, "Fitting"))
			case overflowTrim:
				res, err = video.BuildTrimmed(frames, audio, fps, quality, cfg.MaxVideoMB, prog.fitPhase(0.4, 1.0, false))
			default:
				res, err = video.BuildROM(frames, audio, fps, quality, cfg.MaxVideoMB, cache, prog.phase(0.4, 1.0, "Encoding"))
			}
			if err != nil {
				gs.setStatus("Video error: " + err.Error())
				return
			}
		}
		if err := video.WriteROM(cfg.OutputDir, cfg.Name, res.ROM); err != nil {
			gs.setStatus("Video error: " + err.Error())
			return
		}
		qualityText := fmt.Sprintf("quality %d %%", video.PercentFromQuality(quality))
		if mode == overflowQuality {
			qualityText = "average " + qualityText
		}
		msg := fmt.Sprintf("ROM → %s/%s.gbc  (%s, %d/%d frames, %s)",
			cfg.OutputDir, cfg.Name, formatBytes(len(res.ROM)), res.FramesUsed, res.FramesTotal, qualityText)
		if mode == overflowTrim && res.Truncated() {
			msg += fmt.Sprintf(" — trimmed to %.1f of %.1f s", float64(res.FramesUsed)/fps, float64(res.FramesTotal)/fps)
		}
		gs.setStatus(msg)
		if res.Truncated() && mode != overflowTrim {
			uiDo(func() { gs.warnTruncated(res) })
		}
	})
}

// prepareFrames runs the GB pipeline on the clip. Video previews are scaled
// down, so a video is decoded again with ffmpeg doing the crop.
func prepareFrames(videoMode bool, videoPath string, videoFPS int, srcSize image.Point, src []*image.RGBA, cfg core.ConvertConfig, progress func(done, total int)) ([]image.Image, error) {
	if videoMode {
		return video.ExtractGBFrames(videoPath, videoFPS, cfg, srcSize, progress)
	}
	return processFrames(src, cfg, progress)
}

// keepFrames caches prepared frames unless the settings changed meanwhile.
func (gs *gifEditorState) keepFrames(frames []image.Image, gen int) {
	uiDo(func() {
		if gs.procGen == gen {
			gs.procFrames = frames
		}
	})
}

// compileParts (background goroutine) splits the clip into ROMs that each fit
// Max ROM: name_part1.gbc, name_part2.gbc…, or name.gbc when one is enough.
func (gs *gifEditorState) compileParts(cfg core.ConvertConfig, frames []image.Image, audio []byte, fps float64, prog *progressDialog) {
	parts, err := video.BuildParts(frames, audio, fps, cfg.Quality, cfg.MaxVideoMB, prog.fitPhase(0.4, 1.0, true))
	if err != nil {
		gs.setStatus("Video error: " + err.Error())
		return
	}
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = cfg.Name
		if len(parts) > 1 {
			names[i] = fmt.Sprintf("%s_part%d", cfg.Name, i+1)
		}
		if err := video.WriteROM(cfg.OutputDir, names[i], p.ROM); err != nil {
			gs.setStatus("Video error: " + err.Error())
			return
		}
	}
	pct := video.PercentFromQuality(cfg.Quality)
	if len(parts) == 1 {
		gs.setStatus(fmt.Sprintf("ROM → %s/%s.gbc  (%s, %d frames, quality %d %%)",
			cfg.OutputDir, names[0], formatBytes(len(parts[0].ROM)), parts[0].FramesUsed, pct))
		return
	}
	gs.setStatus(fmt.Sprintf("%d ROMs → %s/%s_part1…%d.gbc  (%d frames, quality %d %%)",
		len(parts), cfg.OutputDir, cfg.Name, len(parts), len(frames), pct))
	msg := fmt.Sprintf("The clip was split into %d ROMs:\n\n", len(parts))
	for i, p := range parts {
		msg += fmt.Sprintf("%s.gbc — %.1f–%.1f s (%d frames, %d banks)\n", names[i],
			float64(p.FirstFrame)/fps, float64(p.FirstFrame+p.FramesUsed)/fps, p.FramesUsed, p.Banks)
	}
	uiDo(func() { dialog.ShowInformation("Split into several ROMs", msg, gs.win) })
}

// longVideoSeconds is the clip length above which the video almost certainly
// won't fit into the ROM and decoding all frames may exhaust memory.
const longVideoSeconds = 3 * 60

// loadVideo runs on a background goroutine (file dialog callback).
func (gs *gifEditorState) loadVideo(path string) {
	var fps int
	uiDoAndWait(func() { fps = gs.targetFPS })
	dur, err := ffmpeg.ProbeDuration(path)
	if err != nil || dur <= longVideoSeconds {
		gs.decodeVideo(path, true, fps)
		return
	}
	msg := fmt.Sprintf(
		"This video is %d:%02d long (more than 3 minutes).\n\n"+
			"It will most likely not fit into the ROM, and decoding all of its frames\n"+
			"may use up the memory and crash the application.\n\n"+
			"Trim the clip first for best results. Continue anyway?",
		int(dur)/60, int(dur)%60)
	uiDo(func() {
		dialog.ShowConfirm("Long video", msg, func(ok bool) {
			if ok {
				goSafe(gs.setStatus, func() { gs.decodeVideo(path, true, fps) })
			} else {
				gs.setStatus("Loading cancelled: video longer than 3 minutes.")
			}
		}, gs.win)
	})
}

// decodeVideo (background goroutine) extracts frames, and for a fresh load
// also audio, at the given frame rate. With fresh=false (frame-rate change)
// the crop, name and audio are kept.
func (gs *gifEditorState) decodeVideo(path string, fresh bool, fps int) {
	if !gs.loading.CompareAndSwap(false, true) {
		gs.setStatus("Still loading the previous video…")
		return
	}
	defer gs.loading.Store(false)

	if fresh {
		if sf, err := ffmpeg.ProbeFPS(path); err == nil && sf > 0 && float64(fps) > sf {
			fps = capFPS(sf)
		}
	}
	prog := newProgressDialog(gs.win, "Loading video")
	defer prog.close()
	prog.set(0, fmt.Sprintf("Extracting frames @ %d fps…", fps))
	frames, srcSize, err := video.ExtractPreview(path, fps, func(done, total int) {
		if total > 0 {
			prog.set(0.9*float64(done)/float64(total), fmt.Sprintf("Decoding frames %d / %d…", done, total))
		}
	})
	if err != nil {
		gs.setStatus("Video error: " + err.Error())
		return
	}
	var audio []byte
	if fresh {
		prog.set(0.95, "Extracting audio…")
		audio = video.ExtractAudio(path)
	}

	uiDo(func() {
		gs.videoSrcPath = path
		gs.targetFPS = fps
		gs.fpsSelect.SetSelected(fmt.Sprintf("%d fps", fps))
		if fresh {
			gs.audioPCM = audio
			gs.setName(path)
		}
		gs.setFrames(frames, srcSize, uniformDelays(len(frames), fps), fresh)
		aud := "no audio"
		if gs.audioPCM != nil {
			aud = fmt.Sprintf("%d KB audio", len(gs.audioPCM)/1024)
		}
		gs.setStatus(fmt.Sprintf("Loaded: %s — %d frames @ %d fps, %s", filepath.Base(path), len(frames), fps, aud))
	})
}
