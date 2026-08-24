package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestFillModelCacheStatsCalculatesBoundedHitRate(t *testing.T) {
	t.Parallel()

	row := RouteCandidateRow{SiteModelID: uuid.New()}
	fillModelCacheStats(&row, RouteModelCacheStats{
		RequestCount: 3,
		PromptTokens: 100,
		CachedTokens: 125,
	})
	if row.ModelCacheRequestCount != 3 {
		t.Fatalf("cache request count = %d, want 3", row.ModelCacheRequestCount)
	}
	if !row.ModelCacheHitRate.Valid || row.ModelCacheHitRate.Float64 != 1 {
		t.Fatalf("cache hit rate = %#v, want 1", row.ModelCacheHitRate)
	}

	zero := RouteCandidateRow{}
	fillModelCacheStats(&zero, RouteModelCacheStats{RequestCount: 1, PromptTokens: 10})
	if !zero.ModelCacheHitRate.Valid || zero.ModelCacheHitRate.Float64 != 0 {
		t.Fatalf("zero cache hit rate = %#v, want valid zero", zero.ModelCacheHitRate)
	}

	unknown := RouteCandidateRow{}
	fillModelCacheStats(&unknown, RouteModelCacheStats{RequestCount: 1, CachedTokens: 10})
	if unknown.ModelCacheHitRate.Valid {
		t.Fatalf("cache hit rate without prompt tokens = %#v, want invalid", unknown.ModelCacheHitRate)
	}
}

func TestRouteCandidateCacheStatsExcludesExplorationWarmupRecords(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	var captured string
	storeReplaceQueryCallback(t, db, func(tx *gorm.DB) {
		tx.Statement.Build("SELECT", "FROM", "WHERE", "GROUP BY")
		captured = strings.Join(tx.Statement.Selects, " ") + " " + tx.Statement.SQL.String()
	})

	_, err := NewRouteCandidateRepository(db).listModelCacheStats(context.Background(), []SiteModel{{ID: uuid.New()}})
	if err != nil {
		t.Fatalf("listModelCacheStats returned error: %v", err)
	}
	lower := strings.ToLower(captured)
	for _, fragment := range []string{"routing_exploration", "warmup", "is distinct from"} {
		if !strings.Contains(lower, fragment) {
			t.Fatalf("cache stats SQL %q does not contain %q", captured, fragment)
		}
	}
}
