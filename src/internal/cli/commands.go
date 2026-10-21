package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/gif"
	"os"
	"path/filepath"
	"strings"

	"MediaBoy/internal/core"
	"MediaBoy/internal/deps"
	"MediaBoy/internal/ffmpeg"
	"MediaBoy/internal/gbdk"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/music"
	"MediaBoy/internal/video"
)

// Option words, in the order of the core/video/music constants they map to.
var (
	ditherWords   = []string{"none", "bayer", "floyd-steinberg", "atkinson"} // core.Dithering*
	scalingWords  = []string{"median", "bilinear", "nearest"}                // core.Scaling*
	videoFitWords = []string{"trim", "quality", "split"}
	musicFitWords = []string{"trim", "rate", "split"} // music.Overflow*
)

const (
	videoFitTrim = iota
	videoFitQuality
	videoFitSplit
)

// imageOpts are the picture filters of the image and gallery commands.
type imageOpts struct {
	crop      cropFlag
	dither    *choice
	strength  float64
	bilateral bool
	sharpen   float64
	scaling   *choice
}

func (opts *imageOpts) register(fs *flag.FlagSet, crop bool) {
	def := core.DefaultConfig()
	if crop {
		fs.Var(&opts.crop, "crop", "crop `X,Y,W,H` in source pixels (default: centred, the screen's shape)")
	}
	opts.dither = newChoice(ditherWords[def.Dithering], ditherWords...)
	fs.Var(opts.dither, "dither", "dithering `type`: "+strings.Join(ditherWords, ", "))
	fs.Float64Var(&opts.strength, "dither-strength", def.DitheringStrength, "dithering strength, 0–1")
	fs.BoolVar(&opts.bilateral, "bilateral", def.BilateralEnabled, "smooth with a bilateral filter")
	fs.Float64Var(&opts.sharpen, "sharpen", def.SharpenAmount, "sharpening amount, 0 turns it off")
	opts.scaling = newChoice(scalingWords[def.Scaling], scalingWords...)
	fs.Var(opts.scaling, "scaling", "downscaling `method`: "+strings.Join(scalingWords, ", "))
}

func (opts *imageOpts) apply(cfg *core.ConvertConfig) error {
	if opts.strength < 0 || opts.strength > 1 {
		return usagef("-dither-strength must be between 0 and 1")
	}
	if opts.sharpen < 0 {
		return usagef("-sharpen must not be negative")
	}
	opts.crop.apply(cfg)
	cfg.Dithering = core.DitheringType(opts.dither.index())
	cfg.DitheringEnabled = cfg.Dithering != core.DitheringNone
	cfg.DitheringStrength = opts.strength
	cfg.BilateralEnabled = opts.bilateral
	cfg.SharpenAmount = opts.sharpen
	cfg.SharpenEnabled = opts.sharpen > 0
	cfg.Scaling = core.ScalingType(opts.scaling.index())
	cfg.Mode, cfg.HiColor = core.ModeCGB, true
	return nil
}

func setupImage(fs *flag.FlagSet, o *common) func(context.Context, []string) error {
	o.register(fs, true)
	var opts imageOpts
	opts.register(fs, true)
	return func(_ context.Context, files []string) error {
		path, err := oneFile(files)
		if err != nil {
			return err
		}
		cfg := o.config(core.NameFromPath(path))
		if err := opts.apply(&cfg); err != nil {
			return err
		}
		if err := o.requireTools(&cfg, true, false); err != nil {
			return err
		}
		img, err := imaging.DecodeImageFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Converting…")
		result, err := imaging.RunGBPipeline(img, cfg, &imaging.BilCache{})
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Exporting…")
		if err := gbdk.ExportImage(cfg, result.FullColor); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Compiling…")
		return compiled(gbdk.CompileGB(cfg))
	}
}

