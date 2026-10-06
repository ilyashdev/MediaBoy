# Third-party components and licenses

MediaBoy's own source code is licensed under the MIT License (see `LICENSE`).
It additionally ports, bundles, or downloads the following third-party work,
each under its own license.

## GBVideoPlayer2 — MIT
Copyright (c) 2015-2019 Lior Halphon (LIJI32)
https://github.com/LIJI32/GBVideoPlayer2

- `src/internal/video/encoder.go` (palettes and colour combinations) is ported
  from GBVideoPlayer2's `encoder.c`, and `gbvp3enc.go` builds on it.
- `player/video3.asm`, the GBVP3 player, is derived from GBVideoPlayer2's
  `video.asm`; `src/internal/video/video3.gbc` is built from it and embedded
  into MediaBoy, which puts it in front of the encoded video data, so every
  video ROM MediaBoy makes contains it.
- These are derivative works distributed under the same MIT terms, with the
  author's permission. GBVideoPlayer2's license:

```
MIT License

Copyright (c) 2015-2019 Lior Halphon

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

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
