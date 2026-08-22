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

	// LanDiscovery replicates broadcast and multicast to the group and gives
	// the tunnel the machine's multicast route, so games that find servers by
	// scanning the LAN find the group. On by default: it is the difference
	// between a LAN list with your friends in it and an empty one, which is
	// what people expect the app to do.
	LanDiscovery bool `json:"lanDiscovery"`

	// PacketLog writes a line for every packet worth seeing to a second log.
	// Off by default: it is for working out why a particular session misbehaved
	// and costs a file that turns over quickly, so it is not something to leave
	// running. The counters that end up in the ordinary log run either way.
	PacketLog bool `json:"packetLog"`

	path string
}

// loadSettings reads the settings in dir, falling back to the defaults for
// anything the file does not set, which on first run is all of them.
func loadSettings(dir, defaultServer string) (*settings, error) {
	s := &settings{
		ServerURL:    defaultServer,
		LanDiscovery: true,
		path:         filepath.Join(dir, settingsFile),
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
