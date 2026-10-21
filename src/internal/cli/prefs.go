package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// savedGBDKHome is the GBDK home the GUI saved in its Fyne preferences (app
// ID and key as in package ui), or "" when there is none.
func savedGBDKHome() string {
	root, err := os.UserConfigDir()
	if runtime.GOOS == "darwin" {
		var home string
		home, err = os.UserHomeDir()
		root = filepath.Join(home, "Library", "Preferences")
	}
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "fyne", "com.mediaboy.app", "preferences.json"))
	if err != nil {
		return ""
	}
	var prefs struct {
		GBDKHome string `json:"gbdkHome"`
	}
	_ = json.Unmarshal(data, &prefs)
	return prefs.GBDKHome
}
