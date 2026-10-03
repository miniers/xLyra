package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xlyra/server/internal/catalog"
	sitepkg "xlyra/server/internal/site"
	"xlyra/server/internal/store"
)

type Service struct {
	db *store.Store
}

type CandidateQuery struct {
	ModelKey            string
	EndpointType        string
	ImageGeneration     bool
	Debug               bool
	AllowedSiteIDs      []uuid.UUID
	AllowedSiteModelIDs []uuid.UUID
	ExcludeSiteIDs      []uuid.UUID
	ExcludeSiteModelIDs []uuid.UUID
	Limit               int
	FailoverLimit       int
}

type CandidateList struct {
	CanonicalModel store.CanonicalModel
	Items          []Candidate
	RoutingLock    *store.RouteLock
}

type Candidate struct {
	Rank                  int
	Score                 float64
	Cooling               bool
	CoolingReason         string
	Site                  CandidateSite
	Model                 CandidateModel
	Health                CandidateHealth
	Availability          CandidateAvailability
	Credential            CandidateCredential
	Pricing               CandidatePricing
	AutomaticAnomaly      store.RouteAnomaly
	ManualScoreAdjustment float64
	ScoreBreakdown        map[string]float64
	ScoreProfiles         map[string]CandidateScoreProfile
	Locked                bool
}

type CandidateScoreProfile struct {
	Rank      int
	Score     float64
	Breakdown map[string]float64
}

type Selection struct {
	CanonicalModel store.CanonicalModel
	Candidate      Candidate
}

type SelectionPlan struct {
	CanonicalModel store.CanonicalModel
	Selected       Candidate
	Failover       []Candidate
	RoutingLock    *store.RouteLock
}

type RouteLockTarget struct {
	SiteID            uuid.UUID
	SiteName          string
	SiteSlug          string
	SiteType          string
	SiteModelID       uuid.UUID
	UpstreamModelName string
	DisplayName       string
}

type CandidateSite struct {
	ID                     uuid.UUID
	Name                   string
	Slug                   string
	SiteType               string
	BaseURL                string
	RoutingPriority        float64
	ResponsesToolPolicy    string
	DisabledResponsesTools []string
}

type CandidateModel struct {
	SiteModelID            uuid.UUID
	UpstreamName           string
	DisplayName            string
	MatchSource            string
	MatchConfidence        int
	SupportedEndpointTypes []string
	PricingVariants        store.JSON
}

type CandidateHealth struct {
	Status                     string
	RecentSuccessRate          *float64
	RecentAvgLatencyMS         *int64
	ConsecutiveFailures        int
	ModelSuccessRate           *float64
	ModelAvgLatencyMS          *int64
	ModelRequestCount          int
	ModelAvgFirstByteLatencyMS *int64
	ModelFirstByteRequestCount int
	ModelCacheHitRate          *float64
	ModelCacheRequestCount     int
	ModelTotalRequestCount     int
	ModelLastUsedAt            *time.Time
}

type CandidateAvailability struct {
	AvailableAPIKeys             int
	TotalAPIKeys                 int
	SubscriptionKeyCount         int
	SubscriptionRemainingSeconds *int64
	SubscriptionExpiresAt        *time.Time
}

type CandidateCredential struct {
	ID                     *uuid.UUID
	Name                   string
	RoutingPriority        float64
	GroupName              *string
	CacheDomain            string
	UpstreamCostMultiplier float64
}

type CandidatePricing struct {
	GroupName              *string
	Currency               *string
	BaseInputValue         *float64
	BaseOutputValue        *float64
	BasePerRequestValue    *float64
	InputValue             *float64
	OutputValue            *float64
	CacheReadRatio         *float64
	ImageRatio             *float64
	PerRequestValue        *float64
	BillingType            *string
	QuotaType              *int64
	UpstreamCostMultiplier float64
}

type CooldownInput struct {
	SiteID           uuid.UUID
	SiteModelID      *uuid.UUID
	SiteCredentialID *uuid.UUID
	Scope            string
	Source           string
	Reason           string
	Duration         time.Duration
	Metadata         map[string]any
}

var ErrNoRouteCandidates = errors.New("no route candidates available")
var ErrRouteLockUnavailable = errors.New("locked route channel is unavailable")

type RouteLockUnavailableError struct {
	SiteModelID uuid.UUID
	Lock        *store.RouteLock
}

func (e *RouteLockUnavailableError) Error() string {
	if e == nil || e.SiteModelID == uuid.Nil {
		return ErrRouteLockUnavailable.Error()
	}
	return fmt.Sprintf("%s: site_model_id=%s", ErrRouteLockUnavailable, e.SiteModelID)
}

func (e *RouteLockUnavailableError) Unwrap() error {
	return ErrRouteLockUnavailable
}

func NewService(db *store.Store) *Service {
	return &Service{db: db}
}

