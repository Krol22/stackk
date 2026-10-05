// Package config reads the user's stackk settings, ~/.config/stackk/config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	// Canvas is a command run in the space the dashboard doesn't use (e.g. an ebook reader).
	Canvas string `json:"canvas"`
}

func Path() string {
	if p := os.Getenv("STACKK_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "stackk", "config.json")
}

// Load returns the settings; a missing file means defaults.
func Load() (Config, error) {
	var c Config
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			err = nil
		}
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}

func Save(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path(), append(data, '\n'), 0o644)
}
