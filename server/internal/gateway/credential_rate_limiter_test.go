package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

func TestCredentialRateLimiter_NoLimits(t *testing.T) {
	limiter := NewCredentialRateLimiter()
	credID := uuid.New()
	cred := store.SiteCredential{
		ID: credID,
	}

	allowed, cooldown := limiter.CheckAndAcquire(context.Background(), uuid.New(), cred, time.Now())
	if !allowed || cooldown != nil {
		t.Fatalf("expected allowed=true, cooldown=nil, got %v, %v", allowed, cooldown)
	}
}

func TestCredentialRateLimiter_RPMLimit(t *testing.T) {
	limiter := NewCredentialRateLimiter()
	credID := uuid.New()
	siteID := uuid.New()

	meta := map[string]any{}
	rpm := int64(2)
	store.SetSiteCredentialRateLimitMeta(meta, &rpm, nil, nil)
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal meta: %v", err)
	}

	cred := store.SiteCredential{
		ID:   credID,
		Meta: rawMeta,
	}

	now := time.Date(2026, 9, 11, 12, 0, 10, 0, time.UTC)

	// 1st request -> allowed
	allowed, cooldown := limiter.CheckAndAcquire(context.Background(), siteID, cred, now)
	if !allowed || cooldown != nil {
		t.Fatalf("1st request should be allowed, got %v, %v", allowed, cooldown)
	}

	// 2nd request -> allowed
	allowed, cooldown = limiter.CheckAndAcquire(context.Background(), siteID, cred, now.Add(5*time.Second))
	if !allowed || cooldown != nil {
		t.Fatalf("2nd request should be allowed, got %v, %v", allowed, cooldown)
	}

	// 3rd request in same minute -> rate limited!
	allowed, cooldown = limiter.CheckAndAcquire(context.Background(), siteID, cred, now.Add(10*time.Second))
	if allowed || cooldown == nil {
		t.Fatalf("3rd request should be blocked by RPM limit")
	}
	if cooldown.Reason != store.CooldownReasonCredentialRPMLimitReached {
		t.Fatalf("cooldown reason = %s, want %s", cooldown.Reason, store.CooldownReasonCredentialRPMLimitReached)
	}
	if cooldown.Duration <= 0 || cooldown.Duration > 65*time.Second {
		t.Fatalf("unexpected cooldown duration: %s", cooldown.Duration)
	}

	// Request in next minute -> allowed!
	nextMin := now.Add(time.Minute)
	allowed, cooldown = limiter.CheckAndAcquire(context.Background(), siteID, cred, nextMin)
	if !allowed || cooldown != nil {
		t.Fatalf("request in next minute should be allowed, got %v, %v", allowed, cooldown)
	}
}

func TestCredentialRateLimiter_RPDLimit(t *testing.T) {
	limiter := NewCredentialRateLimiter()
	credID := uuid.New()
	siteID := uuid.New()

	meta := map[string]any{}
	rpd := int64(3)
	tz := "America/Los_Angeles"
	store.SetSiteCredentialRateLimitMeta(meta, nil, &rpd, &tz)
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal meta: %v", err)
	}

	cred := store.SiteCredential{
		ID:   credID,
		Meta: rawMeta,
	}

	loc, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, loc)

	for i := 1; i <= 3; i++ {
		allowed, cooldown := limiter.CheckAndAcquire(context.Background(), siteID, cred, now.Add(time.Duration(i)*time.Minute))
		if !allowed || cooldown != nil {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Check CurrentUsage right after 3rd request (at 10:03)
	minuteCount, dayCount := limiter.CurrentUsage(credID, tz, now.Add(3*time.Minute))
	if minuteCount != 1 || dayCount != 3 {
		t.Fatalf("CurrentUsage at 10:03 = minute %d, day %d; want 1 and 3", minuteCount, dayCount)
	}

	// 4th request -> blocked by RPD
	allowed, cooldown := limiter.CheckAndAcquire(context.Background(), siteID, cred, now.Add(4*time.Minute))
	if allowed || cooldown == nil {
		t.Fatalf("4th request should be blocked by RPD limit")
	}
	if cooldown.Reason != store.CooldownReasonCredentialRPDLimitReached {
		t.Fatalf("cooldown reason = %s, want %s", cooldown.Reason, store.CooldownReasonCredentialRPDLimitReached)
	}
	// It's 10:04 AM PT, reset is at midnight (14 hours away)
	if cooldown.Duration < 13*time.Hour || cooldown.Duration > 15*time.Hour {
		t.Fatalf("unexpected daily cooldown duration: %s", cooldown.Duration)
	}

	// After 10:04 attempt was rejected, day count is still 3, minute count at 10:04 is 0
	minuteCount4, dayCount4 := limiter.CurrentUsage(credID, tz, now.Add(4*time.Minute))
	if minuteCount4 != 0 || dayCount4 != 3 {
		t.Fatalf("CurrentUsage at 10:04 = minute %d, day %d; want 0 and 3", minuteCount4, dayCount4)
	}

	// Resetting the limiter state for this credential allows immediate acquisition
	limiter.Reset(credID)
	allowedAfterReset, cooldownAfterReset := limiter.CheckAndAcquire(context.Background(), siteID, cred, now.Add(4*time.Minute))
	if !allowedAfterReset || cooldownAfterReset != nil {
		t.Fatalf("request after Reset should be allowed, got %v, %v", allowedAfterReset, cooldownAfterReset)
	}
}