func (s *Service) Candidates(ctx context.Context, query CandidateQuery) (CandidateList, error) {
	modelKey := strings.TrimSpace(query.ModelKey)
	if modelKey == "" {
		return CandidateList{}, fmt.Errorf("model_key is required")
	}

	canonicalModel, err := s.resolveCanonicalModel(ctx, modelKey)
	if err != nil {
		return CandidateList{}, err
	}

	rows, err := store.NewRouteCandidateRepository(s.db.DB()).ListByCanonicalModel(ctx, canonicalModel.ID)
	if err != nil {
		return CandidateList{}, err
	}

	var routingLock *store.RouteLock
	lock, lockErr := store.NewRouteLockRepository(s.db.DB()).GetActiveByCanonicalModel(ctx, canonicalModel.ID, time.Now())
	if lockErr == nil && lock.SiteModelID != uuid.Nil && lock.CanonicalModelID != uuid.Nil {
		lock.Policy = store.NormalizeRouteLockPolicy(lock.Policy)
		routingLock = &lock
	} else if lockErr != nil && !errors.Is(lockErr, gorm.ErrRecordNotFound) {
		return CandidateList{}, lockErr
	}

	cooldowns, err := store.NewRouteCooldownRepository(s.db.DB()).ListActive(ctx, time.Now())
	if err != nil {
		return CandidateList{}, err
	}
	cooldownState := indexCooldowns(cooldowns)
	allowedSites := uuidSet(query.AllowedSiteIDs)
	allowedModels := uuidSet(query.AllowedSiteModelIDs)
	excludedSites := uuidSet(query.ExcludeSiteIDs)
	excludedModels := uuidSet(query.ExcludeSiteModelIDs)
	strictLockTargetExcludedByAllowList := false
	if routingLock != nil && routingLock.Policy == store.RouteLockPolicyStrict {
		for _, row := range rows {
			if row.SiteModelID != routingLock.SiteModelID {
				continue
			}
			if allowedSites != nil {
				if _, ok := allowedSites[row.SiteID]; !ok {
					strictLockTargetExcludedByAllowList = true
				}
			}
			if allowedModels != nil {
				if _, ok := allowedModels[row.SiteModelID]; !ok {
					strictLockTargetExcludedByAllowList = true
				}
			}
			break
		}
	}

	candidates := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		availableAPIKeys := row.ModelAvailableKeyCount
		totalAPIKeys := row.ModelAPIKeyCount
		if row.SiteType != "newapi" && totalAPIKeys == 0 {
			availableAPIKeys = row.SiteCredentialCount
			totalAPIKeys = row.SiteCredentialCount
		}

		if !row.SiteEnabled || row.SiteStatus != "active" {
			continue
		}
		if row.SiteModelStatus != "active" {
			continue
		}
		if !routeCandidateSupportsEndpoint(row, query.EndpointType) {
			continue
		}
		if query.ImageGeneration && sitepkg.ResponsesToolDisabled(siteGatewayConfig(row.SiteMeta), sitepkg.ResponsesHostedToolImageGeneration) {
			continue
		}
		if allowedSites != nil {
			if _, ok := allowedSites[row.SiteID]; !ok {
				continue
			}
		}
		if allowedModels != nil {
			if _, ok := allowedModels[row.SiteModelID]; !ok {
				continue
			}
		}
		if _, ok := excludedSites[row.SiteID]; ok {
			continue
		}
		if _, ok := excludedModels[row.SiteModelID]; ok {
			continue
		}
		if availableAPIKeys <= 0 {
			continue
		}
		locked := routingLock != nil && row.SiteModelID == routingLock.SiteModelID
		if _, ok := cooldownState.sites[row.SiteID]; ok {
			continue
		}
		if _, ok := cooldownState.models[row.SiteModelID]; ok {
			continue
		}
		cooling, coolingReason := false, ""
		if item, ok := cooldownState.transientModels[row.SiteModelID]; ok {
			if locked && routingLock.Policy == store.RouteLockPolicyStrict {
				continue
			}
			cooling = true
			coolingReason = item.Reason
		}

		costMultiplier := row.PreferredCredentialCostMultiplier
		if costMultiplier <= 0 {
			costMultiplier = 1
		}
		baseInputValue := nullableFloat(row.PricingInputValue)
		baseOutputValue := nullableFloat(row.PricingOutputValue)
		basePerRequestValue := nullableFloat(row.PricingPerRequestValue)
		candidates = append(candidates, Candidate{
			Cooling:       cooling,
			CoolingReason: coolingReason,
			Site: CandidateSite{
				ID:                     row.SiteID,
				Name:                   row.SiteName,
				Slug:                   row.SiteSlug,
				SiteType:               row.SiteType,
				BaseURL:                row.SiteBaseURL,
				RoutingPriority:        row.SiteRoutingPriority,
				ResponsesToolPolicy:    siteResponsesToolPolicy(row.SiteMeta),
				DisabledResponsesTools: siteDisabledResponsesTools(row.SiteMeta),
			},
			Model: CandidateModel{
				SiteModelID:            row.SiteModelID,
				UpstreamName:           row.UpstreamModelName,
				DisplayName:            row.SiteModelDisplayName,
				MatchSource:            row.MatchSource,
				MatchConfidence:        row.MatchConfidence,
				SupportedEndpointTypes: append([]string(nil), row.SupportedEndpointTypes...),
				PricingVariants:        row.PricingVariants,
			},
			Health: CandidateHealth{
				Status:                     row.SiteHealthStatus,
				RecentSuccessRate:          nullableFloat(row.SiteHealthSuccessRate),
				RecentAvgLatencyMS:         nullableInt64(row.SiteHealthAvgLatencyMS),
				ConsecutiveFailures:        row.SiteHealthFailures,
				ModelSuccessRate:           nullableFloat(row.ModelSuccessRate),
				ModelAvgLatencyMS:          nullableInt64(row.ModelAvgLatencyMS),
				ModelRequestCount:          row.ModelRequestCount,
				ModelAvgFirstByteLatencyMS: nullableInt64(row.ModelAvgFirstByteLatencyMS),
				ModelFirstByteRequestCount: row.ModelFirstByteRequestCount,
				ModelCacheHitRate:          nullableFloat(row.ModelCacheHitRate),
				ModelCacheRequestCount:     row.ModelCacheRequestCount,
				ModelTotalRequestCount:     row.ModelTotalRequestCount,
				ModelLastUsedAt:            nullableTimePointer(row.ModelLastUsedAt),
			},
			Availability: CandidateAvailability{
				AvailableAPIKeys:             availableAPIKeys,
				TotalAPIKeys:                 totalAPIKeys,
				SubscriptionKeyCount:         row.SubscriptionKeyCount,
				SubscriptionRemainingSeconds: nullableInt64(row.SubscriptionRemainingSeconds),
				SubscriptionExpiresAt:        nullableTimePointer(row.SubscriptionExpiresAt),
			},
			Credential: CandidateCredential{
				ID:                     nullableUUIDPointer(row.PreferredCredentialID),
				Name:                   nullStringText(row.PreferredCredentialName),
				RoutingPriority:        row.PreferredCredentialRoutingPriority,
				GroupName:              nullableString(row.PreferredCredentialGroupName),
				CacheDomain:            row.PreferredCredentialCacheDomain,
				UpstreamCostMultiplier: costMultiplier,
			},
			Pricing: CandidatePricing{
				GroupName:              nullableString(row.PricingGroupName),
				Currency:               nullableString(row.PricingCurrency),
				BaseInputValue:         baseInputValue,
				BaseOutputValue:        baseOutputValue,
				BasePerRequestValue:    basePerRequestValue,
				InputValue:             multipliedFloat(baseInputValue, costMultiplier),
				OutputValue:            multipliedFloat(baseOutputValue, costMultiplier),
				CacheReadRatio:         nullableFloat(row.PricingCacheReadRatio),
				ImageRatio:             nullableFloat(row.PricingImageRatio),
				PerRequestValue:        multipliedFloat(basePerRequestValue, costMultiplier),
				BillingType:            nullableString(row.PricingBillingType),
				QuotaType:              nullableInt64(row.PricingQuotaType),
				UpstreamCostMultiplier: costMultiplier,
			},
			AutomaticAnomaly:      row.AutomaticAnomaly,
			ManualScoreAdjustment: row.ManualScoreAdjustment,
			Locked:                locked,
		})
	}

	priceValues := make([]float64, 0)
	for _, item := range candidates {
		if value, ok := candidateActualPriceValue(item); ok {
			priceValues = append(priceValues, value)
		}
	}

	minPrice := 0.0
	maxPrice := 0.0
	if len(priceValues) > 0 {
		minPrice = priceValues[0]
		maxPrice = priceValues[0]
		for _, value := range priceValues[1:] {
			if value < minPrice {
				minPrice = value
			}
			if value > maxPrice {
				maxPrice = value
			}
		}
	}

	preference := store.NormalizeRoutingPreference(canonicalModel.RoutingPreference)
	for index := range candidates {
		activeScore, activeBreakdown := scoreCandidateWithPreferenceConfig(candidates[index], minPrice, maxPrice, preference, canonicalModel.RoutingExpiryRescueEnabled)
		candidates[index].Score = activeScore
		if query.Debug {
			candidates[index].ScoreBreakdown = activeBreakdown
			candidates[index].ScoreProfiles = make(map[string]CandidateScoreProfile, len(routingPreferences))
			for _, profilePreference := range routingPreferences {
				score, breakdown := scoreCandidateWithPreferenceConfig(candidates[index], minPrice, maxPrice, profilePreference, canonicalModel.RoutingExpiryRescueEnabled)
				candidates[index].ScoreProfiles[profilePreference] = CandidateScoreProfile{
					Score:     score,
					Breakdown: breakdown,
				}
			}
		}
	}

	sortCandidates(candidates, preference)
	if routingLock != nil {
		if routingLock.Policy == store.RouteLockPolicyStrict {
			lockedCandidates := make([]Candidate, 0, 1)
			for _, candidate := range candidates {
				if candidate.Locked {
					lockedCandidates = append(lockedCandidates, candidate)
				}
			}
			if len(lockedCandidates) > 0 {
				candidates = lockedCandidates
			} else if strictLockTargetExcludedByAllowList {
				// A strict lock only applies when its target survives the
				// request's allow-list and availability filters.
				routingLock = nil
			} else {
				return CandidateList{}, &RouteLockUnavailableError{
					SiteModelID: routingLock.SiteModelID,
					Lock:        routingLock,
				}
			}
		} else {
			candidates = prioritizeLockedCandidate(candidates, routingLock.SiteModelID)
		}
	}

	for index := range candidates {
		candidates[index].Rank = index + 1
	}
	if query.Debug {
		assignScoreProfileRanks(candidates)
	}

	if query.Limit > 0 && len(candidates) > query.Limit {
		candidates = candidates[:query.Limit]
	}

	return CandidateList{
		CanonicalModel: canonicalModel,
		Items:          candidates,
		RoutingLock:    routingLock,
	}, nil
}

