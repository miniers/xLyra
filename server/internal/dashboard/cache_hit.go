package dashboard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/config"
	"xlyra/server/internal/store"
)

var ErrInvalidCacheHitQuery = errors.New("invalid cache hit query")

const defaultCacheHitDays = 30

type CacheHitQuery struct {
	SiteIDs      []uuid.UUID
	SiteModelIDs []uuid.UUID
	DateFrom     string
	DateTo       string
}

type CacheHitOverview struct {
	Meta      CacheHitMeta         `json:"meta"`
	Summary   CacheHitSummary      `json:"summary"`
	Daily     []CacheHitDailyPoint `json:"daily"`
	Breakdown []CacheHitBreakdown  `json:"breakdown"`
}

type CacheHitMeta struct {
	DateFrom    string `json:"date_from"`
	DateTo      string `json:"date_to"`
	TimeZone    string `json:"timezone"`
	GeneratedAt string `json:"generated_at"`
}

type CacheHitSummary struct {
	RequestCount int64    `json:"request_count"`
	PromptTokens int64    `json:"prompt_tokens"`
	CachedTokens int64    `json:"cached_tokens"`
	HitRate      *float64 `json:"hit_rate"`
}

type CacheHitDailyPoint struct {
	Date              string   `json:"date"`
	SiteID            *string  `json:"site_id"`
	SiteName          string   `json:"site_name"`
	SiteSlug          string   `json:"site_slug"`
	SiteType          string   `json:"site_type"`
	SiteModelID       *string  `json:"site_model_id"`
	ModelKey          string   `json:"model_key"`
	UpstreamModelName string   `json:"upstream_model_name"`
	RequestCount      int64    `json:"request_count"`
	PromptTokens      int64    `json:"prompt_tokens"`
	CachedTokens      int64    `json:"cached_tokens"`
	HitRate           *float64 `json:"hit_rate"`
}

type CacheHitBreakdown struct {
	SiteID            *string  `json:"site_id"`
	SiteName          string   `json:"site_name"`
	SiteSlug          string   `json:"site_slug"`
	SiteType          string   `json:"site_type"`
	SiteModelID       *string  `json:"site_model_id"`
	ModelKey          string   `json:"model_key"`
	UpstreamModelName string   `json:"upstream_model_name"`
	RequestCount      int64    `json:"request_count"`
	PromptTokens      int64    `json:"prompt_tokens"`
	CachedTokens      int64    `json:"cached_tokens"`
	HitRate           *float64 `json:"hit_rate"`
}

type cacheHitWindow struct {
	dateFrom time.Time
	dateTo   time.Time
	end      time.Time
	timeZone config.TimeZone
}

type cacheHitAggregate struct {
	date              string
	siteID            uuid.NullUUID
	siteName          string
	siteSlug          string
	siteType          string
	siteModelID       uuid.NullUUID
	modelKey          string
	upstreamModelName string
	requestCount      int64
	promptTokens      int64
	cachedTokens      int64
}

func (s *Service) CacheHit(ctx context.Context, query CacheHitQuery, now time.Time) (CacheHitOverview, error) {
	if s == nil || s.db == nil || s.db.DB() == nil {
		return CacheHitOverview{}, fmt.Errorf("dashboard store is not initialized")
	}
	if now.IsZero() {
		now = time.Now()
	}

	window, err := cacheHitTimeWindow(query, now, s.timeZone)
	if err != nil {
		return CacheHitOverview{}, err
	}
	rows, err := store.NewRequestUsageSummaryRepository(s.db.DB()).List(ctx, store.RequestUsageSummaryQuery{
		TimeZone: window.timeZone.Name,
		From:     &window.dateFrom,
		To:       &window.end,
		SiteIDs:  uniqueCacheHitUUIDs(query.SiteIDs),
	})
	if err != nil {
		return CacheHitOverview{}, fmt.Errorf("list cache hit usage summaries: %w", err)
	}

	return cacheHitOverviewFromSummaries(window, rows, query, now.In(window.timeZone.Location)), nil
}

