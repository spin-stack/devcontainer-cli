package config

import (
	"encoding/json"
	"testing"
)

func TestDevContainerConfig_JSONRoundtrip(t *testing.T) {
	input := `{"image":"ubuntu","features":{"go:1":{}},"postCreateCommand":"echo hi"}`
	var cfg DevContainer
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var cfg2 DevContainer
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if cfg2.Image != "ubuntu" {
		t.Errorf("image = %q after roundtrip", cfg2.Image)
	}
}
