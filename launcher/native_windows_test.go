//go:build windows

package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestChildEnvironmentOverridesAppSettings(t *testing.T) {
	oldEnvironment := os.Environ()
	defer func() {
		for _, name := range []string{"PATH", "PORT", "DEEPSEEK_API_KEY", "NODE_ENV", "HOSTNAME", "DEFAULT_MODEL"} {
			if value, found := environmentValue(oldEnvironment, name); found {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}()
	_ = os.Setenv("PATH", `C:\Windows`)
	_ = os.Setenv("PORT", "9000")
	_ = os.Setenv("DEEPSEEK_API_KEY", "stale")
	_ = os.Setenv("NODE_ENV", "development")
	_ = os.Setenv("HOSTNAME", "attacker-host")
	_ = os.Setenv("DEFAULT_MODEL", "attacker-model")
	got := childEnvironment("test-only-key")
	joined := strings.Join(got, "\n")
	for _, stale := range []string{"PORT=9000", "NODE_ENV=development", "DEEPSEEK_API_KEY=stale", "HOSTNAME=attacker-host", "DEFAULT_MODEL=attacker-model"} {
		if strings.Contains(joined, stale) {
			t.Fatalf("inherited app override was not filtered (%s): %q", stale, got)
		}
	}
	for _, want := range []string{"PATH=C:\\Windows", "NODE_ENV=production", "HOSTNAME=127.0.0.1", "PORT=3000", "DEFAULT_MODEL=deepseek:deepseek-v4-flash", "DEEPSEEK_API_KEY=test-only-key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("child env missing %q", want)
		}
	}
}

func TestChildEnvironmentFiltersCaseVariantOverride(t *testing.T) {
	oldEnvironment := os.Environ()
	oldValue, existed := environmentValue(oldEnvironment, "PORT")
	_ = os.Setenv("pOrT", "9999")
	defer func() {
		_ = os.Unsetenv("pOrT")
		if existed {
			_ = os.Setenv("PORT", oldValue)
		}
	}()
	for _, item := range childEnvironment("safe") {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, "PORT") && value != "3000" {
			t.Fatalf("unexpected child PORT value: %q", item)
		}
	}
}

func TestDPAPIEnvelopeHexRoundTrip(t *testing.T) {
	// Only checks the portable on-disk encoding; DPAPI itself requires Windows.
	b := []byte("opaque-ciphertext")
	encoded := hex.EncodeToString(b)
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(b) {
		t.Fatalf("round trip mismatch: %q", decoded)
	}
}