func prioritizeLockedCandidate(candidates []Candidate, siteModelID uuid.UUID) []Candidate {
	if len(candidates) < 2 || siteModelID == uuid.Nil || candidates[0].Model.SiteModelID == siteModelID {
		return candidates
	}
	index := -1
	for i := range candidates {
		if candidates[i].Model.SiteModelID == siteModelID {
			index = i
			break
		}
	}
	if index <= 0 {
		return candidates
	}
	// A fallback lock should not promote a candidate that is already cooling;
	// healthy channels remain preferred while the locked target stays available
	// as a later retry.
	if candidates[index].Cooling {
		return candidates
	}
	locked := candidates[index]
	copy(candidates[1:index+1], candidates[:index])
	candidates[0] = locked
	return candidates
}

var routingPreferences = []string{
	store.RoutingPreferenceDefault,
	store.RoutingPreferenceValue,
	store.RoutingPreferenceSpeed,
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	item := value.Time
	return &item
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sortCandidates(candidates []Candidate, preference string) {
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidateComesBefore(candidates[i], candidates[j], preference)
	})
}

func assignScoreProfileRanks(candidates []Candidate) {
	for _, preference := range routingPreferences {
		indices := make([]int, len(candidates))
		for index := range candidates {
			indices[index] = index
		}
		sort.SliceStable(indices, func(i, j int) bool {
			return candidateComesBefore(candidates[indices[i]], candidates[indices[j]], preference)
		})
		for rank, index := range indices {
			profile := candidates[index].ScoreProfiles[preference]
			profile.Rank = rank + 1
			candidates[index].ScoreProfiles[preference] = profile
		}
	}
}

