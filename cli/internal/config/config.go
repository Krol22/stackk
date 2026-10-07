// Package config reads the user's stackk settings, ~/.config/stackk/config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	// Theme is the dashboard's color theme (stackk theme lists them).
	Theme string `json:"theme,omitempty"`
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

// Save writes the settings, keeping keys this version doesn't know.
func Save(c Config) error {
	all := map[string]any{}
	if data, err := os.ReadFile(Path()); err == nil {
		json.Unmarshal(data, &all)
	}
	mine, err := json.Marshal(c)
	if err != nil {
		return err
	}
	var fields map[string]any
	json.Unmarshal(mine, &fields)
	for k, v := range fields {
		all[k] = v
	}
	if c.Theme == "" {
		delete(all, "theme")
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path(), append(data, '\n'), 0o644)
}
