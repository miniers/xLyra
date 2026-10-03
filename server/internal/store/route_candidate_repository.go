package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type RouteCandidateRow struct {
	CanonicalModelID                   uuid.UUID
	CanonicalModelKey                  string
	CanonicalDisplayName               string
	SiteID                             uuid.UUID
	SiteName                           string
	SiteSlug                           string
	SiteType                           string
	SiteBaseURL                        string
	SiteStatus                         string
	SiteEnabled                        bool
	SiteRoutingPriority                float64
	SiteMeta                           JSON
	SiteModelID                        uuid.UUID
	UpstreamModelName                  string
	SiteModelDisplayName               string
	SiteModelStatus                    string
	MatchSource                        string
	MatchConfidence                    int
	SiteHealthStatus                   string
	SiteHealthSuccessRate              sql.NullFloat64
	SiteHealthAvgLatencyMS             sql.NullInt64
	SiteHealthFailures                 int
	SiteHealthCheckedAt                sql.NullTime
	ModelSuccessRate                   sql.NullFloat64
	ModelAvgLatencyMS                  sql.NullInt64
	ModelRequestCount                  int
	ModelTotalRequestCount             int
	ModelLastUsedAt                    sql.NullTime
	ModelAvgFirstByteLatencyMS         sql.NullInt64
	ModelFirstByteRequestCount         int
	ModelCacheHitRate                  sql.NullFloat64
	ModelCacheRequestCount             int
	ModelAPIKeyCount                   int
	ModelAvailableKeyCount             int
	SiteCredentialCount                int
	SubscriptionKeyCount               int
	SubscriptionRemainingSeconds       sql.NullInt64
	SubscriptionExpiresAt              sql.NullTime
	PreferredCredentialID              uuid.NullUUID
	PreferredCredentialName            sql.NullString
	PreferredCredentialRoutingPriority float64
	PreferredCredentialCostMultiplier  float64
	PreferredCredentialGroupName       sql.NullString
	PreferredCredentialCacheDomain     string
	OAuthSubscriptionExpiresAt         sql.NullTime
	PricingGroupName                   sql.NullString
	PricingCurrency                    sql.NullString
	PricingInputValue                  sql.NullFloat64
	PricingOutputValue                 sql.NullFloat64
	PricingCacheReadRatio              sql.NullFloat64
	PricingImageRatio                  sql.NullFloat64
	PricingPerRequestValue             sql.NullFloat64
	PricingBillingType                 sql.NullString
	PricingQuotaType                   sql.NullInt64
	PricingVariants                    JSON
	SupportedEndpointTypes             []string
	AutomaticAnomaly                   RouteAnomaly
	ManualScoreAdjustment              float64
}

type RouteCandidateRepository struct {
	db *gorm.DB
}

type routeAnomalyEvaluationKey struct {
	CanonicalModelID uuid.UUID
	SiteModelID      uuid.UUID
}

var routeAnomalyEvaluations sync.Map

func NewRouteCandidateRepository(db *gorm.DB) RouteCandidateRepository {
	return RouteCandidateRepository{db: db}
}