func candidateComesBefore(a Candidate, b Candidate, preference string) bool {
	if a.Cooling != b.Cooling {
		return !a.Cooling
	}
	if a.Locked != b.Locked {
		return a.Locked
	}
	aScore := candidateScoreForPreference(a, preference)
	bScore := candidateScoreForPreference(b, preference)
	if preference != store.RoutingPreferenceDefault {
		if aScore != bScore {
			return aScore > bScore
		}
		if preference == store.RoutingPreferenceSpeed {
			if firstByteA, firstByteB := modelFirstByteLatency(a), modelFirstByteLatency(b); firstByteA != nil && firstByteB != nil && *firstByteA != *firstByteB {
				return *firstByteA < *firstByteB
			}
		}
	}
	if a.Site.RoutingPriority != b.Site.RoutingPriority {
		return a.Site.RoutingPriority > b.Site.RoutingPriority
	}
	if aScore == bScore {
		return a.Site.Name < b.Site.Name
	}
	return aScore > bScore
}

func candidateScoreForPreference(item Candidate, preference string) float64 {
	if profile, ok := item.ScoreProfiles[preference]; ok {
		return profile.Score
	}
	return item.Score
}

func routeCandidateSupportsEndpoint(row store.RouteCandidateRow, endpointType string) bool {
	endpointType = strings.TrimSpace(strings.ToLower(endpointType))
	if endpointType == "" {
		return true
	}
	if endpointType == "gemini-image" {
		for _, item := range row.SupportedEndpointTypes {
			supported := strings.TrimSpace(strings.ToLower(item))
			if supported == "google-gemini" || supported == "openai-image" {
				return true
			}
		}
		return false
	}
	if isMiMoV25TTSRouteModel(row.UpstreamModelName) {
		return endpointType == "openai" || endpointType == "openai-audio-speech"
	}
	for _, item := range row.SupportedEndpointTypes {
		if endpointTypeSameFamily(endpointType, strings.TrimSpace(strings.ToLower(item))) {
			return true
		}
	}
	return false
}

