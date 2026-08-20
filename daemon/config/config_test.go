package config

import "testing"

func TestValidateServerURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://lan.example.com", false},
		{"https://lan.example.com:8443", false},
		{"http://localhost:8080", false},
		{"http://127.0.0.1:8080", false},
		{"http://lan.example.com", true},
		{"ftp://lan.example.com", true},
		{"not a url", true},
		{"", true},
	}
	for _, tt := range tests {
		err := ValidateServerURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateServerURL(%q) err = %v, wantErr = %v", tt.url, err, tt.wantErr)
		}
	}
}

// The hosted deployment is used until the user points the app elsewhere.
func TestLoadDefaultsToHostedServer(t *testing.T) {
	t.Setenv("NET192168_SERVER_URL", "")
	t.Setenv("NET192168_DATA_DIR", t.TempDir())

	cfg, err := Load(false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServerURL != DefaultServerURL {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, DefaultServerURL)
	}
}

func TestLoadRejectsUnusableServerURL(t *testing.T) {
	t.Setenv("NET192168_SERVER_URL", "http://192168.lol")
	t.Setenv("NET192168_DATA_DIR", t.TempDir())

	if _, err := Load(false); err == nil {
		t.Error("Load: want error for plain-http server URL, got nil")
	}
}
