package config

import "testing"

func TestAdvertisedURLs(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		wantAPI      string
		wantRealtime string
	}{
		{
			name:         "derived from the public URL",
			cfg:          Config{PublicURL: "https://lan.example.com"},
			wantAPI:      "https://lan.example.com/api",
			wantRealtime: "wss://lan.example.com/realtime",
		},
		{
			name:         "local development stays on plain http",
			cfg:          Config{PublicURL: "http://localhost:8080"},
			wantAPI:      "http://localhost:8080/api",
			wantRealtime: "ws://localhost:8080/realtime",
		},
		{
			name: "discovery and API on different hosts",
			cfg: Config{
				PublicURL:        "https://192168.lol",
				APIOverride:      "https://api.192168.lol",
				RealtimeOverride: "wss://api.192168.lol/realtime",
			},
			wantAPI:      "https://api.192168.lol",
			wantRealtime: "wss://api.192168.lol/realtime",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.APIURL(); got != tt.wantAPI {
				t.Errorf("APIURL() = %q, want %q", got, tt.wantAPI)
			}
			if got := tt.cfg.RealtimeURL(); got != tt.wantRealtime {
				t.Errorf("RealtimeURL() = %q, want %q", got, tt.wantRealtime)
			}
		})
	}
}

func TestCheckPublicRejectsUnusableAddresses(t *testing.T) {
	tests := []struct {
		raw     string
		scheme  string
		wantErr bool
	}{
		{"https://192168.lol", "https", false},
		{"http://localhost:8080", "https", false},
		{"http://192168.lol", "https", true},
		{"wss://api.192168.lol/realtime", "wss", false},
		{"ws://api.192168.lol/realtime", "wss", true},
		{"ws://localhost:8080/realtime", "wss", false},
		{"nonsense", "https", true},
	}
	for _, tt := range tests {
		err := checkPublic("VAR", tt.raw, tt.scheme)
		if (err != nil) != tt.wantErr {
			t.Errorf("checkPublic(%q, %q) err = %v, wantErr = %v", tt.raw, tt.scheme, err, tt.wantErr)
		}
	}
}