func cacheHitTimeWindow(query CacheHitQuery, now time.Time, timeZone config.TimeZone) (cacheHitWindow, error) {
	timeZone = serviceTimeZone(timeZone)
	today := timeZone.StartOfDay(now.In(timeZone.Location))
	dateFrom := today.AddDate(0, 0, -(defaultCacheHitDays - 1))
	dateTo := today

	if raw := strings.TrimSpace(query.DateFrom); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, timeZone.Location)
		if err != nil {
			return cacheHitWindow{}, fmt.Errorf("%w: date_from must use YYYY-MM-DD", ErrInvalidCacheHitQuery)
		}
		dateFrom = parsed
	}
	if raw := strings.TrimSpace(query.DateTo); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, timeZone.Location)
		if err != nil {
			return cacheHitWindow{}, fmt.Errorf("%w: date_to must use YYYY-MM-DD", ErrInvalidCacheHitQuery)
		}
		dateTo = parsed
	}
	if dateFrom.After(dateTo) {
		return cacheHitWindow{}, fmt.Errorf("%w: date_from must be before or equal to date_to", ErrInvalidCacheHitQuery)
	}

	return cacheHitWindow{
		dateFrom: dateFrom,
		dateTo:   dateTo,
		end:      dateTo.AddDate(0, 0, 1),
		timeZone: timeZone,
	}, nil
}

func cacheHitOverviewFromSummaries(window cacheHitWindow, rows []store.RequestUsageDailySummary, query CacheHitQuery, generatedAt time.Time) CacheHitOverview {
	selectedSiteIDs := cacheHitUUIDSet(query.SiteIDs)
	selectedSiteModelIDs := cacheHitUUIDSet(query.SiteModelIDs)
	byDay := map[string]*cacheHitAggregate{}
	byBreakdown := map[string]*cacheHitAggregate{}

	for _, row := range rows {
		if !row.Success || row.BucketStart.Before(window.dateFrom) || !row.BucketStart.Before(window.end) {
			continue
		}
		if len(selectedSiteIDs) > 0 && (!row.SiteID.Valid || !selectedSiteIDs[row.SiteID.UUID]) {
			continue
		}
		if len(selectedSiteModelIDs) > 0 && (!row.SiteModelID.Valid || !selectedSiteModelIDs[row.SiteModelID.UUID]) {
			continue
		}

		date := row.BucketStart.In(window.timeZone.Location).Format("2006-01-02")
		modelKey := cacheHitModelKey(row)
		aggregateKey := cacheHitAggregateKey(row, modelKey)
		dayKey := date + "\x00" + aggregateKey
		day := byDay[dayKey]
		if day == nil {
			day = newCacheHitAggregate(date, row, modelKey)
			byDay[dayKey] = day
		}
		breakdown := byBreakdown[aggregateKey]
		if breakdown == nil {
			breakdown = newCacheHitAggregate("", row, modelKey)
			byBreakdown[aggregateKey] = breakdown
		}
		cacheHitAddUsage(day, row)
		cacheHitAddUsage(breakdown, row)
	}

	daily := make([]CacheHitDailyPoint, 0, len(byDay))
	for _, item := range byDay {
		daily = append(daily, cacheHitDailyPoint(item))
	}
	sort.SliceStable(daily, func(i, j int) bool {
		if daily[i].Date != daily[j].Date {
			return daily[i].Date < daily[j].Date
		}
		if daily[i].SiteName != daily[j].SiteName {
			return daily[i].SiteName < daily[j].SiteName
		}
		return daily[i].ModelKey < daily[j].ModelKey
	})

	breakdown := make([]CacheHitBreakdown, 0, len(byBreakdown))
	summary := CacheHitSummary{}
	for _, item := range byBreakdown {
		breakdown = append(breakdown, cacheHitBreakdown(item))
		summary.RequestCount += item.requestCount
		summary.PromptTokens += item.promptTokens
		summary.CachedTokens += item.cachedTokens
	}
	summary.HitRate = cacheHitRate(summary.CachedTokens, summary.PromptTokens)
	sort.SliceStable(breakdown, func(i, j int) bool {
		if breakdown[i].CachedTokens != breakdown[j].CachedTokens {
			return breakdown[i].CachedTokens > breakdown[j].CachedTokens
		}
		if breakdown[i].PromptTokens != breakdown[j].PromptTokens {
			return breakdown[i].PromptTokens > breakdown[j].PromptTokens
		}
		if breakdown[i].SiteName != breakdown[j].SiteName {
			return breakdown[i].SiteName < breakdown[j].SiteName
		}
		return breakdown[i].ModelKey < breakdown[j].ModelKey
	})

	return CacheHitOverview{
		Meta: CacheHitMeta{
			DateFrom:    window.dateFrom.Format("2006-01-02"),
			DateTo:      window.dateTo.Format("2006-01-02"),
			TimeZone:    window.timeZone.Name,
			GeneratedAt: generatedAt.Format(time.RFC3339),
		},
		Summary:   summary,
		Daily:     daily,
		Breakdown: breakdown,
	}
}