func setupGallery(fs *flag.FlagSet, o *common) func(context.Context, []string) error {
	o.register(fs, true)
	var opts imageOpts
	opts.register(fs, false)
	return func(_ context.Context, files []string) error {
		if err := someFiles(files); err != nil {
			return err
		}
		cfg := o.config("gallery")
		if err := opts.apply(&cfg); err != nil {
			return err
		}
		if err := o.requireTools(&cfg, true, false); err != nil {
			return err
		}
		images := make([]image.Image, len(files))
		for i, path := range files {
			fmt.Fprintf(os.Stderr, "Converting %d / %d: %s\n", i+1, len(files), filepath.Base(path))
			img, err := imaging.DecodeImageFile(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			result, err := imaging.RunGBPipeline(img, cfg, &imaging.BilCache{})
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			images[i] = result.FullColor
		}
		fmt.Fprintln(os.Stderr, "Exporting gallery…")
		banks, err := gbdk.ExportGBDKImageGallery(cfg, images)
		if err != nil {
			return err
		}
		cfg.ROMBanks = banks
		fmt.Fprintln(os.Stderr, "Compiling gallery ROM…")
		return compiled(gbdk.CompileGB(cfg))
	}
}

// compiled reports a GBDK compile: the ROM on success, the compiler's log
// otherwise.
func compiled(res gbdk.CompileResult) error {
	if !res.Success {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(res.Output))
		return errors.New("compile failed")
	}
	fmt.Println(res.ROMPath)
	return nil
}

// clipOpts are the options of the gif and video commands.
type clipOpts struct {
	crop      cropFlag
	letterbox bool
	scaling   *choice
	quality   int
	maxMB     int
}

func (co *clipOpts) register(fs *flag.FlagSet) {
	def := core.DefaultConfig()
	fs.Var(&co.crop, "crop", "crop `X,Y,W,H` in source pixels (default: centred, the screen's shape)")
	fs.BoolVar(&co.letterbox, "letterbox", false, "fit the whole frame (or crop) on screen with black bars")
	co.scaling = newChoice(scalingWords[def.Scaling], scalingWords...)
	fs.Var(co.scaling, "scaling", "downscaling `method`: "+strings.Join(scalingWords, ", "))
	fs.IntVar(&co.quality, "quality", video.PercentFromQuality(def.Quality),
		fmt.Sprintf("quality in percent, %d–100", video.MinPercent))
	fs.IntVar(&co.maxMB, "max-mb", def.MaxVideoMB, "maximum ROM size in MB, 1–8")
}

// config is the editor's configuration for a clip: no picture filters.
func (co *clipOpts) config(o *common, fallback string) (core.ConvertConfig, error) {
	cfg := o.config(fallback)
	if co.quality < video.MinPercent || co.quality > 100 {
		return cfg, usagef("-quality must be between %d and 100", video.MinPercent)
	}
	if co.maxMB < 1 || co.maxMB > 8 {
		return cfg, usagef("-max-mb must be between 1 and 8")
	}
	cfg.Mode = core.ModeCGB
	cfg.BilateralEnabled = false
	cfg.SharpenEnabled = false
	cfg.PosterizeEnabled = false
	cfg.DitheringEnabled = false
	co.crop.apply(&cfg)
	cfg.Letterbox = co.letterbox
	cfg.Scaling = core.ScalingType(co.scaling.index())
	cfg.Quality = video.QualityFromPercent(co.quality)
	cfg.MaxVideoMB = co.maxMB
	return cfg, nil
}