func (r RouteCandidateRepository) ListByCanonicalModel(ctx context.Context, canonicalModelID uuid.UUID) ([]RouteCandidateRow, error) {
	canonical, err := NewCanonicalModelRepository(r.db).GetByID(ctx, canonicalModelID)
	if err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if canonical.Status != "active" {
		return []RouteCandidateRow{}, nil
	}
	siteModels, err := NewSiteModelRepository(r.db).ListByCanonical(ctx, canonicalModelID)
	if err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if len(siteModels) == 0 {
		return []RouteCandidateRow{}, nil
	}

	siteIDs := make([]uuid.UUID, 0, len(siteModels))
	siteModelIDs := make([]uuid.UUID, 0, len(siteModels))
	seenSiteIDs := make(map[uuid.UUID]struct{}, len(siteModels))
	for _, model := range siteModels {
		if model.ID != uuid.Nil {
			siteModelIDs = append(siteModelIDs, model.ID)
		}
		if model.SiteID != uuid.Nil {
			if _, ok := seenSiteIDs[model.SiteID]; !ok {
				seenSiteIDs[model.SiteID] = struct{}{}
				siteIDs = append(siteIDs, model.SiteID)
			}
		}
	}

	var sites []Site
	var healthStates []SiteHealthState
	var healthSnapshots []HealthSnapshot
	var apiKeyModels []SiteAPIKeyModel
	var credentials []SiteCredential
	var keyStates []SiteAPIKeyState
	var pricings []SiteModelPricing
	var oauthConnections []OAuthConnection
	var overrides []SiteModelEndpointOverride
	now := time.Now()
	if err := r.db.WithContext(ctx).Where(map[string]any{"id": siteIDs}).Find(&sites).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if err := r.db.WithContext(ctx).Where(map[string]any{"site_id": siteIDs}).Find(&healthStates).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if err := r.db.WithContext(ctx).Where(map[string]any{
		"site_model_id": siteModelIDs,
		"scope":         "model",
		"source":        "gateway",
	}).Find(&healthSnapshots).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if err := r.db.WithContext(ctx).Where(map[string]any{"site_model_id": siteModelIDs}).Find(&apiKeyModels).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if err := r.db.WithContext(ctx).Where(map[string]any{"site_id": siteIDs}).Find(&credentials).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	credentialIDs := make([]uuid.UUID, 0, len(credentials))
	for _, credential := range credentials {
		if credential.ID != uuid.Nil {
			credentialIDs = append(credentialIDs, credential.ID)
		}
	}
	if len(credentialIDs) > 0 {
		if err := r.db.WithContext(ctx).Where(map[string]any{"site_credential_id": credentialIDs}).Find(&keyStates).Error; err != nil {
			return nil, fmt.Errorf("list route candidates: %w", err)
		}
	}
	if err := r.db.WithContext(ctx).Where(map[string]any{
		"site_model_id": siteModelIDs,
		"available":     true,
	}).Find(&pricings).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if err := r.db.WithContext(ctx).
		Select("site_id", "metadata").
		Where(map[string]any{"site_id": siteIDs}).
		Find(&oauthConnections).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	cacheStats, err := r.listModelCacheStatsFromHourlySummaries(ctx, canonical, siteModels, now)
	if err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	anomalyStates, err := NewRouteAnomalyRepository(r.db).ListByCanonicalModel(ctx, canonicalModelID)
	if err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	scoreAdjustments, err := NewRouteScoreAdjustmentRepository(r.db).ListActiveByCanonicalModel(ctx, canonicalModelID, now)
	if err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	if err := r.db.WithContext(ctx).Where(map[string]any{"site_model_id": siteModelIDs}).Find(&overrides).Error; err != nil {
		return nil, fmt.Errorf("list route candidates: %w", err)
	}
	overrideByModel := map[uuid.UUID]SiteModelEndpointOverride{}
	for _, override := range overrides {
		overrideByModel[override.SiteModelID] = override
	}
	cooldowns, err := NewRouteCooldownRepository(r.db).ListActiveForRouteCandidates(ctx, siteIDs, siteModelIDs, credentialIDs, now)
	if err != nil {
		return nil, err
	}
	sitesByID := map[uuid.UUID]Site{}
	for _, site := range filterActiveSites(sites) {
		sitesByID[site.ID] = site
	}
	healthBySiteID := map[uuid.UUID]SiteHealthState{}
	for _, state := range healthStates {
		healthBySiteID[state.SiteID] = state
	}
	credentialsByID := map[uuid.UUID]SiteCredential{}
	for _, credential := range credentials {
		credentialsByID[credential.ID] = credential
	}
	keyStatesByCredentialID := map[uuid.UUID]SiteAPIKeyState{}
	for _, state := range keyStates {
		keyStatesByCredentialID[state.SiteCredentialID] = state
	}
	oauthSubscriptionExpiryBySiteID := make(map[uuid.UUID]sql.NullTime)
	for _, connection := range oauthConnections {
		if connection.SiteID == nil || *connection.SiteID == uuid.Nil {
			continue
		}
		expiresAt, ok := oauthConnectionSubscriptionExpiresAt(connection.Metadata)
		if !ok {
			continue
		}
		current := oauthSubscriptionExpiryBySiteID[*connection.SiteID]
		if !current.Valid || expiresAt.Before(current.Time) {
			oauthSubscriptionExpiryBySiteID[*connection.SiteID] = sql.NullTime{Time: expiresAt, Valid: true}
		}
	}
	anomalyStatesBySiteModelID := make(map[uuid.UUID]RouteAnomalyState, len(anomalyStates))
	for _, state := range anomalyStates {
		anomalyStatesBySiteModelID[state.SiteModelID] = state
	}
	manualScoreAdjustmentsBySiteModelID := make(map[uuid.UUID]float64, len(scoreAdjustments))
	for _, adjustment := range scoreAdjustments {
		manualScoreAdjustmentsBySiteModelID[adjustment.SiteModelID] = adjustment.Delta
	}
	items := make([]RouteCandidateRow, 0, len(siteModels))
	for _, model := range siteModels {
		site := sitesByID[model.SiteID]
		if site.ID == uuid.Nil {
			continue
		}
		state := healthBySiteID[site.ID]
		row := RouteCandidateRow{
			CanonicalModelID:           canonical.ID,
			CanonicalModelKey:          canonical.ModelKey,
			CanonicalDisplayName:       canonical.DisplayName,
			PricingVariants:            canonical.PricingVariants,
			SiteID:                     site.ID,
			SiteName:                   site.Name,
			SiteSlug:                   site.Slug,
			SiteType:                   site.SiteType,
			SiteBaseURL:                site.BaseURL,
			SiteStatus:                 site.Status,
			SiteEnabled:                site.Enabled,
			SiteRoutingPriority:        site.RoutingPriority,
			SiteMeta:                   site.Meta,
			SiteModelID:                model.ID,
			UpstreamModelName:          model.UpstreamName,
			SiteModelDisplayName:       model.DisplayName,
			SiteModelStatus:            model.Status,
			OAuthSubscriptionExpiresAt: oauthSubscriptionExpiryBySiteID[site.ID],
			MatchSource:                model.MatchSource,
			MatchConfidence:            model.MatchConfidence,
			SiteHealthStatus:           defaultHealthStatus(state.Status),
			SiteHealthSuccessRate:      state.RecentSuccessRate,
			SiteHealthAvgLatencyMS:     state.RecentAvgLatencyMS,
			SiteHealthFailures:         state.ConsecutiveFailures,
			SiteHealthCheckedAt:        state.CheckedAt,
		}
		fillModelHealth(&row, healthSnapshots)
		fillModelCacheStats(&row, cacheStats[model.ID])
		anomalyState := anomalyStatesBySiteModelID[model.ID]
		cacheStatsForModel := cacheStats[model.ID]
		observation := buildRouteAnomalyObservation(healthSnapshots, cacheStatsForModel, model.ID, now)
		if routeAnomalyStateNeedsEvaluation(anomalyState, now) &&
			(anomalyState.ID != uuid.Nil || observationHasData(observation)) {
			anomalyState.CanonicalModelID = canonical.ID
			anomalyState.SiteModelID = model.ID
			scheduleRouteAnomalyEvaluation(ctx, r.db, anomalyState, healthSnapshots, observation, now)
		}
		row.AutomaticAnomaly = routeAnomalyFromState(anomalyState, now)
		row.ManualScoreAdjustment = manualScoreAdjustmentsBySiteModelID[model.ID]
		fillRouteKeyCounts(&row, apiKeyModels, credentialsByID, keyStatesByCredentialID, cooldowns)
		fillRouteSiteCredentialCount(&row, credentials, keyStatesByCredentialID, cooldowns)
		fillRoutePricing(&row, pricings)
		row.SupportedEndpointTypes = effectiveSupportedEndpointTypes(canonical, model, overrideByModel[model.ID])
		items = append(items, row)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SiteName != items[j].SiteName {
			return items[i].SiteName < items[j].SiteName
		}
		return items[i].UpstreamModelName < items[j].UpstreamModelName
	})
	return items, nil
}