func isMiMoV25TTSRouteModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "mimo-v2.5-tts", "mimo-v2.5-tts-voicedesign", "mimo-v2.5-tts-voiceclone":
		return true
	default:
		return false
	}
}

func endpointTypeSameFamily(a, b string) bool {
	return endpointTypeFamily(a) != "" && endpointTypeFamily(a) == endpointTypeFamily(b)
}

func endpointTypeFamily(endpointType string) string {
	switch endpointType {
	case "openai", "openai-response", "anthropic-messages", "google-gemini":
		return "text"
	case "openai-image":
		return "image"
	case "openai-embedding":
		return "embedding"
	case "openai-audio-speech":
		return "audio-speech"
	case "systemone", "typesafe-systemone":
		return "systemone"
	default:
		return ""
	}
}

func siteGatewayConfig(meta store.JSON) *sitepkg.GatewayConfig {
	return sitepkg.GatewayConfigFromSiteMeta(meta)
}

func siteResponsesToolPolicy(meta store.JSON) string {
	cfg := sitepkg.GatewayConfigFromSiteMeta(meta)
	if cfg == nil {
		return sitepkg.ResponsesToolPolicyPassthrough
	}
	return sitepkg.NormalizeResponsesToolPolicy(cfg.ResponsesToolPolicy)
}

func siteDisabledResponsesTools(meta store.JSON) []string {
	cfg := sitepkg.GatewayConfigFromSiteMeta(meta)
	if cfg == nil {
		return nil
	}
	return sitepkg.NormalizeDisabledResponsesTools(cfg.DisabledResponsesTools)
}

func (s *Service) Select(ctx context.Context, query CandidateQuery) (Selection, error) {
	result, err := s.Candidates(ctx, query)
	if err != nil {
		return Selection{}, err
	}
	if len(result.Items) == 0 {
		return Selection{}, ErrNoRouteCandidates
	}

	return Selection{
		CanonicalModel: result.CanonicalModel,
		Candidate:      result.Items[0],
	}, nil
}

func (s *Service) Plan(ctx context.Context, query CandidateQuery) (SelectionPlan, error) {
	result, err := s.Candidates(ctx, query)
	if err != nil {
		return SelectionPlan{}, err
	}
	if len(result.Items) == 0 {
		return SelectionPlan{}, ErrNoRouteCandidates
	}

	items := result.Items

	failoverLimit := query.FailoverLimit
	if failoverLimit <= 0 {
		failoverLimit = 3
	}

	failover := make([]Candidate, 0, failoverLimit)
	for _, item := range items[1:] {
		if len(failover) >= failoverLimit {
			break
		}
		failover = append(failover, item)
	}

	return SelectionPlan{
		CanonicalModel: result.CanonicalModel,
		Selected:       items[0],
		Failover:       failover,
		RoutingLock:    result.RoutingLock,
	}, nil
}

func (s *Service) GetRouteLock(ctx context.Context, canonicalModelID uuid.UUID) (store.RouteLock, error) {
	return store.NewRouteLockRepository(s.db.DB()).GetByCanonicalModel(ctx, canonicalModelID)
}

func (s *Service) GetActiveRouteLock(ctx context.Context, canonicalModelID uuid.UUID) (store.RouteLock, error) {
	return store.NewRouteLockRepository(s.db.DB()).GetActiveByCanonicalModel(ctx, canonicalModelID, time.Now())
}

func (s *Service) GetRouteLockTarget(ctx context.Context, siteModelID uuid.UUID) (RouteLockTarget, error) {
	if siteModelID == uuid.Nil {
		return RouteLockTarget{}, fmt.Errorf("site_model_id is required")
	}
	if s == nil || s.db == nil {
		return RouteLockTarget{}, fmt.Errorf("get route lock target: store is not initialized")
	}
	model, err := store.NewSiteModelRepository(s.db.DB()).GetByID(ctx, siteModelID)
	if err != nil {
		return RouteLockTarget{}, err
	}
	site, err := store.NewSiteRepository(s.db.DB()).GetByID(ctx, model.SiteID)
	if err != nil {
		return RouteLockTarget{}, err
	}
	return RouteLockTarget{
		SiteID:            site.ID,
		SiteName:          site.Name,
		SiteSlug:          site.Slug,
		SiteType:          site.SiteType,
		SiteModelID:       model.ID,
		UpstreamModelName: model.UpstreamName,
		DisplayName:       model.DisplayName,
	}, nil
}

func (s *Service) UpsertRouteLock(ctx context.Context, params store.UpsertRouteLockParams) (store.RouteLock, error) {
	return store.NewRouteLockRepository(s.db.DB()).Upsert(ctx, params)
}

func (s *Service) DeleteRouteLock(ctx context.Context, canonicalModelID uuid.UUID) error {
	return store.NewRouteLockRepository(s.db.DB()).Delete(ctx, canonicalModelID)
}

func (s *Service) ActiveCooldowns(ctx context.Context) ([]store.RouteCooldown, error) {
	return store.NewRouteCooldownRepository(s.db.DB()).ListActive(ctx, time.Now())
}

