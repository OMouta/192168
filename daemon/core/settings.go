package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const settingsFile = "settings.json"

// settings is what the user chose, as opposed to what the server told us. It
// holds no secrets, so unlike the identity it is plain readable JSON.
type settings struct {
	ServerURL string `json:"serverUrl"`

	path string
}

// loadSettings reads the settings in dir, falling back to the default server on
// first run.
func loadSettings(dir, defaultServer string) (*settings, error) {
	s := &settings{
		ServerURL: defaultServer,
		path:      filepath.Join(dir, settingsFile),
	}

	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("core: read settings: %w", err)
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, fmt.Errorf("core: parse settings: %w", err)
	}
	return s, nil
}

func (s *settings) save() error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("core: encode settings: %w", err)
	}
	if err := os.WriteFile(s.path, body, 0o600); err != nil {
		return fmt.Errorf("core: write settings: %w", err)
	}
	return nil
}