func (r RouteCandidateRepository) listModelCacheStatsFromHourlySummaries(ctx context.Context, canonical CanonicalModel, siteModels []SiteModel, now time.Time) (map[uuid.UUID]RouteModelCacheStats, error) {
	result := make(map[uuid.UUID]RouteModelCacheStats)
	if canonical.ID == uuid.Nil || len(siteModels) == 0 {
		return result, nil
	}

	siteModelKeys := make([]string, 0, len(siteModels))
	siteModelIDs := make(map[uuid.UUID]struct{}, len(siteModels))
	for _, model := range siteModels {
		if model.ID == uuid.Nil {
			continue
		}
		siteModelKeys = append(siteModelKeys, model.ID.String())
		siteModelIDs[model.ID] = struct{}{}
	}
	if len(siteModelKeys) == 0 {
		return result, nil
	}

	canonicalModelKeys := []string{canonical.ID.String()}
	if strings.TrimSpace(canonical.ModelKey) != "" {
		canonicalModelKeys = append(canonicalModelKeys, canonical.ModelKey)
	}
	baselineSince := now.Add(-routeAnomalyBaselineWindow)
	recentSince := now.Add(-routeAnomalyRecentWindow)
	var rows []RequestUsageHourlySummary
	query := r.db.WithContext(ctx).
		Model(&RequestUsageHourlySummary{}).
		Select("bucket_start", "site_model_id", "site_model_key", "request_count", "prompt_tokens", "cached_tokens", "first_request_at", "last_request_at").
		Where("canonical_model_key IN ? AND site_model_key IN ? AND bucket_start >= ? AND bucket_start <= ? AND internal = ?", canonicalModelKeys, siteModelKeys, baselineSince.Add(-time.Hour), now, false).
		Find(&rows)
	if query.Error != nil {
		return nil, query.Error
	}
	for _, row := range rows {
		siteModelID := uuid.Nil
		if row.SiteModelID.Valid {
			siteModelID = row.SiteModelID.UUID
		} else if parsed, err := uuid.Parse(row.SiteModelKey); err == nil {
			siteModelID = parsed
		}
		if _, ok := siteModelIDs[siteModelID]; !ok {
			continue
		}
		firstRequestAt := row.BucketStart
		if row.FirstRequestAt.Valid {
			firstRequestAt = row.FirstRequestAt.Time
		}
		lastRequestAt := row.BucketStart.Add(time.Hour)
		if row.LastRequestAt.Valid {
			lastRequestAt = row.LastRequestAt.Time
		}
		if lastRequestAt.Before(baselineSince) || firstRequestAt.After(now) {
			continue
		}

		stats := result[siteModelID]
		stats.SiteModelID = siteModelID
		stats.RequestCount += row.RequestCount
		stats.PromptTokens += row.PromptTokens
		if row.CachedTokens > 0 {
			stats.CachedTokens += row.CachedTokens
		}
		if lastRequestAt.After(recentSince) {
			stats.RecentRequestCount += row.RequestCount
			stats.RecentPromptTokens += row.PromptTokens
			if row.CachedTokens > 0 {
				stats.RecentCachedTokens += row.CachedTokens
			}
		} else {
			stats.BaselineRequestCount += row.RequestCount
			stats.BaselinePromptTokens += row.PromptTokens
			if row.CachedTokens > 0 {
				stats.BaselineCachedTokens += row.CachedTokens
			}
		}
		result[siteModelID] = stats
	}
	return result, nil
}