func (s *Service) Overview(ctx context.Context, since time.Time) ([]store.RouteOverviewRow, error) {
	return store.NewRouteInsightRepository(s.db.DB()).ListOverview(ctx, since)
}

func (s *Service) ActivateCooldown(ctx context.Context, input CooldownInput) (store.RouteCooldown, error) {
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		if input.SiteModelID != nil {
			scope = "model"
		} else {
			scope = "site"
		}
	}

	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "manual"
	}

	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "cooldown"
	}

	duration := input.Duration
	if duration <= 0 {
		duration = 5 * time.Minute
	}

	var siteModelID any
	if input.SiteModelID != nil {
		siteModelID = *input.SiteModelID
	}
	var siteCredentialID any
	if input.SiteCredentialID != nil {
		siteCredentialID = *input.SiteCredentialID
	}

	metadata, _ := json.Marshal(input.Metadata)
	return store.NewRouteCooldownRepository(s.db.DB()).Activate(ctx, store.ActivateRouteCooldownParams{
		SiteID:           input.SiteID,
		SiteModelID:      siteModelID,
		SiteCredentialID: siteCredentialID,
		Scope:            scope,
		Source:           source,
		Reason:           reason,
		ActiveUntil:      time.Now().Add(duration),
		Metadata:         metadata,
	})
}

func (s *Service) ClearCooldown(ctx context.Context, siteID uuid.UUID, siteModelID *uuid.UUID, siteCredentialID *uuid.UUID, source string) error {
	var modelValue any
	if siteModelID != nil {
		modelValue = *siteModelID
	}
	var credentialValue any
	if siteCredentialID != nil {
		credentialValue = *siteCredentialID
	}
	return store.NewRouteCooldownRepository(s.db.DB()).ClearActive(ctx, siteID, modelValue, credentialValue, source)
}

func (s *Service) ClearRouteAnomaly(ctx context.Context, canonicalModelID, siteModelID uuid.UUID) error {
	if canonicalModelID == uuid.Nil {
		return fmt.Errorf("canonical_model_id is required")
	}
	if siteModelID == uuid.Nil {
		return fmt.Errorf("site_model_id is required")
	}
	return store.NewRouteAnomalyRepository(s.db.DB()).ClearActive(ctx, canonicalModelID, siteModelID)
}

func (s *Service) AdjustRouteScore(ctx context.Context, canonicalModelID, siteModelID uuid.UUID, direction string) (store.RouteScoreAdjustment, error) {
	if canonicalModelID == uuid.Nil {
		return store.RouteScoreAdjustment{}, fmt.Errorf("canonical_model_id is required")
	}
	if siteModelID == uuid.Nil {
		return store.RouteScoreAdjustment{}, fmt.Errorf("site_model_id is required")
	}

	var delta float64
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "increase":
		delta = store.RouteScoreAdjustmentStep
	case "decrease":
		delta = -store.RouteScoreAdjustmentStep
	default:
		return store.RouteScoreAdjustment{}, fmt.Errorf("direction must be increase or decrease")
	}

	return store.NewRouteScoreAdjustmentRepository(s.db.DB()).Apply(ctx, canonicalModelID, siteModelID, delta, time.Now())
}

func (s *Service) resolveCanonicalModel(ctx context.Context, modelKey string) (store.CanonicalModel, error) {
	repo := store.NewCanonicalModelRepository(s.db.DB())
	normalized := catalog.CanonicalModelKeyFromUpstream(modelKey)
	if normalized == "" {
		normalized = catalog.NormalizeModelKey(modelKey)
	}

	canonical, err := repo.GetByKey(ctx, normalized)
	if err == nil {
		return canonical, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.CanonicalModel{}, err
	}

	canonical, err = repo.GetByNormalizedAlias(ctx, normalized)
	if err != nil {
		return store.CanonicalModel{}, fmt.Errorf("canonical model %q was not found", modelKey)
	}

	return canonical, nil
}

func scoreCandidate(item Candidate, minPrice float64, maxPrice float64) (float64, map[string]float64) {
	return scoreCandidateWithPreference(item, minPrice, maxPrice, store.RoutingPreferenceDefault)
}

func scoreCandidateWithPreference(item Candidate, minPrice float64, maxPrice float64, preference string) (float64, map[string]float64) {
	return scoreCandidateWithPreferenceConfig(item, minPrice, maxPrice, preference, false)
}

