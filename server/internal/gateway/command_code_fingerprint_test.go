package gateway

import (
	"regexp"
	"testing"
	"time"
)

func TestCommandCodeGenerateFingerprintDeterministic(t *testing.T) {
	apiKey := "cc-test-api-key-12345"
	salt := "custom-salt"

	fp1 := ccGenerateFingerprint(apiKey, salt)
	fp2 := ccGenerateFingerprint(apiKey, salt)

	if fp1.Thumbmark == "" {
		t.Fatal("expected non-empty thumbmark")
	}
	if fp1.Thumbmark != fp2.Thumbmark {
		t.Fatalf("expected deterministic thumbmark, got %s vs %s", fp1.Thumbmark, fp2.Thumbmark)
	}

	platform := fp1.Components["platform"].(string)
	if platform != "win32" {
		t.Fatalf("expected platform win32, got %s", platform)
	}
	arch := fp1.Components["arch"].(string)
	if arch != "x64" {
		t.Fatalf("expected arch x64, got %s", arch)
	}
	if fp1.Components["collectorVersion"].(int) != 1 {
		t.Fatalf("expected collectorVersion 1, got %v", fp1.Components["collectorVersion"])
	}
}

func TestCommandCodeSessionForKey(t *testing.T) {
	apiKey := "cc-test-api-key-12345"
	now := time.Unix(1710000000, 0)

	s1 := ccSessionForKey(apiKey, now)
	s2 := ccSessionForKey(apiKey, now.Add(time.Hour)) // Same 12h bucket

	if s1 != s2 {
		t.Fatalf("expected same session within bucket, got %s vs %s", s1, s2)
	}

	uuidRegex := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !uuidRegex.MatchString(s1) {
		t.Fatalf("expected UUID format for session, got %s", s1)
	}
}