func scheduleRouteAnomalyEvaluation(ctx context.Context, db *gorm.DB, state RouteAnomalyState, snapshots []HealthSnapshot, fallbackObservation routeAnomalyObservation, now time.Time) {
	key := routeAnomalyEvaluationKey{CanonicalModelID: state.CanonicalModelID, SiteModelID: state.SiteModelID}
	if key.CanonicalModelID == uuid.Nil || key.SiteModelID == uuid.Nil {
		return
	}
	if _, loaded := routeAnomalyEvaluations.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	go func() {
		defer routeAnomalyEvaluations.Delete(key)
		baseContext := context.Background()
		if ctx != nil {
			baseContext = context.WithoutCancel(ctx)
		}
		evaluationContext, cancel := context.WithTimeout(baseContext, 5*time.Second)
		defer cancel()
		anomalyRepository := NewRouteAnomalyRepository(db)
		claimed, err := anomalyRepository.ClaimEvaluation(evaluationContext, state.CanonicalModelID, state.SiteModelID, now)
		if err != nil || !claimed {
			return
		}
		observation := fallbackObservation
		if stats, err := NewRouteCandidateRepository(db).listModelCacheStats(evaluationContext, []SiteModel{{ID: state.SiteModelID}}); err == nil {
			observation = buildRouteAnomalyObservation(snapshots, stats[state.SiteModelID], state.SiteModelID, now)
		}
		evaluated := evaluateRouteAnomalyState(state, observation, now)
		// Anomaly scoring is best-effort. A transient write failure must not
		// make an otherwise routable candidate unavailable.
		_ = anomalyRepository.SaveEvaluation(evaluationContext, evaluated)
	}()
}

type RouteModelCacheStats struct {
	SiteModelID          uuid.UUID `gorm:"column:site_model_id"`
	RequestCount         int64     `gorm:"column:request_count"`
	PromptTokens         int64     `gorm:"column:prompt_tokens"`
	CachedTokens         int64     `gorm:"column:cached_tokens"`
	RecentRequestCount   int64     `gorm:"column:recent_request_count"`
	RecentPromptTokens   int64     `gorm:"column:recent_prompt_tokens"`
	RecentCachedTokens   int64     `gorm:"column:recent_cached_tokens"`
	BaselineRequestCount int64     `gorm:"column:baseline_request_count"`
	BaselinePromptTokens int64     `gorm:"column:baseline_prompt_tokens"`
	BaselineCachedTokens int64     `gorm:"column:baseline_cached_tokens"`
}

