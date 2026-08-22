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

// The hosted server moved and the old host was taken down. An install from
// before the move has it saved, and a saved value beats the default, so it has
// to be rewritten or the app keeps asking a hostname that is gone.
func TestTheRetiredHostedServerIsMovedOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"serverUrl":"https://api.192168.lol","lanDiscovery":false}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := loadSettings(dir, "https://192168.lol")
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if s.ServerURL != "https://192168.lol" {
		t.Errorf("server = %q, want the new default", s.ServerURL)
	}
	// Everything else survives the rewrite.
	if s.LanDiscovery {
		t.Error("the move turned LAN discovery back on")
	}

	// Written down, so it does not have to happen again.
	again, err := loadSettings(dir, "https://elsewhere.example.com")
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if again.ServerURL != "https://192168.lol" {
		t.Errorf("server = %q, want it saved", again.ServerURL)
	}
}

// A self-hosted address is somebody's own choice and is left alone.
func TestASelfHostedServerIsNotMoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"serverUrl":"https://lan.example.com"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := loadSettings(dir, "https://192168.lol")
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if s.ServerURL != "https://lan.example.com" {
		t.Errorf("server = %q, want it left alone", s.ServerURL)
	}
}
