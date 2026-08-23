package store

import (
	"testing"

	"github.com/google/uuid"
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
