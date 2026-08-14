package dashboard

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/config"
	"xlyra/server/internal/store"
)

func TestCacheHitTimeWindowUsesConfiguredTimeZoneAndCustomDates(t *testing.T) {
	t.Parallel()

	window, err := cacheHitTimeWindow(CacheHitQuery{
		DateFrom: "2026-05-01",
		DateTo:   "2026-05-03",
	}, time.Date(2026, 5, 10, 3, 0, 0, 0, time.UTC), config.LoadTimeZone("UTC"))
	if err != nil {
		t.Fatalf("cache hit window: %v", err)
	}
	if got := window.dateFrom.Format("2006-01-02"); got != "2026-05-01" {
		t.Fatalf("date from = %q, want 2026-05-01", got)
	}
	if got := window.dateTo.Format("2006-01-02"); got != "2026-05-03" {
		t.Fatalf("date to = %q, want 2026-05-03", got)
	}
	if got := window.end.Format(time.RFC3339); got != "2026-05-04T00:00:00Z" {
		t.Fatalf("end = %q, want 2026-05-04T00:00:00Z", got)
	}
}

func TestCacheHitTimeWindowRejectsInvalidRange(t *testing.T) {
	t.Parallel()

	_, err := cacheHitTimeWindow(CacheHitQuery{DateFrom: "2026-05-03", DateTo: "2026-05-01"}, time.Now(), config.LoadTimeZone("UTC"))
	if !errors.Is(err, ErrInvalidCacheHitQuery) {
		t.Fatalf("error = %v, want invalid cache hit query", err)
	}
}

func TestCacheHitOverviewFromSummariesAggregatesBySiteModelAndExcludesFailures(t *testing.T) {
	t.Parallel()

	timeZone := config.LoadTimeZone("UTC")
	window, err := cacheHitTimeWindow(CacheHitQuery{DateFrom: "2026-05-01", DateTo: "2026-05-02"}, time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC), timeZone)
	if err != nil {
		t.Fatalf("cache hit window: %v", err)
	}
	siteID := uuid.New()
	otherSiteID := uuid.New()
	modelID := uuid.New()
	otherModelID := uuid.New()
	rows := []store.RequestUsageDailySummary{
		{
			BucketStart: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			Success:     true, SuccessCount: 2, PromptTokens: 100, CachedTokens: 25,
			SiteID: uuid.NullUUID{UUID: siteID, Valid: true}, SiteName: "Alpha", SiteSlug: "alpha", SiteType: "openai",
			SiteModelID: uuid.NullUUID{UUID: modelID, Valid: true}, CanonicalModelKey: "gpt-5", UpstreamModelName: "gpt-5-upstream",
		},
		{
			BucketStart: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			Success:     true, SuccessCount: 1, PromptTokens: 50, CachedTokens: 50,
			SiteID: uuid.NullUUID{UUID: siteID, Valid: true}, SiteName: "Alpha", SiteSlug: "alpha", SiteType: "openai",
			SiteModelID: uuid.NullUUID{UUID: modelID, Valid: true}, CanonicalModelKey: "gpt-5", UpstreamModelName: "gpt-5-upstream",
		},
		{
			BucketStart: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
			Success:     true, SuccessCount: 1, PromptTokens: 40, CachedTokens: 80,
			SiteID: uuid.NullUUID{UUID: siteID, Valid: true}, SiteName: "Alpha", SiteSlug: "alpha", SiteType: "openai",
			SiteModelID: uuid.NullUUID{UUID: otherModelID, Valid: true}, CanonicalModelKey: "gpt-5-mini", UpstreamModelName: "gpt-5-mini-upstream",
		},
		{
			BucketStart: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
			Success:     false, FailureCount: 1, PromptTokens: 1000, CachedTokens: 1000,
			SiteID: uuid.NullUUID{UUID: siteID, Valid: true}, SiteName: "Alpha",
			SiteModelID: uuid.NullUUID{UUID: modelID, Valid: true}, CanonicalModelKey: "gpt-5",
		},
		{
			BucketStart: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
			Success:     true, SuccessCount: 3, PromptTokens: 60, CachedTokens: 30,
			SiteID: uuid.NullUUID{UUID: otherSiteID, Valid: true}, SiteName: "Beta",
			SiteModelID: uuid.NullUUID{UUID: modelID, Valid: true}, CanonicalModelKey: "gpt-5",
		},
	}

	overview := cacheHitOverviewFromSummaries(window, rows, CacheHitQuery{SiteIDs: []uuid.UUID{siteID}}, time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	if overview.Summary.RequestCount != 4 || overview.Summary.PromptTokens != 190 || overview.Summary.CachedTokens != 115 {
		t.Fatalf("summary = %#v, want requests=4 prompt=190 cached=115", overview.Summary)
	}
	if overview.Summary.HitRate == nil || *overview.Summary.HitRate != float64(115)/190 {
		t.Fatalf("summary hit rate = %#v, want %v", overview.Summary.HitRate, float64(115)/190)
	}
	if len(overview.Daily) != 2 || len(overview.Breakdown) != 2 {
		t.Fatalf("daily/breakdown lengths = %d/%d, want 2/2", len(overview.Daily), len(overview.Breakdown))
	}
	if overview.Breakdown[0].ModelKey != "gpt-5" || overview.Breakdown[0].PromptTokens != 150 || overview.Breakdown[0].CachedTokens != 75 {
		t.Fatalf("first breakdown = %#v", overview.Breakdown[0])
	}
	if overview.Breakdown[1].ModelKey != "gpt-5-mini" || overview.Breakdown[1].CachedTokens != 40 || overview.Breakdown[1].HitRate == nil || *overview.Breakdown[1].HitRate != 1 {
		t.Fatalf("second breakdown = %#v", overview.Breakdown[1])
	}
}

