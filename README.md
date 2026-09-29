# MediaBoy

<img src="logo.png" alt="MediaBoy" width="96" align="left" />

MediaBoy is a desktop app that turns photos, GIFs, videos and music into real
**Game Boy / Game Boy Color ROMs**. It wraps the [GBDK-2020](https://github.com/gbdk-2020/gbdk-2020)
toolchain for compilation and uses [GBVideoPlayer2](https://github.com/LIJI32/GBVideoPlayer2)
for full-motion video playback on real CGB hardware.

![platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-blue)
![license](https://img.shields.io/badge/license-MIT-green)

## Features

- **Image → ROM** — crop, downscale and convert a photo to a CGB ROM. HiColor
  mode squeezes ~288 colours per frame via per-scanline palette swaps. The same
  ROM also runs on an original (non-colour) Game Boy, falling back to a grayscale
  copy of the picture. Press **START** to print on a Game Boy Printer — the image
  feeds out on screen in step with the print as it happens.
- **Image gallery** — bundle several pictures into one ROM with ◀▶ switching.
  Each image carries its own grayscale copy used both for the original-GB fallback
  and for **START**-to-print on the Game Boy Printer, so any image in the gallery
  can be printed, repeatedly, on both GB and GBC.
- **GIF / Video → ROM** — convert an animated GIF or any `ffmpeg`-readable video
  into a CGB full-motion video ROM using **GBVideoPlayer2**. The encoder is a
  native Go port of GBVideoPlayer2's `encoder.c` (no external encoder needed),
  multithreaded across CPU cores. Includes audio, fps presets (12/15/24/30/60,
  down-sample only), a per-ROM size cap with overflow warnings, and an automatic
  "best quality that fits" search.
- **Music → ROM** — sampled 3-bit PCM or chiptune playback with a cover image.

## Getting the dependencies

MediaBoy needs two external tools: the **GBDK-2020** toolchain (`lcc` + its
bundled SDCC C compiler) and **FFmpeg** (`ffmpeg`/`ffprobe`). No host C
compiler (gcc/clang) is needed to build ROMs — only to build MediaBoy itself
from source.

On startup MediaBoy checks for all of them (saved *GBDK Home*, the local
`deps/` folder, common install locations and `PATH`) and, if something is
missing, offers to install just the missing parts:

- **GBDK** is always installed locally into `deps/` next to the app (or
  `~/.config/MediaBoy/deps` when that folder is not writable). It is never
  installed system-wide.
- **ffmpeg** can be installed either **system-wide** with the OS package
  manager (apt / dnf / pacman / zypper via a password prompt, winget on
  Windows, Homebrew on macOS) or **locally** into `deps/`. An ffmpeg that is
  already on `PATH` is simply used.

The check can be reopened any time with **Dependencies** in the top-right
corner of the window, and turned off at startup from the same dialog.

## Usage

1. Pick a category in the toolbar (Image, GIF, Video, Music) and use its
   **Open…** / **Add Song…** button to load a file.
2. Adjust crop / settings; for video pick the frame rate, max ROM size and
   quality. Settings are grouped into collapsible sections — click a section
   header to fold it (the state is remembered between launches).
3. Click **Compile ROM**. The finished `.gb` / `.gbc` lands in the `out/` folder
   (build intermediates are cleaned up automatically). Use **Open output folder**
   to jump there.
4. Run the ROM in an emulator (BGB, SameBoy, Emulicious, …) or flash it to a
   cartridge.

## Building from source

Requirements: Go 1.26+, a C compiler (CGO is required by Fyne), and the
platform OpenGL/dev headers.

```bash
cd src
go build .      # produces the MediaBoy binary
go run .        # run without building
```

- **Windows**: MinGW-w64 (gcc).
- **Linux**: `sudo apt install gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev libgtk-3-dev`.
- **macOS**: Xcode command-line tools.

Prebuilt binaries for Windows, Linux and macOS are attached to each
[release](../../releases).

## Project layout

```
src/
  main.go                 entry point
  video.gbc               GBVideoPlayer2 player ROM (loaded at runtime)
  internal/
    core/                 shared types, config, color helpers
    imaging/              image ops, dithering, tilemaps, conversion pipeline
    ffmpeg/               ffmpeg/ffprobe wrappers (audio decode, probing)
    deps/                 GBDK + ffmpeg auto-download
    gbdk/                 GBDK C/asset export, compiler driver, vendored libs
    music/                music player ROM export (PCM + chiptune)
    video/                GBVideoPlayer2 encoder and video import
    ui/                   Fyne UI
```

## How video encoding works (short version)

Audio is interleaved as 3-bit stereo PCM, then each frame is fit to 8 CGB
palettes via k-means (the parallel hot path) and packed by GBVideoPlayer2's
combination + per-line diff format. The vendored `video.gbc` player ROM is
concatenated in front of the encoded data and a valid CGB/MBC5 cartridge header
is written. See `src/internal/video/gbvp2enc.go`.

## License

MediaBoy is released under the **MIT License** (see [`LICENSE`](LICENSE)).
It ports GBVideoPlayer2 (MIT) and uses GBDK-2020 (GPLv2 with Linking Exception)
and FFmpeg (LGPL/GPL); full third-party notices are in
[`THIRD_PARTY.md`](THIRD_PARTY.md).

## Credits

- [GBDK-2020](https://github.com/gbdk-2020/gbdk-2020) — Game Boy Development Kit.
- [GBVideoPlayer2](https://github.com/LIJI32/GBVideoPlayer2) by Lior Halphon —
  the full-motion video format and player.
- [FFmpeg](https://ffmpeg.org) — media decoding.
- [Fyne](https://fyne.io) — the GUI toolkit.
