# Third-party components and licenses

MediaBoy's own source code is licensed under the MIT License (see `LICENSE`).
It additionally ports, bundles, or downloads the following third-party work,
each under its own license.

## GBVideoPlayer2 — MIT
Copyright (c) Lior Halphon (LIJI32)
https://github.com/LIJI32/GBVideoPlayer2

- `src/internal/video/gbvp2enc.go` is a faithful port of GBVideoPlayer2's `encoder.c` and is
  therefore a derivative work distributed under the same MIT terms.
- `src/video.gbc` is a compiled GBVideoPlayer2 player ROM, shipped so MediaBoy
  can concatenate it in front of the encoded video data.

## GBDK-2020 — GPLv2 with Linking Exception
Copyright (c) the GBDK-2020 contributors
https://github.com/gbdk-2020/gbdk-2020

- MediaBoy invokes the GBDK-2020 toolchain (`lcc`, which wraps SDCC) as an
  external program to compile Game Boy ROMs. The toolchain is **not** bundled;
  it is downloaded on demand from the official GBDK-2020 releases.
- `src/internal/gbdk/gbdklib/gbprinter.c`, `gbprinter.h`, `gbc_hicolor.c`, `gbc_hicolor.h`
  are vendored from the GBDK-2020 examples and remain under GBDK-2020's
  GPLv2-with-Linking-Exception license. They are emitted as C source for the
  user's own GBDK build; they are not linked into the MediaBoy binary.
- GBDK-2020 states that ROMs compiled with it require no attribution.

## FFmpeg — LGPL/GPL (downloaded, not bundled)
https://ffmpeg.org

- MediaBoy uses `ffmpeg`/`ffprobe` to decode video and audio. They are **not**
  bundled; the Dependencies dialog installs it with the system package manager or fetches a prebuilt binary for the
  current platform from:
  - Windows: https://www.gyan.dev/ffmpeg/builds/ (gyan.dev)
  - Linux:   https://johnvansickle.com/ffmpeg/ (static builds)
  - macOS:   https://evermeet.cx/ffmpeg/ (evermeet.cx)
- FFmpeg is licensed under the LGPL/GPL; consult ffmpeg.org for the terms of the
  specific build you download.

## Fyne — BSD-3-Clause
Copyright (c) Fyne.io developers
https://github.com/fyne-io/fyne

## sqweek/dialog — see upstream
Native open/save file dialogs. Refer to the upstream repository for its license.
https://github.com/sqweek/dialog

## ulikunitz/xz — BSD 3-Clause
Copyright (c) Ulrich Kunitz
https://github.com/ulikunitz/xz

- Pure-Go xz decoder, compiled into MediaBoy to unpack the Linux ffmpeg
  `.tar.xz` archive without needing the system `xz`/`tar` tools.