func newCacheHitAggregate(date string, row store.RequestUsageDailySummary, modelKey string) *cacheHitAggregate {
	return &cacheHitAggregate{
		date:              date,
		siteID:            row.SiteID,
		siteName:          defaultString(strings.TrimSpace(row.SiteName), "unknown"),
		siteSlug:          strings.TrimSpace(row.SiteSlug),
		siteType:          strings.TrimSpace(row.SiteType),
		siteModelID:       row.SiteModelID,
		modelKey:          modelKey,
		upstreamModelName: strings.TrimSpace(row.UpstreamModelName),
	}
}

func cacheHitAddUsage(item *cacheHitAggregate, row store.RequestUsageDailySummary) {
	promptTokens := max(row.PromptTokens, 0)
	cachedTokens := min(max(row.CachedTokens, 0), promptTokens)
	item.requestCount += max(row.SuccessCount, 0)
	item.promptTokens += promptTokens
	item.cachedTokens += cachedTokens
}

func cacheHitAggregateKey(row store.RequestUsageDailySummary, modelKey string) string {
	return nullableUUIDKey(row.SiteID) + "\x00" + strings.TrimSpace(row.SiteName) + "\x00" +
		nullableUUIDKey(row.SiteModelID) + "\x00" + modelKey + "\x00" + strings.TrimSpace(row.UpstreamModelName)
}

func cacheHitModelKey(row store.RequestUsageDailySummary) string {
	modelKey := strings.TrimSpace(row.CanonicalModelKey)
	if modelKey == "" || modelKey == summaryNoneKey {
		modelKey = strings.TrimSpace(row.UpstreamModelName)
	}
	return defaultString(modelKey, "unknown")
}

func cacheHitDailyPoint(item *cacheHitAggregate) CacheHitDailyPoint {
	return CacheHitDailyPoint{
		Date:              item.date,
		SiteID:            nullableUUIDString(item.siteID),
		SiteName:          item.siteName,
		SiteSlug:          item.siteSlug,
		SiteType:          item.siteType,
		SiteModelID:       nullableUUIDString(item.siteModelID),
		ModelKey:          item.modelKey,
		UpstreamModelName: item.upstreamModelName,
		RequestCount:      item.requestCount,
		PromptTokens:      item.promptTokens,
		CachedTokens:      item.cachedTokens,
		HitRate:           cacheHitRate(item.cachedTokens, item.promptTokens),
	}
}

func cacheHitBreakdown(item *cacheHitAggregate) CacheHitBreakdown {
	return CacheHitBreakdown{
		SiteID:            nullableUUIDString(item.siteID),
		SiteName:          item.siteName,
		SiteSlug:          item.siteSlug,
		SiteType:          item.siteType,
		SiteModelID:       nullableUUIDString(item.siteModelID),
		ModelKey:          item.modelKey,
		UpstreamModelName: item.upstreamModelName,
		RequestCount:      item.requestCount,
		PromptTokens:      item.promptTokens,
		CachedTokens:      item.cachedTokens,
		HitRate:           cacheHitRate(item.cachedTokens, item.promptTokens),
	}
}

func cacheHitRate(cachedTokens int64, promptTokens int64) *float64 {
	if promptTokens <= 0 {
		return nil
	}
	value := float64(cachedTokens) / float64(promptTokens)
	return &value
}

func cacheHitUUIDSet(values []uuid.UUID) map[uuid.UUID]bool {
	if len(values) == 0 {
		return nil
	}
	result := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = true
		}
	}
	return result
}

func uniqueCacheHitUUIDs(values []uuid.UUID) []uuid.UUID {
	set := cacheHitUUIDSet(values)
	if len(set) == 0 {
		return nil
	}
	result := make([]uuid.UUID, 0, len(set))
	for _, value := range values {
		if value != uuid.Nil && set[value] {
			result = append(result, value)
			delete(set, value)
		}
	}
	return result
}