func (r RouteCandidateRepository) listModelCacheStats(ctx context.Context, siteModels []SiteModel) (map[uuid.UUID]RouteModelCacheStats, error) {
	result := make(map[uuid.UUID]RouteModelCacheStats)
	if len(siteModels) == 0 {
		return result, nil
	}

	siteModelIDs := make([]uuid.UUID, 0, len(siteModels))
	for _, model := range siteModels {
		if model.ID != uuid.Nil {
			siteModelIDs = append(siteModelIDs, model.ID)
		}
	}
	if len(siteModelIDs) == 0 {
		return result, nil
	}

	now := time.Now()
	recentSince := now.Add(-routeAnomalyRecentWindow)
	baselineSince := now.Add(-routeAnomalyBaselineWindow)
	var rows []RouteModelCacheStats
	query := r.db.WithContext(ctx).
		Table("usage_records").
		Select(`request_logs.site_model_id AS site_model_id,
			COUNT(*) FILTER (WHERE request_logs.metadata #>> '{routing_exploration,warmup}' IS DISTINCT FROM 'true') AS request_count,
			COALESCE(SUM(CASE WHEN request_logs.metadata #>> '{routing_exploration,warmup}' = 'true' THEN 0 ELSE usage_records.prompt_tokens END), 0) AS prompt_tokens,
			COALESCE(SUM(CASE
				WHEN request_logs.metadata #>> '{routing_exploration,warmup}' = 'true' THEN 0
				WHEN usage_records.cached_tokens IS NULL OR usage_records.cached_tokens < 0 THEN 0
				ELSE usage_records.cached_tokens
			END), 0) AS cached_tokens,
			COUNT(*) FILTER (WHERE request_logs.metadata #>> '{routing_exploration,warmup}' IS DISTINCT FROM 'true' AND request_logs.created_at >= ? AND request_logs.created_at <= ?) AS recent_request_count,
			COALESCE(SUM(CASE
				WHEN request_logs.metadata #>> '{routing_exploration,warmup}' = 'true' OR request_logs.created_at < ? OR request_logs.created_at > ? THEN 0
				ELSE usage_records.prompt_tokens
			END), 0) AS recent_prompt_tokens,
			COALESCE(SUM(CASE
				WHEN request_logs.metadata #>> '{routing_exploration,warmup}' = 'true' OR request_logs.created_at < ? OR request_logs.created_at > ? THEN 0
				WHEN usage_records.cached_tokens IS NULL OR usage_records.cached_tokens < 0 THEN 0
				ELSE usage_records.cached_tokens
			END), 0) AS recent_cached_tokens,
			COUNT(*) FILTER (WHERE request_logs.metadata #>> '{routing_exploration,warmup}' IS DISTINCT FROM 'true' AND request_logs.created_at >= ? AND request_logs.created_at < ?) AS baseline_request_count,
			COALESCE(SUM(CASE
				WHEN request_logs.metadata #>> '{routing_exploration,warmup}' = 'true' OR request_logs.created_at < ? OR request_logs.created_at >= ? THEN 0
				ELSE usage_records.prompt_tokens
			END), 0) AS baseline_prompt_tokens,
			COALESCE(SUM(CASE
				WHEN request_logs.metadata #>> '{routing_exploration,warmup}' = 'true' OR request_logs.created_at < ? OR request_logs.created_at >= ? THEN 0
				WHEN usage_records.cached_tokens IS NULL OR usage_records.cached_tokens < 0 THEN 0
				ELSE usage_records.cached_tokens
			END), 0) AS baseline_cached_tokens`,
			recentSince, now,
			recentSince, now,
			recentSince, now,
			baselineSince, recentSince,
			baselineSince, recentSince,
			baselineSince, recentSince).
		Joins("JOIN request_logs ON request_logs.id = usage_records.request_log_id").
		Where("request_logs.site_model_id IN ? AND request_logs.internal = ? AND request_logs.created_at >= ?", siteModelIDs, false, baselineSince).
		Group("request_logs.site_model_id").
		Find(&rows)
	if query.Error != nil {
		return nil, query.Error
	}
	for _, row := range rows {
		result[row.SiteModelID] = row
	}
	return result, nil
}

func routeAnomalyStateNeedsEvaluation(state RouteAnomalyState, now time.Time) bool {
	return state.LastEvaluatedAt == nil || now.Sub(*state.LastEvaluatedAt) >= routeAnomalyEvaluationInterval
}

