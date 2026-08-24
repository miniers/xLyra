package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestRouteExplorationRepositoryResetSiteUpdatesOnlyTheRequestedPairOffline(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	canonicalModelID := uuid.New()
	siteModelID := uuid.New()
	var captured string
	var updated RouteExplorationState
	storeReplaceQueryCallback(t, db, func(tx *gorm.DB) {
		tx.Statement.Build("WHERE")
		captured = tx.Statement.SQL.String()
		item, ok := tx.Statement.Dest.(*RouteExplorationState)
		if !ok {
			t.Fatalf("query destination = %T, want *RouteExplorationState", tx.Statement.Dest)
		}
		*item = RouteExplorationState{
			ID:               uuid.New(),
			CanonicalModelID: canonicalModelID,
			SiteModelID:      siteModelID,
			CycleKind:        RouteExplorationCycleInitial,
			CycleKey:         "initial",
			Attempts:         3,
			Target:           3,
		}
		tx.Statement.RowsAffected = 1
	})
	storeReplaceUpdateCallback(t, db, func(tx *gorm.DB) {
		item, ok := tx.Statement.Dest.(*RouteExplorationState)
		if !ok {
			t.Fatalf("update destination = %T, want *RouteExplorationState", tx.Statement.Dest)
		}
		updated = *item
		tx.Statement.RowsAffected = 1
	})

	if err := NewRouteExplorationRepository(db).ResetSite(context.Background(), canonicalModelID, siteModelID); err != nil {
		t.Fatalf("ResetSite returned error: %v", err)
	}

	if !strings.Contains(strings.ToLower(captured), "canonical_model_id") || !strings.Contains(strings.ToLower(captured), "site_model_id") {
		t.Fatalf("generated SQL %q is not scoped to the canonical/site-model pair", captured)
	}
	if updated.CycleKind != RouteExplorationCycleInitial || updated.Attempts != 0 || updated.Target != 1 || !strings.HasPrefix(updated.CycleKey, "manual_reset:") {
		t.Fatalf("reset state = %#v, want a pending manual reset", updated)
	}
}

func TestRouteExplorationRepositoryResetSiteValidatesIDs(t *testing.T) {
	t.Parallel()

	repo := NewRouteExplorationRepository(nil)
	if err := repo.ResetSite(context.Background(), uuid.Nil, uuid.New()); err == nil {
		t.Fatal("ResetSite should reject a missing canonical model id")
	}
	if err := repo.ResetSite(context.Background(), uuid.New(), uuid.Nil); err == nil {
		t.Fatal("ResetSite should reject a missing site model id")
	}
}
