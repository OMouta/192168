package core

import (
	"os"
	"path/filepath"
	"testing"
)

// LAN discovery is on unless somebody turned it off, and the settings file
// written before it existed says nothing about it. A missing key decodes as
// false, so the default has to be in place before the file is read or every
// existing install would silently lose the feature.
func TestLanDiscoveryDefaultsOn(t *testing.T) {
	tests := []struct {
		name string
		file string
		want bool
	}{
		{name: "first run, no file at all", want: true},
		{name: "written before the setting existed", file: `{"serverUrl":"https://api.192168.lol"}`, want: true},
		{name: "turned off", file: `{"lanDiscovery":false}`, want: false},
		{name: "turned back on", file: `{"lanDiscovery":true}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte(tt.file), 0o600); err != nil {
					t.Fatalf("write settings: %v", err)
				}
			}

			s, err := loadSettings(dir, "https://api.192168.lol")
			if err != nil {
				t.Fatalf("loadSettings: %v", err)
			}
			if s.LanDiscovery != tt.want {
				t.Errorf("LanDiscovery = %t, want %t", s.LanDiscovery, tt.want)
			}
		})
	}
}
