package main

import (
	"strings"
	"testing"
)

func TestBuildChildEnvironment(t *testing.T) {
	got := buildChildEnvironment([]string{
		"PATH=C:\\Windows",
		"pOrT=9999",
		"nOdE_EnV=development",
		"DeepSeek_Api_Key=stale",
		"hostname=untrusted",
		"Default_Model=untrusted",
		"KEEP_ME=unchanged",
	}, "test-only-key")
	if value, ok := environmentValue(got, "PATH"); !ok || value != `C:\Windows` {
		t.Fatalf("PATH not preserved: %q, %v", value, ok)
	}
	if value, ok := environmentValue(got, "KEEP_ME"); !ok || value != "unchanged" {
		t.Fatalf("unrelated environment variable not preserved: %q, %v", value, ok)
	}
	want := []string{"NODE_ENV=production", "HOSTNAME=127.0.0.1", "PORT=3000", "DEFAULT_MODEL=deepseek:deepseek-v4-flash", "DEEPSEEK_API_KEY=test-only-key"}
	if len(got) < len(want) {
		t.Fatalf("child environment unexpectedly short: %v", got)
	}
	for i, expected := range want {
		if got[len(got)-len(want)+i] != expected {
			t.Errorf("child environment suffix[%d]=%q want %q", i, got[len(got)-len(want)+i], expected)
		}
	}
	for _, stale := range []string{"pOrT=9999", "nOdE_EnV=development", "DeepSeek_Api_Key=stale", "hostname=untrusted", "Default_Model=untrusted"} {
		for _, item := range got {
			if item == stale {
				t.Errorf("stale environment value leaked: %s", stale)
			}
		}
	}
}

func TestEnvironmentValue(t *testing.T) {
	got := []string{"PATH=C:\\Windows", "pOrT=9999", "NODE_ENV=production"}
	if value, ok := environmentValue(got, "PORT"); !ok || value != "9999" {
		t.Fatalf("case-insensitive lookup got (%q, %v)", value, ok)
	}
	if environmentNameCount(got, "port") != 1 {
		t.Fatalf("case-insensitive variable count = %d, want 1", environmentNameCount(got, "port"))
	}
	if !strings.Contains(got[0], "C:\\Windows") {
		t.Fatal("test PATH fixture malformed")
	}
}