func buildRouteAnomalyObservation(snapshots []HealthSnapshot, cacheStats RouteModelCacheStats, siteModelID uuid.UUID, now time.Time) routeAnomalyObservation {
	observation := routeAnomalyObservation{}
	recentSince := now.Add(-routeAnomalyRecentWindow)
	baselineSince := now.Add(-routeAnomalyBaselineWindow)
	var recentFirstByteSum, baselineFirstByteSum int64
	var recentFirstByteCount, baselineFirstByteCount int
	for _, snapshot := range snapshots {
		if !snapshot.SiteModelID.Valid || snapshot.SiteModelID.UUID != siteModelID || snapshot.Scope != "model" || snapshot.Source != "gateway" || !snapshot.FirstByteLatencyMS.Valid || snapshot.FirstByteLatencyMS.Int64 <= 0 {
			continue
		}
		if snapshot.CheckedAt.Before(baselineSince) || snapshot.CheckedAt.After(now) {
			continue
		}
		if snapshot.CheckedAt.After(recentSince) {
			recentFirstByteSum += snapshot.FirstByteLatencyMS.Int64
			recentFirstByteCount++
			continue
		}
		baselineFirstByteSum += snapshot.FirstByteLatencyMS.Int64
		baselineFirstByteCount++
	}
	if recentFirstByteCount >= routeAnomalyMinSamples && baselineFirstByteCount >= routeAnomalyMinSamples {
		observation.FirstByteLatency = routeAnomalyWindowMetric{
			RecentValue:     float64(recentFirstByteSum) / float64(recentFirstByteCount),
			BaselineValue:   float64(baselineFirstByteSum) / float64(baselineFirstByteCount),
			RecentSamples:   recentFirstByteCount,
			BaselineSamples: baselineFirstByteCount,
			Ready:           baselineFirstByteSum > 0,
		}
	}

	recentCacheRate, recentCacheReady := cacheHitRate(cacheStats.RecentPromptTokens, cacheStats.RecentCachedTokens)
	baselineCacheRate, baselineCacheReady := cacheHitRate(cacheStats.BaselinePromptTokens, cacheStats.BaselineCachedTokens)
	if cacheStats.RecentRequestCount >= routeAnomalyMinSamples && cacheStats.BaselineRequestCount >= routeAnomalyMinSamples && recentCacheReady && baselineCacheReady && baselineCacheRate >= 0.1 {
		observation.CacheHitRate = routeAnomalyWindowMetric{
			RecentValue:     recentCacheRate,
			BaselineValue:   baselineCacheRate,
			RecentSamples:   int(cacheStats.RecentRequestCount),
			BaselineSamples: int(cacheStats.BaselineRequestCount),
			Ready:           true,
		}
	}
	return observation
}

