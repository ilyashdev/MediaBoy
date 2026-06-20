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
  mode squeezes ~288 colours per frame via per-scanline palette swaps. Press
  **START** on the device to print the picture on a Game Boy Printer (a printer
  icon shows while printing, a ✕ if none is connected).
- **Image gallery** — bundle several pictures into one ROM with ◀▶ switching and
  per-image GB Printer output.
- **GIF / Video → ROM** — convert an animated GIF or any `ffmpeg`-readable video
  into a CGB full-motion video ROM using **GBVideoPlayer2**. The encoder is a
  native Go port of GBVideoPlayer2's `encoder.c` (no external encoder needed),
  multithreaded across CPU cores. Includes audio, fps presets (12/15/24/30/60,
  down-sample only), a per-ROM size cap with overflow warnings, and an automatic
  "best quality that fits" search.
- **Music → ROM** — sampled 3-bit PCM or chiptune playback with a cover image.

## Getting the dependencies

MediaBoy needs two external tools: the **GBDK-2020** compiler (`lcc`) and
**FFmpeg** (`ffmpeg`/`ffprobe`). You don't have to install them by hand:

> In the **GB / GBC** settings tab, click **"Download GBDK + ffmpeg"**.

It downloads the correct build for your platform into a local `deps/` folder,
points *GBDK Home* at it and adds FFmpeg to the app's `PATH`. On the next launch
they are picked up automatically. You can also set *GBDK Home* manually if you
already have GBDK installed.

## Usage

1. Open an image, GIF, video or music file from the toolbar.
2. Adjust crop / settings; for video pick fps, max ROM size and quality.
3. Click **Compile ROM**. The finished `.gb` / `.gbc` lands in the `out/` folder
   (build intermediates are cleaned up automatically). Use **Open output folder**
   to jump there.
4. Run the ROM in an emulator (BGB, SameBoy, Emulicious, …) or flash it to a
   cartridge.

## Building from source

Requirements: Go 1.21+, a C compiler (CGO is required by Fyne), and the
platform OpenGL/dev headers.

```bash
cd src
go build .      # produces the MediaBoy binary
go run .        # run without building
```

- **Windows**: MinGW-w64 (gcc).
- **Linux**: `sudo apt install gcc libgl1-mesa-dev xorg-dev`.
- **macOS**: Xcode command-line tools.

Prebuilt binaries for Windows, Linux and macOS are attached to each
[release](../../releases).

## How video encoding works (short version)

Audio is interleaved as 3-bit stereo PCM, then each frame is fit to 8 CGB
palettes via k-means (the parallel hot path) and packed by GBVideoPlayer2's
combination + per-line diff format. The vendored `video.gbc` player ROM is
concatenated in front of the encoded data and a valid CGB/MBC5 cartridge header
is written. See `src/gbvp2enc.go`.

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
