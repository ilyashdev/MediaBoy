package gbdk

import (
	"image"
	"path/filepath"

	"MediaBoy/internal/core"
	"MediaBoy/internal/imaging"
)

// ExportImage writes the GBDK sources for a converted full-colour image.
func ExportImage(cfg core.ConvertConfig, full image.Image) error {
	dir, name := cfg.OutputDir, cfg.Name
	var err error
	switch {
	case cfg.Mode == core.ModeDMG:
		tiles, pal := imaging.TileficationDMG(full)
		err = ExportGBDKDMG(dir, name, tiles, pal[0])
	case cfg.HiColor:
		err = ExportGBDKHiColor(dir, name, cfg.GBDKHome, full)
	default:
		tiles, pals := imaging.Tilefication(full)
		err = ExportGBDK(dir, name, tiles, pals)
	}
	if err != nil {
		return err
	}
	_ = GenerateBatchFile(cfg)
	_ = imaging.SaveImage(full, filepath.Join(dir, name+"_preview.jpg"))
	return nil
}