func cacheHitRate(promptTokens, cachedTokens int64) (float64, bool) {
	if promptTokens <= 0 {
		return 0, false
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	rate := float64(cachedTokens) / float64(promptTokens)
	if rate > 1 {
		rate = 1
	}
	return rate, true
}

func fillModelCacheStats(row *RouteCandidateRow, stats RouteModelCacheStats) {
	row.ModelCacheRequestCount = int(stats.RequestCount)
	if stats.PromptTokens <= 0 {
		return
	}
	cachedTokens := stats.CachedTokens
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	rate := float64(cachedTokens) / float64(stats.PromptTokens)
	if rate > 1 {
		rate = 1
	}
	row.ModelCacheHitRate = sql.NullFloat64{Float64: rate, Valid: true}
}

func effectiveSupportedEndpointTypes(canonical CanonicalModel, model SiteModel, override SiteModelEndpointOverride) []string {
	return siteModelEndpointPolicy(canonical, model, override).SupportedEndpointTypes
}

func collectCanonicalEndpointTypes(model CanonicalModel) []string {
	var values []string
	if json.Unmarshal(model.SupportedEndpointTypes, &values) != nil {
		return nil
	}
	return values
}

func defaultHealthStatus(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func fillModelHealth(row *RouteCandidateRow, snapshots []HealthSnapshot) {
	since := time.Now().Add(-24 * time.Hour)
	lifetimeTotal := 0
	var lastUsedAt time.Time
	total := 0
	success := 0
	latencySum := int64(0)
	latencyCount := int64(0)
	firstByteLatencySum := int64(0)
	firstByteLatencyCount := int64(0)
	for _, snapshot := range snapshots {
		if !snapshot.SiteModelID.Valid || snapshot.SiteModelID.UUID != row.SiteModelID || snapshot.Scope != "model" || snapshot.Source != "gateway" {
			continue
		}
		lifetimeTotal++
		if lastUsedAt.IsZero() || snapshot.CheckedAt.After(lastUsedAt) {
			lastUsedAt = snapshot.CheckedAt
		}
		if snapshot.CheckedAt.Before(since) {
			continue
		}
		total++
		if snapshot.Success {
			success++
		}
		if snapshot.LatencyMS.Valid {
			latencySum += snapshot.LatencyMS.Int64
			latencyCount++
		}
		if snapshot.FirstByteLatencyMS.Valid {
			firstByteLatencySum += snapshot.FirstByteLatencyMS.Int64
			firstByteLatencyCount++
		}
	}
	row.ModelTotalRequestCount = lifetimeTotal
	if !lastUsedAt.IsZero() {
		row.ModelLastUsedAt = sql.NullTime{Time: lastUsedAt, Valid: true}
	}
	row.ModelRequestCount = total
	if total > 0 {
		row.ModelSuccessRate = sql.NullFloat64{Float64: float64(success) / float64(total), Valid: true}
	}
	if latencyCount > 0 {
		row.ModelAvgLatencyMS = sql.NullInt64{Int64: latencySum / latencyCount, Valid: true}
	}
	row.ModelFirstByteRequestCount = int(firstByteLatencyCount)
	if firstByteLatencyCount > 0 {
		row.ModelAvgFirstByteLatencyMS = sql.NullInt64{Int64: firstByteLatencySum / firstByteLatencyCount, Valid: true}
	}
}

func fillRouteKeyCounts(row *RouteCandidateRow, apiKeyModels []SiteAPIKeyModel, credentials map[uuid.UUID]SiteCredential, states map[uuid.UUID]SiteAPIKeyState, cooldowns []RouteCooldown) {
	total := map[uuid.UUID]struct{}{}
	available := map[uuid.UUID]GatewayCredential{}
	now := time.Now()
	for _, apiKeyModel := range apiKeyModels {
		if !apiKeyModel.SiteModelID.Valid || apiKeyModel.SiteModelID.UUID != row.SiteModelID {
			continue
		}
		total[apiKeyModel.SiteCredentialID] = struct{}{}
		credential, ok := credentials[apiKeyModel.SiteCredentialID]
		if !ok || credential.ID == uuid.Nil || credential.SiteID != row.SiteID || !isModelBoundCredentialType(credential.CredentialType) {
			continue
		}
		state := states[apiKeyModel.SiteCredentialID]
		if apiKeyModel.Available && apiKeyModel.Enabled && credentialUsable(credential) && credentialStateUsableForCredentialAt(credential, state, now) && !credentialCooling(cooldowns, row.SiteID, row.SiteModelID, credential.ID) {
			available[apiKeyModel.SiteCredentialID] = GatewayCredential{Credential: credential, State: state, GroupName: state.GroupName, ModelUpdatedAt: apiKeyModel.UpdatedAt}
		}
	}
	row.ModelAPIKeyCount = len(total)
	row.ModelAvailableKeyCount = len(available)
	items := make([]GatewayCredential, 0, len(available))
	for _, item := range available {
		items = append(items, item)
	}
	SortGatewayCredentials(items)
	setRouteSubscriptionExpiry(row, items, now)
	if len(items) > 0 {
		setPreferredRouteCredential(row, items[0])
	}
}

func fillRouteSiteCredentialCount(row *RouteCandidateRow, credentials []SiteCredential, states map[uuid.UUID]SiteAPIKeyState, cooldowns []RouteCooldown) {
	items := make([]GatewayCredential, 0)
	now := time.Now()
	for _, credential := range credentials {
		if credential.SiteID != row.SiteID || !isAPIKeyCredentialType(credential.CredentialType) || !credentialUsable(credential) || credentialCooling(cooldowns, row.SiteID, uuid.Nil, credential.ID) {
			continue
		}
		state := states[credential.ID]
		if !credentialStateUsableForCredentialAt(credential, state, now) {
			continue
		}
		items = append(items, GatewayCredential{Credential: credential, State: state, GroupName: state.GroupName})
	}
	SortGatewayCredentials(items)
	row.SiteCredentialCount = len(items)
	if row.ModelAPIKeyCount == 0 {
		setRouteSubscriptionExpiry(row, items, now)
	}
	if !row.PreferredCredentialID.Valid && len(items) > 0 {
		setPreferredRouteCredential(row, items[0])
	}
}

func setRouteSubscriptionExpiry(row *RouteCandidateRow, items []GatewayCredential, now time.Time) {
	row.SubscriptionKeyCount = 0
	row.SubscriptionRemainingSeconds = sql.NullInt64{}
	row.SubscriptionExpiresAt = sql.NullTime{}

	var earliest time.Time
	for _, item := range items {
		expiresAt, ok := gatewayCredentialExpiry(item, now)
		if !ok || !expiresAt.After(now) {
			continue
		}
		row.SubscriptionKeyCount++
		if earliest.IsZero() || expiresAt.Before(earliest) {
			earliest = expiresAt
		}
	}
	if siteExpiry, ok := siteMetaSubscriptionExpiresAt(row.SiteMeta); ok && siteExpiry.After(now) {
		if earliest.IsZero() || siteExpiry.Before(earliest) {
			earliest = siteExpiry
		}
		if row.SubscriptionKeyCount == 0 {
			row.SubscriptionKeyCount = 1
		}
	}
	if row.OAuthSubscriptionExpiresAt.Valid && row.OAuthSubscriptionExpiresAt.Time.After(now) {
		if earliest.IsZero() || row.OAuthSubscriptionExpiresAt.Time.Before(earliest) {
			earliest = row.OAuthSubscriptionExpiresAt.Time
		}
		if row.SubscriptionKeyCount == 0 {
			row.SubscriptionKeyCount = 1
		}
	}
	if earliest.IsZero() {
		return
	}
	remaining := int64(earliest.Sub(now).Seconds())
	if remaining < 1 {
		remaining = 1
	}
	row.SubscriptionRemainingSeconds = sql.NullInt64{Int64: remaining, Valid: true}
	row.SubscriptionExpiresAt = sql.NullTime{Time: earliest, Valid: true}
}

func setPreferredRouteCredential(row *RouteCandidateRow, item GatewayCredential) {
	row.PreferredCredentialID = uuid.NullUUID{UUID: item.Credential.ID, Valid: item.Credential.ID != uuid.Nil}
	name := SiteCredentialDisplayName(item.Credential, item.State)
	row.PreferredCredentialName = sql.NullString{String: name, Valid: name != ""}
	row.PreferredCredentialRoutingPriority = SiteCredentialRoutingPriority(item.Credential)
	row.PreferredCredentialCostMultiplier = SiteCredentialUpstreamCostMultiplier(item.Credential)
	row.PreferredCredentialGroupName = item.GroupName
	row.PreferredCredentialCacheDomain = SiteCredentialCacheDomain(item.Credential)
}

func fillRoutePricing(row *RouteCandidateRow, pricings []SiteModelPricing) {
	matches := make([]SiteModelPricing, 0)
	for _, pricing := range pricings {
		if pricing.SiteModelID.Valid && pricing.SiteModelID.UUID == row.SiteModelID {
			matches = append(matches, pricing)
		}
	}
	if len(matches) == 0 {
		return
	}
	groupName := ""
	if row.PreferredCredentialGroupName.Valid {
		groupName = row.PreferredCredentialGroupName.String
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return pricingRank(matches[i], groupName) < pricingRank(matches[j], groupName)
	})
	pricing := matches[0]
	row.PricingGroupName = sql.NullString{String: pricing.GroupName, Valid: pricing.GroupName != ""}
	row.PricingCurrency = sql.NullString{String: pricing.Currency, Valid: pricing.Currency != ""}
	row.PricingInputValue = pricing.InputValue
	row.PricingOutputValue = pricing.OutputValue
	row.PricingCacheReadRatio = pricing.CacheRatio
	row.PricingImageRatio = pricing.ImageRatio
	row.PricingPerRequestValue = pricing.PerRequestValue
	row.PricingBillingType = sql.NullString{String: pricing.BillingType, Valid: pricing.BillingType != ""}
	row.PricingQuotaType = sql.NullInt64{Int64: int64(pricing.QuotaType), Valid: true}
}

func collectSupportedEndpointTypes(model SiteModel) []string {
	seen := map[string]struct{}{}
	items := make([]string, 0)

	if len(model.Capabilities) > 0 {
		var values map[string]any
		if err := json.Unmarshal(model.Capabilities, &values); err == nil {
			switch raw := values["supported_endpoint_types"].(type) {
			case []any:
				for _, item := range raw {
					text, _ := item.(string)
					text = strings.TrimSpace(text)
					if text == "" {
						continue
					}
					if _, ok := seen[text]; ok {
						continue
					}
					seen[text] = struct{}{}
					items = append(items, text)
				}
			case []string:
				for _, item := range raw {
					text := strings.TrimSpace(item)
					if text == "" {
						continue
					}
					if _, ok := seen[text]; ok {
						continue
					}
					seen[text] = struct{}{}
					items = append(items, text)
				}
			}
		}
	}

	return items
}
