// Package cli runs MediaBoy's conversions from the command line, without the
// GUI: `MediaBoy <command> [options] FILE...`.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"MediaBoy/internal/core"
	"MediaBoy/internal/deps"
)

// command is one subcommand: its flags are added to fs by setup, which
// returns the function that runs it on the positional arguments.
type command struct {
	name, args, summary string
	setup               func(fs *flag.FlagSet, o *common) func(ctx context.Context, files []string) error
}

var commands = []command{
	{"image", "FILE", "convert a photo (jpg/png) into a GBC ROM", setupImage},
	{"gallery", "FILE...", "bundle several photos into one ROM with ◀▶ switching", setupGallery},
	{"gif", "FILE", "convert an animated GIF into a GBVP3 video ROM", setupGIF},
	{"video", "FILE", "convert a video (any ffmpeg-readable file) into a GBVP3 video ROM", setupVideo},
	{"music", "FILE...", "build a music player ROM from audio, MIDI, .uge or .mod files", setupMusic},
	{"deps", "", "check for GBDK and ffmpeg, and optionally install them", setupDeps},
}

// errUsage reports a wrong command line; the message is followed by the usage.
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

func usagef(format string, a ...any) error { return errUsage{fmt.Sprintf(format, a...)} }

// Run runs a command line (without the program name) and returns the exit
// code: 0 on success, 1 when the conversion failed, 2 on a usage error.
func Run(args []string) int {
	attachConsole()
	if len(args) == 0 || isHelp(args[0]) || args[0] == "help" {
		if len(args) > 1 {
			if c := find(args[1]); c != nil {
				fs, _ := newFlagSet(c, os.Stdout)
				fs.Usage()
				return 0
			}
		}
		usage(os.Stdout)
		return 0
	}
	c := find(args[0])
	if c == nil {
		fmt.Fprintf(os.Stderr, "MediaBoy: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}

	fs, run := newFlagSet(c, os.Stderr)
	files, err := parse(fs, args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2 // flag already printed the error and the usage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = run(ctx, files)
	var ue errUsage
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintf(os.Stderr, "MediaBoy %s: %v\n\n", c.name, err)
		fs.Usage()
		return 2
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(os.Stderr, "\nCancelled.")
		return 1
	default:
		fmt.Fprintf(os.Stderr, "MediaBoy %s: %v\n", c.name, err)
		return 1
	}
}

func isHelp(a string) bool { return a == "-h" || a == "-help" || a == "--help" }

func find(name string) *command {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  MediaBoy                            start the GUI")
	fmt.Fprintln(w, "  MediaBoy <command> [options] FILE…  convert from the command line")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'MediaBoy help <command>' for the command's options.")
}

// newFlagSet defines the command's flags and returns the function that runs it.
func newFlagSet(c *command, w io.Writer) (*flag.FlagSet, func(context.Context, []string) error) {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(w)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: MediaBoy %s [options] %s\n\n%s.\n\nOptions:\n", c.name, c.args, upperFirst(c.summary))
		fs.PrintDefaults()
	}
	return fs, c.setup(fs, &common{})
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// parse parses flags that may come before, between or after the files
// (`MediaBoy image photo.jpg -o roms`); everything after "--" is a file.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var files []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return files, nil
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(files, rest...), nil
		}
		files = append(files, rest[0])
		args = rest[1:]
	}
}

// common holds the options every converting command has.
type common struct {
	out, name, gbdk string
}

func (o *common) register(fs *flag.FlagSet, gbdk bool) {
	fs.StringVar(&o.out, "o", "out", "output `dir`ectory")
	fs.StringVar(&o.name, "name", "", "ROM `name` (default: the input file's name)")
	if gbdk {
		fs.StringVar(&o.gbdk, "gbdk", "", "GBDK-2020 home `dir` (default: found automatically)")
	}
}

// config is the default configuration with the common options applied;
// fallback is the ROM name when -name is not given.
func (o *common) config(fallback string) core.ConvertConfig {
	cfg := core.DefaultConfig()
	cfg.OutputDir = o.out
	cfg.Name = fallback
	if o.name != "" {
		cfg.Name = core.SanitizeName(o.name)
	}
	return cfg
}

// requireTools finds GBDK and ffmpeg (putting them on PATH) and fails when a
// needed one is missing.
func (o *common) requireTools(cfg *core.ConvertConfig, gbdk, ffmpeg bool) error {
	preferred := o.gbdk
	if preferred == "" {
		preferred = savedGBDKHome()
	}
	st := deps.Check(preferred)
	if gbdk {
		if !st.HaveGBDK() {
			if o.gbdk != "" {
				return fmt.Errorf("no GBDK-2020 toolchain in %s", o.gbdk)
			}
			return errors.New("GBDK-2020 not found: pass -gbdk DIR or run 'MediaBoy deps -install local'")
		}
		cfg.GBDKHome = st.GBDKHome
	}
	if ffmpeg && !st.HaveFFmpeg() {
		return errors.New("ffmpeg/ffprobe not found: put them on PATH or run 'MediaBoy deps -install local'")
	}
	return nil
}

// cropFlag is a crop rectangle given as X,Y,W,H in source pixels.
type cropFlag struct{ r image.Rectangle }

func (c *cropFlag) String() string {
	if c.r.Empty() {
		return ""
	}
	return fmt.Sprintf("%d,%d,%d,%d", c.r.Min.X, c.r.Min.Y, c.r.Dx(), c.r.Dy())
}

func (c *cropFlag) Set(v string) error {
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return errors.New("want X,Y,W,H")
	}
	var n [4]int
	for i, p := range parts {
		var err error
		if n[i], err = strconv.Atoi(strings.TrimSpace(p)); err != nil {
			return fmt.Errorf("want X,Y,W,H: %w", err)
		}
	}
	if n[0] < 0 || n[1] < 0 || n[2] <= 0 || n[3] <= 0 {
		return errors.New("X and Y must be ≥ 0, W and H > 0")
	}
	c.r = image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3])
	return nil
}

func (c *cropFlag) apply(cfg *core.ConvertConfig) {
	if !c.r.Empty() {
		cfg.CropRect, cfg.CropEnabled = c.r, true
	}
}

// choice is a flag that takes one of a fixed set of words.
type choice struct {
	value   string
	options []string
}

func newChoice(value string, options ...string) *choice { return &choice{value, options} }

func (c *choice) String() string {
	if c == nil {
		return ""
	}
	return c.value
}

func (c *choice) Set(v string) error {
	for _, o := range c.options {
		if strings.EqualFold(v, o) {
			c.value = o
			return nil
		}
	}
	return fmt.Errorf("want one of: %s", strings.Join(c.options, ", "))
}

func (c *choice) index() int {
	for i, o := range c.options {
		if o == c.value {
			return i
		}
	}
	return 0
}

// isSet reports whether the flag name was given on the command line.
func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func oneFile(files []string) (string, error) {
	switch len(files) {
	case 0:
		return "", usagef("no input file")
	case 1:
		return files[0], nil
	}
	return "", usagef("one input file expected, got %d", len(files))
}

func someFiles(files []string) error {
	if len(files) == 0 {
		return usagef("no input files")
	}
	return nil
}
