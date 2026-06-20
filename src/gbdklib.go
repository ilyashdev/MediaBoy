package main

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed gbdklib/gbc_hicolor.c gbdklib/gbc_hicolor.h gbdklib/gbprinter.c gbdklib/gbprinter.h
var gbdkLibFS embed.FS

func writeVendoredLibs(dir string) error {
	return writeLibs(dir, "gbc_hicolor.c", "gbc_hicolor.h", "gbprinter.c", "gbprinter.h")
}

func writePrinterLib(dir string) error {
	return writeLibs(dir, "gbprinter.c", "gbprinter.h")
}

func writeLibs(dir string, files ...string) error {
	for _, f := range files {
		b, err := gbdkLibFS.ReadFile("gbdklib/" + f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0644); err != nil {
			return err
		}
	}
	return nil
}
