package gateway

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

type CredentialRateLimiter struct {
	mu        sync.Mutex
	states    map[uuid.UUID]*credentialRateLimitState
	lastSweep time.Time
}

type credentialRateLimitState struct {
	minuteWindowStart time.Time
	minuteCount       int64
	dayWindowStart    time.Time
	dayCount          int64
	lastUpdated       time.Time
}

func NewCredentialRateLimiter() *CredentialRateLimiter {
	return &CredentialRateLimiter{
		states:    make(map[uuid.UUID]*credentialRateLimitState),
		lastSweep: time.Now(),
	}
}

// CheckAndAcquire checks if the credential is within its configured RPM and RPD limits.
// If allowed, it increments the counters and returns allowed=true, cooldown=nil.
// If a limit is reached, it returns allowed=false along with the appropriate CooldownInput.
func (l *CredentialRateLimiter) CheckAndAcquire(
	ctx context.Context,
	siteID uuid.UUID,
	credential store.SiteCredential,
	now time.Time,
) (bool, *routeengine.CooldownInput) {
	if l == nil {
		return true, nil
	}
	rateLimit := store.SiteCredentialRateLimit(credential)
	if rateLimit == nil || (rateLimit.RPMLimit == nil && rateLimit.RPDLimit == nil) {
		return true, nil
	}

	if now.IsZero() {
		now = time.Now()
	}

	timezone := strings.TrimSpace(rateLimit.ResetTimezone)
	if timezone == "" {
		timezone = "UTC"
	}

	minuteWindowStart := now.UTC().Truncate(time.Minute)
	dayWindowStart, nextDayReset := credentialDailyWindow(now, timezone)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	state := l.states[credential.ID]
	if state == nil {
		state = &credentialRateLimitState{
			minuteWindowStart: minuteWindowStart,
			dayWindowStart:    dayWindowStart,
			lastUpdated:       now,
		}
		l.states[credential.ID] = state
	}

	// Reset minute window if expired
	if !state.minuteWindowStart.Equal(minuteWindowStart) {
		state.minuteWindowStart = minuteWindowStart
		state.minuteCount = 0
	}

	// Reset day window if expired
	if !state.dayWindowStart.Equal(dayWindowStart) {
		state.dayWindowStart = dayWindowStart
		state.dayCount = 0
	}

	// Check RPM
	if rateLimit.RPMLimit != nil && *rateLimit.RPMLimit > 0 {
		if state.minuteCount+1 > *rateLimit.RPMLimit {
			duration := minuteWindowStart.Add(time.Minute).Sub(now) + 1*time.Second
			if duration < 1*time.Second {
				duration = 1 * time.Second
			}
			return false, &routeengine.CooldownInput{
				SiteID:           siteID,
				SiteCredentialID: &credential.ID,
				Scope:            "credential",
				Source:           "gateway",
				Reason:           store.CooldownReasonCredentialRPMLimitReached,
				Duration:         duration,
				Metadata: map[string]any{
					"limit_type": "rpm",
					"rpm_limit":  *rateLimit.RPMLimit,
					"rpm_used":   state.minuteCount,
				},
			}
		}
	}

	// Check RPD
	if rateLimit.RPDLimit != nil && *rateLimit.RPDLimit > 0 {
		if state.dayCount+1 > *rateLimit.RPDLimit {
			duration := nextDayReset.Sub(now)
			if duration < 10*time.Minute {
				duration = 10 * time.Minute
			}
			return false, &routeengine.CooldownInput{
				SiteID:           siteID,
				SiteCredentialID: &credential.ID,
				Scope:            "credential",
				Source:           "gateway",
				Reason:           store.CooldownReasonCredentialRPDLimitReached,
				Duration:         duration,
				Metadata: map[string]any{
					"limit_type": "rpd",
					"rpd_limit":  *rateLimit.RPDLimit,
					"rpd_used":   state.dayCount,
					"reset_at":   nextDayReset.UTC().Format(time.RFC3339),
				},
			}
		}
	}

	// Increment
	state.minuteCount++
	state.dayCount++
	state.lastUpdated = now

	return true, nil
}

// CurrentUsage returns the current minute count and day count for the given credential.
func (l *CredentialRateLimiter) CurrentUsage(credentialID uuid.UUID, timezone string, now time.Time) (int64, int64) {
	if l == nil {
		return 0, 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	if timezone == "" {
		timezone = "UTC"
	}

	minuteWindowStart := now.UTC().Truncate(time.Minute)
	dayWindowStart, _ := credentialDailyWindow(now, timezone)

	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.states[credentialID]
	if !ok {
		return 0, 0
	}

	var minuteCount, dayCount int64
	if state.minuteWindowStart.Equal(minuteWindowStart) {
		minuteCount = state.minuteCount
	}
	if state.dayWindowStart.Equal(dayWindowStart) {
		dayCount = state.dayCount
	}

	return minuteCount, dayCount
}

// Reset clears any in-memory rate limit state for the specified credential.
func (l *CredentialRateLimiter) Reset(credentialID uuid.UUID) {
	if l == nil || credentialID == uuid.Nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, credentialID)
}

func (l *CredentialRateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < 5*time.Minute {
		return
	}
	l.lastSweep = now
	cutoff := now.Add(-48 * time.Hour)
	for id, state := range l.states {
		if state.lastUpdated.Before(cutoff) {
			delete(l.states, id)
		}
	}
}

func credentialDailyWindow(now time.Time, timezone string) (dayStart time.Time, nextReset time.Time) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	nowInLoc := now.In(loc)
	dayStart = time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, loc)
	nextReset = time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day()+1, 0, 0, 1, 0, loc)
	return dayStart, nextReset
}