func TestCacheHitOverviewFromSummariesFiltersExactSiteModel(t *testing.T) {
	t.Parallel()

	timeZone := config.LoadTimeZone("UTC")
	window, err := cacheHitTimeWindow(CacheHitQuery{DateFrom: "2026-05-01", DateTo: "2026-05-01"}, time.Now(), timeZone)
	if err != nil {
		t.Fatalf("cache hit window: %v", err)
	}
	siteID := uuid.New()
	firstModelID := uuid.New()
	secondModelID := uuid.New()
	rows := []store.RequestUsageDailySummary{
		{BucketStart: window.dateFrom, Success: true, SuccessCount: 1, PromptTokens: 10, CachedTokens: 5, SiteID: uuid.NullUUID{UUID: siteID, Valid: true}, SiteModelID: uuid.NullUUID{UUID: firstModelID, Valid: true}, CanonicalModelKey: "same-model"},
		{BucketStart: window.dateFrom, Success: true, SuccessCount: 1, PromptTokens: 20, CachedTokens: 2, SiteID: uuid.NullUUID{UUID: siteID, Valid: true}, SiteModelID: uuid.NullUUID{UUID: secondModelID, Valid: true}, CanonicalModelKey: "same-model"},
	}

	overview := cacheHitOverviewFromSummaries(window, rows, CacheHitQuery{SiteIDs: []uuid.UUID{siteID}, SiteModelIDs: []uuid.UUID{secondModelID}}, time.Now())
	if len(overview.Breakdown) != 1 || overview.Breakdown[0].SiteModelID == nil || *overview.Breakdown[0].SiteModelID != secondModelID.String() {
		t.Fatalf("breakdown = %#v, want only second model", overview.Breakdown)
	}
	if overview.Summary.PromptTokens != 20 || overview.Summary.CachedTokens != 2 {
		t.Fatalf("summary = %#v, want prompt=20 cached=2", overview.Summary)
	}
}
