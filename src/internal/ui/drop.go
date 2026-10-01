package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
)

// File types each editor opens; shared by the open dialogs and drag & drop.
var (
	imageExts = []string{"jpg", "jpeg", "png"}
	gifExts   = []string{"gif"}
	videoExts = []string{"mp4", "mov", "mkv", "avi", "webm", "m4v"}
	songExts  = []string{"mp3", "mp4", "wav", "ogg", "m4a", "flac", "mid", "midi", "uge", "mod"}
)

func hasExt(path string, exts []string) bool {
	return slices.Contains(exts, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")))
}

// handleDrop opens files dropped onto the window in the editor that fits
// them, switching to it. Runs on the UI thread; loaders run in the background
// like after a file dialog.
func (s *appState) handleDrop(uris []fyne.URI, setMode func(string)) {
	var paths []string
	for _, u := range uris {
		if u.Scheme() == "file" {
			paths = append(paths, filepath.FromSlash(u.Path()))
		}
	}
	if len(paths) == 0 {
		return
	}

	// In the music editor audio and video become tracks and an image becomes
	// the selected track's cover; anything else opens its own editor.
	if s.mode == "music" && s.music != nil && s.music.handleDrop(paths) {
		return
	}

	var songs []string
	for _, p := range paths {
		if hasExt(p, songExts) && !hasExt(p, videoExts) {
			songs = append(songs, p)
		}
	}
	for _, p := range paths {
		switch {
		case hasExt(p, gifExts):
			setMode("gif")
			go s.gif.loadGIF(p)
		case hasExt(p, videoExts):
			setMode("video")
			go s.video.loadVideo(p)
		case hasExt(p, imageExts):
			setMode("image")
			go s.loadImageFromPath(p)
		case len(songs) > 0:
			setMode("music")
			s.music.handleDrop(songs)
		default:
			continue
		}
		return
	}
	s.statusForMode(fmt.Sprintf("Unsupported file: %s", filepath.Base(paths[0])))
}

// handleDrop adds dropped audio/video files as tracks, or sets a dropped image
// as the selected track's cover. Reports whether it used any of paths.
func (ms *musicState) handleDrop(paths []string) bool {
	used := false
	for _, p := range paths {
		if hasExt(p, songExts) || hasExt(p, videoExts) {
			ms.addSongPath(p)
			used = true
		}
	}
	if used {
		return true
	}
	for _, p := range paths {
		if hasExt(p, imageExts) {
			if ms.selected < 0 {
				ms.setStatus("Select a track first to set its cover.")
				return true
			}
			go ms.setCoverPath(ms.songs[ms.selected], p)
			return true
		}
	}
	return false
}
