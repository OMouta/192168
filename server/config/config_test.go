package config

import "testing"

func TestAdvertisedURLs(t *testing.T) {
	tests := []struct {
		name         string
		publicURL    string
		wantAPI      string
		wantRealtime string
	}{
		{
			name:         "hosted deployment",
			publicURL:    "https://api.192168.lol",
			wantAPI:      "https://api.192168.lol/api",
			wantRealtime: "wss://api.192168.lol/realtime",
		},
		{
			name:         "self-hosted",
			publicURL:    "https://lan.example.com",
			wantAPI:      "https://lan.example.com/api",
			wantRealtime: "wss://lan.example.com/realtime",
		},
		{
			name:         "local development stays on plain http",
			publicURL:    "http://localhost:8080",
			wantAPI:      "http://localhost:8080/api",
			wantRealtime: "ws://localhost:8080/realtime",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{PublicURL: tt.publicURL}
			if got := c.APIURL(); got != tt.wantAPI {
				t.Errorf("APIURL() = %q, want %q", got, tt.wantAPI)
			}
			if got := c.RealtimeURL(); got != tt.wantRealtime {
				t.Errorf("RealtimeURL() = %q, want %q", got, tt.wantRealtime)
			}
		})
	}
}

func TestCheckPublicURLRejectsUnusableAddresses(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr bool
	}{
		{"https://api.192168.lol", false},
		{"https://lan.example.com:8443", false},
		{"http://localhost:8080", false},
		{"http://127.0.0.1:8080", false},
		{"http://lan.example.com", true},
		{"ftp://lan.example.com", true},
		{"nonsense", true},
	}
	for _, tt := range tests {
		if err := checkPublicURL(tt.raw); (err != nil) != tt.wantErr {
			t.Errorf("checkPublicURL(%q) err = %v, wantErr = %v", tt.raw, err, tt.wantErr)
		}
	}
}

func TestListenAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
		port string
		want string
	}{
		{"nothing set", "", "", ":8080"},
		{"our own variable", ":9000", "", ":9000"},
		// Railway and similar hosts hand the port over this way, and a
		// deployment that ignores it never becomes reachable.
		{"the host supplies a port", "", "4567", ":4567"},
		{"ours wins", ":9000", "4567", ":9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NET192168_ADDR", tt.addr)
			t.Setenv("PORT", tt.port)
			if got := listenAddress(); got != tt.want {
				t.Errorf("listenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
