package store

import (
	"testing"
	"time"
)

func TestNormalizeRouteLockPolicy(t *testing.T) {
	if got := NormalizeRouteLockPolicy(" FALLBACK "); got != RouteLockPolicyFallback {
		t.Fatalf("NormalizeRouteLockPolicy = %q, want %q", got, RouteLockPolicyFallback)
	}
	if got := NormalizeRouteLockPolicy("unknown"); got != RouteLockPolicyStrict {
		t.Fatalf("NormalizeRouteLockPolicy(unknown) = %q, want %q", got, RouteLockPolicyStrict)
	}
	if !ValidRouteLockPolicy(RouteLockPolicyStrict) || !ValidRouteLockPolicy(RouteLockPolicyFallback) {
		t.Fatal("expected strict and fallback policies to be valid")
	}
	if ValidRouteLockPolicy("unknown") {
		t.Fatal("expected unknown policy to be invalid")
	}
}

func TestRouteLockIsActive(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if !RouteLockIsActive(RouteLock{}, now) {
		t.Fatal("lock without expiry should be active")
	}
	if !RouteLockIsActive(RouteLock{ExpiresAt: timePtr(now.Add(time.Minute))}, now) {
		t.Fatal("future lock should be active")
	}
	if RouteLockIsActive(RouteLock{ExpiresAt: timePtr(now)}, now) {
		t.Fatal("lock expiring at now should be inactive")
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
