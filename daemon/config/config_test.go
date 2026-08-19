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
