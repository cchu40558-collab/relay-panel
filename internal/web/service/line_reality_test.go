package service

import "testing"

func TestRealityDefaultMinimumClientVersion(t *testing.T) {
	config := ensureLineConfigDefaults(8, LineTypeReality, map[string]string{})
	if got := config["realityMinClientVer"]; got != defaultRealityMinClientVersion {
		t.Fatalf("default min client version = %q, want %q", got, defaultRealityMinClientVersion)
	}

	config = map[string]string{"realitySni": "www.example.com"}
	if err := ensureRealityConfig(config); err != nil {
		t.Fatalf("ensureRealityConfig() error = %v", err)
	}
	if got := config["realityMinClientVer"]; got != defaultRealityMinClientVersion {
		t.Fatalf("apply min client version = %q, want %q", got, defaultRealityMinClientVersion)
	}
}

func TestRealityExplicitMinimumClientVersionIsPreserved(t *testing.T) {
	config := ensureLineConfigDefaults(8, LineTypeReality, map[string]string{
		"realityMinClientVer": "1.9.0",
	})
	if got := config["realityMinClientVer"]; got != "1.9.0" {
		t.Fatalf("explicit min client version = %q, want %q", got, "1.9.0")
	}
}