func scoreCandidateWithPreferenceConfig(item Candidate, minPrice float64, maxPrice float64, preference string, rescueEnabled bool) (float64, map[string]float64) {
	preference = store.NormalizeRoutingPreference(preference)
	modelFirstByte := modelFirstByteLatency(item)
	weights := map[string]float64{
		"site_health":                         40,
		"site_success_rate":                   10,
		"site_latency":                        5,
		"model_success_rate":                  30,
		"model_latency":                       15,
		"model_first_byte_latency":            5,
		"api_key_capacity":                    15,
		"actual_price":                        20,
		"api_key_subscription_expiry_urgency": 10,
	}
	switch preference {
	case store.RoutingPreferenceValue:
		weights = map[string]float64{
			"site_health":                         40,
			"site_success_rate":                   15,
			"site_latency":                        3,
			"model_success_rate":                  25,
			"model_latency":                       5,
			"model_first_byte_latency":            2,
			"api_key_capacity":                    5,
			"actual_price":                        50,
			"api_key_subscription_expiry_urgency": 20,
		}
	case store.RoutingPreferenceSpeed:
		weights = map[string]float64{
			"site_health":                         35,
			"site_success_rate":                   10,
			"site_latency":                        15,
			"model_success_rate":                  20,
			"model_latency":                       15,
			"model_first_byte_latency":            25,
			"api_key_capacity":                    5,
			"actual_price":                        10,
			"api_key_subscription_expiry_urgency": 8,
		}
	}
	breakdown := map[string]float64{
		"site_health":                              healthScore(item.Health.Status) * weights["site_health"] / 40,
		"site_success_rate":                        successRateScore(item.Health.RecentSuccessRate, weights["site_success_rate"]),
		"site_latency":                             latencyScore(item.Health.RecentAvgLatencyMS, weights["site_latency"]),
		"model_success_rate":                       successRateScore(item.Health.ModelSuccessRate, weights["model_success_rate"]),
		"model_latency":                            latencyScore(item.Health.ModelAvgLatencyMS, weights["model_latency"]),
		"model_first_byte_latency":                 firstByteLatencyScore(modelFirstByte, weights["model_first_byte_latency"]),
		"api_key_capacity":                         float64(min(item.Availability.AvailableAPIKeys, 5)) * weights["api_key_capacity"] / 5,
		"actual_price":                             priceScore(item, minPrice, maxPrice) * weights["actual_price"] / 10,
		"api_key_subscription_expiry_urgency":      subscriptionExpiryUrgencyScore(item.Availability.SubscriptionRemainingSeconds, weights["api_key_subscription_expiry_urgency"]),
		"api_key_subscription_expiry_rescue_bonus": subscriptionExpiryRescueBonus(item.Availability.SubscriptionRemainingSeconds, preference, rescueEnabled),
	}
	if item.AutomaticAnomaly.Active && item.AutomaticAnomaly.Penalty < 0 {
		breakdown["automatic_anomaly_penalty"] = item.AutomaticAnomaly.Penalty
	}
	if item.ManualScoreAdjustment != 0 {
		breakdown["manual_score_adjustment"] = item.ManualScoreAdjustment
	}

	total := 0.0
	for _, value := range breakdown {
		total += value
	}

	return total, breakdown
}

func subscriptionExpiryUrgencyScore(remainingSeconds *int64, maxScore float64) float64 {
	if remainingSeconds == nil || *remainingSeconds <= 0 {
		return 0
	}
	switch {
	case *remainingSeconds <= int64(24*time.Hour/time.Second):
		return maxScore
	case *remainingSeconds <= int64(3*24*time.Hour/time.Second):
		return maxScore * 0.8
	case *remainingSeconds <= int64(7*24*time.Hour/time.Second):
		return maxScore * 0.6
	case *remainingSeconds <= int64(30*24*time.Hour/time.Second):
		return maxScore * 0.3
	default:
		return 0
	}
}

func subscriptionExpiryRescueBonus(remainingSeconds *int64, preference string, enabled bool) float64 {
	if !enabled || remainingSeconds == nil || *remainingSeconds <= 0 {
		return 0
	}
	preference = store.NormalizeRoutingPreference(preference)
	bonus := 0.0
	switch preference {
	case store.RoutingPreferenceValue:
		switch {
		case *remainingSeconds <= int64(24*time.Hour/time.Second):
			bonus = 45
		case *remainingSeconds <= int64(3*24*time.Hour/time.Second):
			bonus = 30
		case *remainingSeconds <= int64(7*24*time.Hour/time.Second):
			bonus = 10
		}
	case store.RoutingPreferenceSpeed:
		switch {
		case *remainingSeconds <= int64(24*time.Hour/time.Second):
			bonus = 12
		case *remainingSeconds <= int64(3*24*time.Hour/time.Second):
			bonus = 8
		case *remainingSeconds <= int64(7*24*time.Hour/time.Second):
			bonus = 3
		}
	default:
		switch {
		case *remainingSeconds <= int64(24*time.Hour/time.Second):
			bonus = 25
		case *remainingSeconds <= int64(3*24*time.Hour/time.Second):
			bonus = 15
		case *remainingSeconds <= int64(7*24*time.Hour/time.Second):
			bonus = 5
		}
	}
	return bonus
}