func setupGIF(fs *flag.FlagSet, o *common) func(context.Context, []string) error {
	o.register(fs, false)
	var co clipOpts
	co.register(fs)
	return func(ctx context.Context, files []string) error {
		path, err := oneFile(files)
		if err != nil {
			return err
		}
		cfg, err := co.config(o, core.NameFromPath(path))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		m, err := gif.DecodeAll(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("GIF decode error: %w", err)
		}
		src := imaging.GIFToFrames(m)
		if len(src) == 0 {
			return errors.New("the GIF has no frames")
		}
		fps := video.GIFFPS(m.Delay)
		fmt.Fprintf(os.Stderr, "Loaded %s: %d frames @ %.1f fps\n", filepath.Base(path), len(src), fps)

		var mt meter
		defer mt.end()
		frames, err := video.ProcessFrames(ctx, src, cfg, mt.frames("Preprocessing frames"))
		if err != nil {
			return err
		}
		// A GIF has no audio: the frames that fit.
		res, err := video.BuildTrimmed(ctx, frames, nil, fps, cfg.Quality, cfg.MaxVideoMB, mt.fit(false))
		if err != nil {
			return err
		}
		mt.end()
		return wroteClip(cfg, res, fps, cfg.Quality, videoFitTrim)
	}
}

func setupVideo(fs *flag.FlagSet, o *common) func(context.Context, []string) error {
	o.register(fs, false)
	var co clipOpts
	co.register(fs)
	fps := fs.Int("fps", 24, "frame rate (lowered to the source's when that is lower)")
	fit := newChoice(videoFitWords[videoFitTrim], videoFitWords...)
	fs.Var(fit, "fit", "`mode` when the clip does not fit -max-mb: trim (shorten it), "+
		"quality (lower the quality until it fits), split (several ROMs: name_part1, name_part2…)")
	return func(ctx context.Context, files []string) error {
		path, err := oneFile(files)
		if err != nil {
			return err
		}
		cfg, err := co.config(o, core.NameFromPath(path))
		if err != nil {
			return err
		}
		if *fps < 1 || *fps > 60 {
			return usagef("-fps must be between 1 and 60")
		}
		if err := o.requireTools(&cfg, false, true); err != nil {
			return err
		}
		if !isSet(fs, "fps") {
			if sf, err := ffmpeg.ProbeFPS(path); err == nil && sf > 0 && float64(*fps) > sf {
				*fps = video.CapFPS(sf)
			}
		}
		size, err := ffmpeg.ProbeFrameSize(path)
		if err != nil {
			return err
		}

		var mt meter
		defer mt.end()
		frames, err := video.ExtractGBFrames(ctx, path, *fps, cfg, size, mt.frames("Decoding frames"))
		if err != nil {
			return err
		}
		mt.say("Extracting audio…")
		audio := video.ExtractAudio(path)
		mt.say(fmt.Sprintf("%d frames @ %d fps, %d KB audio", len(frames), *fps, len(audio)/1024))

		rate := float64(*fps)
		quality := cfg.Quality
		var res video.Result
		switch fit.index() {
		case videoFitSplit:
			parts, err := video.BuildParts(ctx, frames, audio, rate, quality, cfg.MaxVideoMB, mt.fit(true))
			if err != nil {
				return err
			}
			mt.end()
			return wroteParts(cfg, parts, rate)
		case videoFitQuality:
			res, quality, err = video.BuildROMFit(ctx, frames, audio, rate, cfg.MaxVideoMB, video.NewPaletteCache(), nil, mt.frames("Fitting frames"))
		default:
			res, err = video.BuildTrimmed(ctx, frames, audio, rate, quality, cfg.MaxVideoMB, mt.fit(false))
		}
		if err != nil {
			return err
		}
		mt.end()
		return wroteClip(cfg, res, rate, quality, fit.index())
	}
}

// romPath is where video.WriteROM puts the ROM name.
func romPath(dir, name string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, name+".gbc")
}

// wroteClip writes a video ROM and reports it.
func wroteClip(cfg core.ConvertConfig, res video.Result, fps float64, quality, fit int) error {
	if err := video.WriteROM(cfg.OutputDir, cfg.Name, res.ROM); err != nil {
		return err
	}
	qualityText := fmt.Sprintf("quality %d %%", video.PercentFromQuality(quality))
	if fit == videoFitQuality {
		qualityText = "average " + qualityText
	}
	fmt.Fprintf(os.Stderr, "%s, %d/%d frames, %s\n", formatBytes(len(res.ROM)), res.FramesUsed, res.FramesTotal, qualityText)
	if res.Truncated() {
		fmt.Fprintf(os.Stderr, "Trimmed to %.1f of %.1f s to fit %d MB.\n",
			float64(res.FramesUsed)/fps, float64(res.FramesTotal)/fps, cfg.MaxVideoMB)
	}
	fmt.Println(romPath(cfg.OutputDir, cfg.Name))
	return nil
}

