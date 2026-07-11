package oci

import "testing"

func TestIsLocalRegistry(t *testing.T) {
	tests := []struct {
		registry string
		want     bool
	}{
		{"localhost:5000", true},
		{"127.0.0.1:5000", true},
		{"localhost", true},
		{"ghcr.io", false},
		{"myregistry.azurecr.io", false},
		{"registry.example.com:443", false},
	}
	for _, tt := range tests {
		if got := isLocalRegistry(tt.registry); got != tt.want {
			t.Errorf("isLocalRegistry(%q) = %v, want %v", tt.registry, got, tt.want)
		}
	}
}