type cooldownIndex struct {
	sites                 map[uuid.UUID]store.RouteCooldown
	models                map[uuid.UUID]store.RouteCooldown
	transientModels       map[uuid.UUID]store.RouteCooldown
	siteCredentialCounts  map[uuid.UUID]int
	modelCredentialCounts map[uuid.UUID]int
}

func indexCooldowns(items []store.RouteCooldown) cooldownIndex {
	index := cooldownIndex{
		sites:                 make(map[uuid.UUID]store.RouteCooldown),
		models:                make(map[uuid.UUID]store.RouteCooldown),
		transientModels:       make(map[uuid.UUID]store.RouteCooldown),
		siteCredentialCounts:  make(map[uuid.UUID]int),
		modelCredentialCounts: make(map[uuid.UUID]int),
	}
	for _, item := range items {
		if item.SiteCredentialID.Valid {
			if item.SiteModelID.Valid {
				index.modelCredentialCounts[item.SiteModelID.UUID]++
			} else {
				index.siteCredentialCounts[item.SiteID]++
			}
			continue
		}
		if item.SiteModelID.Valid {
			if store.TransientRouteCooldown(item) {
				index.transientModels[item.SiteModelID.UUID] = item
			} else {
				index.models[item.SiteModelID.UUID] = item
			}
			continue
		}
		index.sites[item.SiteID] = item
	}

	return index
}

func uuidSet(items []uuid.UUID) map[uuid.UUID]struct{} {
	if len(items) == 0 {
		return nil
	}

	result := make(map[uuid.UUID]struct{}, len(items))
	for _, item := range items {
		result[item] = struct{}{}
	}
	return result
}

func healthScore(status string) float64 {
	switch status {
	case "healthy":
		return 40
	case "degraded":
		return 22
	case "unknown":
		return 12
	default:
		return 0
	}
}

func successRateScore(rate *float64, maxScore float64) float64 {
	if rate == nil {
		return 0
	}
	return *rate * maxScore
}

func latencyScore(latency *int64, maxScore float64) float64 {
	return linearLatencyScore(latency, maxScore, 500, 3000)
}

func firstByteLatencyScore(latency *int64, maxScore float64) float64 {
	return linearLatencyScore(latency, maxScore, 5000, 20000)
}

func linearLatencyScore(latency *int64, maxScore float64, fullScoreUntilMS, zeroScoreAtMS int64) float64 {
	if latency == nil {
		return 0
	}
	if *latency <= fullScoreUntilMS {
		return maxScore
	}
	if *latency >= zeroScoreAtMS {
		return 0
	}
	return maxScore * float64(zeroScoreAtMS-*latency) / float64(zeroScoreAtMS-fullScoreUntilMS)
}

func modelFirstByteLatency(item Candidate) *int64 {
	if item.Health.ModelAvgFirstByteLatencyMS == nil {
		return nil
	}
	if item.Health.ModelFirstByteRequestCount > 0 && item.Health.ModelFirstByteRequestCount < 3 {
		return item.Health.ModelAvgLatencyMS
	}
	return item.Health.ModelAvgFirstByteLatencyMS
}

func priceScore(item Candidate, minPrice float64, maxPrice float64) float64 {
	value, ok := candidateActualPriceValue(item)
	if !ok {
		return 2
	}
	if maxPrice <= minPrice {
		return 10
	}
	return 10 * ((maxPrice - value) / (maxPrice - minPrice))
}

func candidateActualPriceValue(item Candidate) (float64, bool) {
	if item.Pricing.PerRequestValue != nil {
		return *item.Pricing.PerRequestValue, true
	}

	inputMultiplier := 1.0
	if item.Pricing.InputValue != nil && item.Health.ModelCacheHitRate != nil && item.Pricing.CacheReadRatio != nil {
		hitRate := *item.Health.ModelCacheHitRate
		if hitRate < 0 {
			hitRate = 0
		}
		if hitRate > 1 {
			hitRate = 1
		}
		cacheReadRatio := *item.Pricing.CacheReadRatio
		if cacheReadRatio < 0 {
			cacheReadRatio = 0
		}
		inputMultiplier = (1 - hitRate) + hitRate*cacheReadRatio
	}

	value := 0.0
	found := false
	if item.Pricing.InputValue != nil {
		value += *item.Pricing.InputValue * inputMultiplier
		found = true
	}
	if item.Pricing.OutputValue != nil {
		value += *item.Pricing.OutputValue
		found = true
	}
	return value, found
}

func candidatePriceValue(item Candidate) (float64, bool) {
	value := 0.0
	found := false
	if item.Pricing.PerRequestValue != nil {
		return *item.Pricing.PerRequestValue, true
	}
	if item.Pricing.InputValue != nil {
		value += *item.Pricing.InputValue
		found = true
	}
	if item.Pricing.OutputValue != nil {
		value += *item.Pricing.OutputValue
		found = true
	}
	return value, found
}

func nullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func nullStringText(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullableUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := value.UUID
	return &result
}

func multipliedFloat(value *float64, multiplier float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value * multiplier
	return &result
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
