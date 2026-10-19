package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// config is the small amount of state the CLI keeps between invocations.
//
// Only the selected project is stored. Credentials stay in the environment so
// that backups and synced dotfile directories never contain them.
type config struct {
	ProjectID string `json:"project_id,omitempty"`
}

// configPath honours XDG_CONFIG_HOME, falling back to ~/.config.
func configPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory not found: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "nemau", "config.json"), nil
}

// loadConfig reads the stored configuration. A missing file is not an error:
// it is the normal state of a fresh installation.
func loadConfig() config {
	path, err := configPath()
	if err != nil {
		return config{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return config{}
	}
	var stored config
	if err := json.Unmarshal(raw, &stored); err != nil {
		// A corrupt file must not break the CLI: fall back to no stored state
		// and let `nemau project use` overwrite it.
		return config{}
	}
	return stored
}

// saveConfig writes the configuration, creating the directory if needed.
func saveConfig(stored config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	raw, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	// Write to a temporary file then rename: an interrupted write must not
	// leave a truncated config that the next run silently ignores.
	temp := path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// configExists reports whether a configuration file is present.
func configExists() bool {
	path, err := configPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}
