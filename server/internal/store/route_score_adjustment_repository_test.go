package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestApplyRouteScoreAdjustmentAccumulatesAndRefreshesExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	first := applyRouteScoreAdjustment(RouteScoreAdjustment{}, RouteScoreAdjustmentStep, now)
	if first.Delta != 5 || !first.ActiveUntil.Equal(now.Add(RouteScoreAdjustmentDuration)) {
		t.Fatalf("first adjustment = %#v, want +5 for 10 minutes", first)
	}

	second := applyRouteScoreAdjustment(first, RouteScoreAdjustmentStep, now.Add(time.Minute))
	if second.Delta != 10 || !second.ActiveUntil.Equal(now.Add(time.Minute+RouteScoreAdjustmentDuration)) {
		t.Fatalf("accumulated adjustment = %#v, want +10 with refreshed expiry", second)
	}

	third := applyRouteScoreAdjustment(second, -RouteScoreAdjustmentStep, now.Add(2*time.Minute))
	if third.Delta != 5 {
		t.Fatalf("opposite adjustment = %#v, want +5", third)
	}

	expired := applyRouteScoreAdjustment(second, -RouteScoreAdjustmentStep, now.Add(11*time.Minute))
	if expired.Delta != -5 {
		t.Fatalf("expired adjustment = %#v, want a fresh -5 adjustment", expired)
	}

	clampedHigh := applyRouteScoreAdjustment(RouteScoreAdjustment{Delta: 28, ActiveUntil: now.Add(time.Hour)}, 5, now)
	if clampedHigh.Delta != MaxRouteScoreAdjustmentDelta {
		t.Fatalf("clamped high delta = %v, want %v", clampedHigh.Delta, MaxRouteScoreAdjustmentDelta)
	}

	clampedLow := applyRouteScoreAdjustment(RouteScoreAdjustment{Delta: -28, ActiveUntil: now.Add(time.Hour)}, -5, now)
	if clampedLow.Delta != MinRouteScoreAdjustmentDelta {
		t.Fatalf("clamped low delta = %v, want %v", clampedLow.Delta, MinRouteScoreAdjustmentDelta)
	}
}

func TestRouteScoreAdjustmentRepositoryListActiveScopesPairAndExpiry(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	canonicalModelID := uuid.New()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	var captured string
	storeReplaceQueryCallback(t, db, func(tx *gorm.DB) {
		tx.Statement.Build("SELECT", "FROM", "WHERE", "ORDER BY")
		captured = tx.Statement.SQL.String()
		tx.Statement.RowsAffected = 0
	})

	items, err := NewRouteScoreAdjustmentRepository(db).ListActiveByCanonicalModel(context.Background(), canonicalModelID, now)
	if err != nil {
		t.Fatalf("ListActiveByCanonicalModel returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v, want empty offline result", items)
	}
	lower := strings.ToLower(captured)
	for _, want := range []string{"canonical_model_id", "delta", "active_until"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("generated SQL %q missing %q", captured, want)
		}
	}
}

func TestRouteScoreAdjustmentRepositoryValidatesInputs(t *testing.T) {
	t.Parallel()

	repo := NewRouteScoreAdjustmentRepository(nil)
	if _, err := repo.Apply(context.Background(), uuid.Nil, uuid.New(), RouteScoreAdjustmentStep, time.Time{}); err == nil {
		t.Fatal("Apply should reject a missing canonical model id")
	}
	if _, err := repo.Apply(context.Background(), uuid.New(), uuid.New(), 1, time.Time{}); err == nil {
		t.Fatal("Apply should reject an unsupported delta")
	}
	if _, err := repo.ListActiveByCanonicalModel(context.Background(), uuid.Nil, time.Time{}); err == nil {
		t.Fatal("ListActiveByCanonicalModel should reject a missing canonical model id")
	}
}
