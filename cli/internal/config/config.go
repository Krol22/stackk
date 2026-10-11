// Package config reads the user's stackk settings, ~/.config/stackk/config.json.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

// Save writes the settings, keeping keys this version doesn't know exactly as they are. Saves
// from several stackk processes take turns, and the file is replaced whole, never half-written.
func Save(c Config) error {
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(Path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close() // releases the lock
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	all := map[string]json.RawMessage{}
	data, err := os.ReadFile(Path())
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &all); err != nil {
			return fmt.Errorf("%s: %v (fix or remove it)", Path(), err)
		}
	case !os.IsNotExist(err):
		return err
	}
	if c.Theme == "" {
		delete(all, "theme")
	} else {
		all["theme"], _ = json.Marshal(c.Theme)
	}
	out, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(Path()), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), Path())
}
