package core

import (
	"path/filepath"
	"strings"
)

// SanitizeName turns name into a project name that is also a C identifier.
func SanitizeName(name string) string {
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "image"
	}
	s := b.String()
	// A C identifier (used for include guards, symbol names, etc.) may not
	// start with a digit, so prefix one with '_'. Without this a name like
	// "222" generates "#ifndef 222_H", which sdcc rejects ("macro names must
	// be identifiers"), skipping the whole header and cascading into
	// undefined-type/macro errors downstream.
	if s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return s
}

// NameFromPath is the project name for a source file: its base name.
func NameFromPath(path string) string {
	return SanitizeName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
}

// CodecForExt is the codec a track gets by default for its file extension.
func CodecForExt(ext string) MusicCodec {
	switch strings.ToLower(ext) {
	case ".uge":
		return CodecChiptuneUGE
	case ".mod":
		return CodecChiptuneMOD
	case ".mid", ".midi":
		return CodecChiptuneAuto
	default:
		return CodecPCM
	}
}