// wroteParts writes the ROMs of a split clip: name_part1.gbc, name_part2.gbc…,
// or name.gbc when one is enough.
func wroteParts(cfg core.ConvertConfig, parts []video.Part, fps float64) error {
	for i, p := range parts {
		name := cfg.Name
		if len(parts) > 1 {
			name = fmt.Sprintf("%s_part%d", cfg.Name, i+1)
		}
		if err := video.WriteROM(cfg.OutputDir, name, p.ROM); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s.gbc — %.1f–%.1f s (%d frames, %s)\n", name,
			float64(p.FirstFrame)/fps, float64(p.FirstFrame+p.FramesUsed)/fps, p.FramesUsed, formatBytes(len(p.ROM)))
		fmt.Println(romPath(cfg.OutputDir, name))
	}
	return nil
}

func formatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func setupMusic(fs *flag.FlagSet, o *common) func(context.Context, []string) error {
	o.register(fs, true)
	target := newChoice("gbc", "gbc", "gb")
	fs.Var(target, "target", "`target`: gbc (colour, up to 16384 Hz, GBC only) or gb (4 shades, up to 8192 Hz, any Game Boy)")
	rate := fs.Int("rate", 0, fmt.Sprintf("PCM sample rate in Hz: %s for gbc, %s for gb (default: the highest)",
		joinInts(music.PCMRatesCGB), joinInts(music.PCMRatesDMG)))
	dpcm := fs.Bool("dpcm", true, "store PCM as 4-bit DPCM, half the size (false: 8-bit, a little clearer)")
	maxMB := fs.Int("max-mb", 8, "maximum ROM size in MB, 1–8")
	fit := newChoice(musicFitWords[music.OverflowTrim], musicFitWords...)
	fs.Var(fit, "fit", "`mode` when the music does not fit -max-mb: trim (cut the playlist), "+
		"rate (lower the sample rate until it fits), split (several ROMs: name_part1, name_part2…)")
	cover := fs.String("cover", "", "cover `image` shown for every track")
	chiptune := fs.Bool("chiptune", false, "turn audio tracks into chiptune instead of PCM")
	return func(_ context.Context, files []string) error {
		if err := someFiles(files); err != nil {
			return err
		}
		name := "music"
		if len(files) == 1 {
			name = core.NameFromPath(files[0])
		}
		cfg := o.config(name)
		dmg := target.value == "gb"
		cfg.HiColor = false
		cfg.Mode, cfg.MusicTarget = core.ModeCGB, core.TargetCGBFast
		rates := music.PCMRatesCGB
		if dmg {
			cfg.Mode, cfg.MusicTarget = core.ModeDMG, core.TargetDMG
			rates = music.PCMRatesDMG
		}
		cfg.PCMRate = music.DefaultPCMRate(dmg)
		if *rate != 0 {
			if !containsInt(rates, *rate) {
				return usagef("-rate for %s must be one of %s", target.value, joinInts(rates))
			}
			cfg.PCMRate = *rate
		}
		if *maxMB < 1 || *maxMB > 8 {
			return usagef("-max-mb must be between 1 and 8")
		}
		cfg.MusicUncompressed = !*dpcm
		cfg.MaxMusicMB = *maxMB
		cfg.MusicOverflow = fit.index()
		// Only audio is decoded with ffmpeg; MIDI and tracker files are not.
		needFFmpeg := false
		for _, path := range files {
			ext := strings.ToLower(filepath.Ext(path))
			needFFmpeg = needFFmpeg || (ext != ".mid" && ext != ".midi" && ext != ".uge" && ext != ".mod")
		}
		if err := o.requireTools(&cfg, true, needFFmpeg); err != nil {
			return err
		}

		var coverImg image.Image
		if *cover != "" {
			var err error
			if coverImg, err = imaging.DecodeImageFile(*cover); err != nil {
				return fmt.Errorf("cover: %w", err)
			}
		}
		songs := make([]*core.Song, len(files))
		for i, path := range files {
			if _, err := os.Stat(path); err != nil {
				return err
			}
			sg := &core.Song{
				Path:      path,
				Title:     strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
				Codec:     core.CodecForExt(filepath.Ext(path)),
				CoverPath: *cover,
				Cover:     coverImg,
			}
			if *chiptune && sg.Codec == core.CodecPCM {
				sg.Codec = core.CodecChiptuneAuto
			}
			if d, err := ffmpeg.ProbeDuration(path); err == nil {
				sg.Seconds = d
			}
			songs[i] = sg
		}

		var mt meter
		defer mt.end()
		res, err := music.Build(cfg, songs, mt.say, mt.frames("Compiling"))
		mt.end()
		if err != nil {
			return err
		}
		if !res.Success {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(res.Output))
			return errors.New("compile failed")
		}
		for _, n := range res.Notes {
			fmt.Fprintln(os.Stderr, "Note: "+n)
		}
		for _, rom := range res.ROMs {
			fmt.Println(rom)
		}
		return nil
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func joinInts(s []int) string {
	words := make([]string, len(s))
	for i, v := range s {
		words[i] = fmt.Sprint(v)
	}
	return strings.Join(words, ", ")
}

func setupDeps(fs *flag.FlagSet, o *common) func(context.Context, []string) error {
	fs.StringVar(&o.gbdk, "gbdk", "", "GBDK-2020 home `dir` to check first")
	install := fs.String("install", "", "`where` to install what is missing: local (into the deps folder) or "+
		"system (ffmpeg with the OS package manager; GBDK is always local)")
	return func(_ context.Context, files []string) error {
		if len(files) > 0 {
			return usagef("deps takes no files")
		}
		if *install != "" && *install != "local" && *install != "system" {
			return usagef("-install must be local or system")
		}
		preferred := o.gbdk
		if preferred == "" {
			preferred = savedGBDKHome()
		}
		st := deps.Check(preferred)
		printDeps(st)
		if *install == "" {
			if !st.Complete() {
				return errors.New("missing tools: run 'MediaBoy deps -install local' (or system)")
			}
			return nil
		}
		if st.Complete() {
			fmt.Fprintln(os.Stderr, "Nothing to install.")
			return nil
		}

		var mt meter
		defer mt.end()
		status := func(msg string) { mt.say(msg) }
		var err error
		if *install == "local" {
			fmt.Fprintln(os.Stderr, "Installing into "+deps.LocalDir()+"…")
			st, err = deps.InstallLocal(st, status)
		} else {
			plan, ok := deps.SystemPlan(st)
			for _, p := range plan {
				fmt.Fprintln(os.Stderr, "  "+p)
			}
			if !ok {
				return errors.New("no supported package manager: use -install local")
			}
			st, err = deps.InstallSystem(st, status)
		}
		mt.end()
		if errors.Is(err, deps.ErrRestartRequired) {
			fmt.Fprintln(os.Stderr, "ffmpeg was installed; open a new terminal so it is on PATH.")
			return nil
		}
		if err != nil {
			return err
		}
		printDeps(st)
		return nil
	}
}

func printDeps(st deps.Status) {
	row := func(name, path string) {
		if path == "" {
			path = "not found"
		}
		fmt.Printf("%-8s %s\n", name, path)
	}
	row("GBDK", st.GBDKHome)
	row("ffmpeg", st.FFmpeg)
	row("ffprobe", st.FFprobe)
}
