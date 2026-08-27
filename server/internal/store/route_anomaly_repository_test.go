package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestDetectRouteAnomalyMetricsCombinesLatencyAndCachePenalties(t *testing.T) {
	t.Parallel()

	metrics := detectRouteAnomalyMetrics(routeAnomalyObservation{
		FirstByteLatency: routeAnomalyWindowMetric{
			RecentValue: 2200, BaselineValue: 1000, Ready: true,
			RecentSamples: 5, BaselineSamples: 10,
		},
		CacheHitRate: routeAnomalyWindowMetric{
			RecentValue: 0.4, BaselineValue: 0.8, Ready: true,
			RecentSamples: 5, BaselineSamples: 10,
		},
	})
	if got := routeAnomalyPenalty(metrics); got != -30 {
		t.Fatalf("routeAnomalyPenalty = %v, want -30", got)
	}
	if len(metrics) != 2 || metrics[0].Metric != "first_byte_latency" || metrics[1].Metric != "cache_hit_rate" {
		t.Fatalf("anomaly metrics = %#v, want latency and cache metrics", metrics)
	}
}

func TestEvaluateRouteAnomalyStateUsesSustainAndRecoveryWindows(t *testing.T) {
	t.Parallel()

	canonicalModelID := uuid.New()
	siteModelID := uuid.New()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	bad := routeAnomalyObservation{FirstByteLatency: routeAnomalyWindowMetric{
		RecentValue: 1800, BaselineValue: 1000, Ready: true,
		RecentSamples: 5, BaselineSamples: 10,
	}}
	good := routeAnomalyObservation{FirstByteLatency: routeAnomalyWindowMetric{
		RecentValue: 1000, BaselineValue: 1000, Ready: true,
		RecentSamples: 5, BaselineSamples: 10,
	}}

	state := RouteAnomalyState{CanonicalModelID: canonicalModelID, SiteModelID: siteModelID}
	state = evaluateRouteAnomalyState(state, bad, now)
	if state.Active || state.BadSince == nil {
		t.Fatalf("initial anomaly state = %#v, want sustained-but-inactive state", state)
	}
	state = evaluateRouteAnomalyState(state, bad, now.Add(routeAnomalySustainWindow-time.Second))
	if state.Active {
		t.Fatal("anomaly should not activate before sustain window")
	}
	state = evaluateRouteAnomalyState(state, bad, now.Add(routeAnomalySustainWindow))
	if !state.Active || state.Penalty != -10 {
		t.Fatalf("sustained anomaly state = %#v, want active -10 penalty", state)
	}

	firstGoodAt := now.Add(routeAnomalySustainWindow + time.Second)
	state = evaluateRouteAnomalyState(state, good, firstGoodAt)
	if !state.Active {
		t.Fatal("anomaly should remain active before recovery window")
	}
	state = evaluateRouteAnomalyState(state, good, firstGoodAt.Add(routeAnomalyRecoveryWindow))
	if state.Active || state.Penalty != 0 || state.ActiveUntil != nil {
		t.Fatalf("recovered anomaly state = %#v, want inactive zero penalty", state)
	}
}

func TestBuildRouteAnomalyObservationUsesRecentAndBaselineWindows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	siteModelID := uuid.New()
	snapshots := make([]HealthSnapshot, 0, 10)
	for index := 0; index < 5; index++ {
		snapshots = append(snapshots, HealthSnapshot{
			SiteModelID:        uuid.NullUUID{UUID: siteModelID, Valid: true},
			Scope:              "model",
			Source:             "gateway",
			FirstByteLatencyMS: sql.NullInt64{Int64: 1800, Valid: true},
			CheckedAt:          now.Add(-time.Duration(index) * time.Minute),
		})
		snapshots = append(snapshots, HealthSnapshot{
			SiteModelID:        uuid.NullUUID{UUID: siteModelID, Valid: true},
			Scope:              "model",
			Source:             "gateway",
			FirstByteLatencyMS: sql.NullInt64{Int64: 900, Valid: true},
			CheckedAt:          now.Add(-time.Hour - time.Duration(index)*time.Minute),
		})
	}

	observation := buildRouteAnomalyObservation(snapshots, RouteModelCacheStats{
		RecentRequestCount: 5, RecentPromptTokens: 1000, RecentCachedTokens: 200,
		BaselineRequestCount: 10, BaselinePromptTokens: 2000, BaselineCachedTokens: 1600,
	}, siteModelID, now)
	if !observation.FirstByteLatency.Ready || observation.FirstByteLatency.RecentValue != 1800 || observation.FirstByteLatency.BaselineValue != 900 {
		t.Fatalf("first-byte observation = %#v, want recent 1800 and baseline 900", observation.FirstByteLatency)
	}
	if !observation.CacheHitRate.Ready || observation.CacheHitRate.RecentValue != 0.2 || observation.CacheHitRate.BaselineValue != 0.8 {
		t.Fatalf("cache observation = %#v, want recent 0.2 and baseline 0.8", observation.CacheHitRate)
	}
}

func TestRouteAnomalyRepositoryListActiveScopesActiveAndUnexpiredStates(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	var captured string
	storeReplaceQueryCallback(t, db, func(tx *gorm.DB) {
		tx.Statement.Build("SELECT", "FROM", "WHERE", "ORDER BY")
		captured = tx.Statement.SQL.String()
		tx.Statement.RowsAffected = 0
	})
	items, err := NewRouteAnomalyRepository(db).ListActive(context.Background(), now)
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ListActive items = %#v, want empty offline result", items)
	}
	lower := strings.ToLower(captured)
	if !strings.Contains(lower, "active") || !strings.Contains(lower, "active_until") {
		t.Fatalf("ListActive SQL %q does not scope active and expiry fields", captured)
	}
}

func TestRouteAnomalyRepositoryClearActiveScopesPairAndResetsState(t *testing.T) {
	t.Parallel()

	canonicalModelID := uuid.New()
	siteModelID := uuid.New()
	db := storeRepositoryOfflineGorm(t)
	var captured string
	var vars []any
	storeReplaceUpdateCallback(t, db, func(tx *gorm.DB) {
		tx.Statement.Build("UPDATE", "SET", "WHERE")
		captured = tx.Statement.SQL.String()
		vars = tx.Statement.Vars
		tx.Statement.RowsAffected = 1
	})

	if err := NewRouteAnomalyRepository(db).ClearActive(context.Background(), canonicalModelID, siteModelID); err != nil {
		t.Fatalf("ClearActive returned error: %v", err)
	}

	lower := strings.ToLower(captured)
	for _, want := range []string{"canonical_model_id", "site_model_id", "active", "penalty"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("generated SQL %q missing %q", captured, want)
		}
	}
	for _, want := range []string{canonicalModelID.String(), siteModelID.String()} {
		found := false
		for _, value := range vars {
			if id, ok := value.(uuid.UUID); ok && id.String() == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("bound vars %#v missing %s", vars, want)
		}
	}
}

func TestRouteAnomalyRepositoryClearActiveRequiresPair(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	storeReplaceUpdateCallback(t, db, func(tx *gorm.DB) {
		t.Fatal("ClearActive should not query with an incomplete pair")
	})

	if err := NewRouteAnomalyRepository(db).ClearActive(context.Background(), uuid.Nil, uuid.New()); err == nil {
		t.Fatal("ClearActive returned nil for missing canonical model id")
	}
}
