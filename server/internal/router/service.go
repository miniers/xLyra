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
	// AllowExploration is set only by real gateway requests.  Admin previews
	// and score inspection must never consume a trial quota.
	AllowExploration bool
}

type CandidateList struct {
	CanonicalModel store.CanonicalModel
	Items          []Candidate
}

type Candidate struct {
	Rank           int
	Score          float64
	Cooling        bool
	CoolingReason  string
	Site           CandidateSite
	Model          CandidateModel
	Health         CandidateHealth
	Availability   CandidateAvailability
	Credential     CandidateCredential
	Pricing        CandidatePricing
	ScoreBreakdown map[string]float64
	ScoreProfiles  map[string]CandidateScoreProfile
	Exploration    CandidateExploration
}

type CandidateExploration struct {
	Enabled       bool
	Mode          string
	CycleKey      string
	Status        string
	Attempts      int
	Target        int
	Remaining     int
	LastAttemptAt *time.Time
	LastUsedAt    *time.Time
	Reserved      bool
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

	cooldowns, err := store.NewRouteCooldownRepository(s.db.DB()).ListActive(ctx, time.Now())
	if err != nil {
		return CandidateList{}, err
	}
	cooldownState := indexCooldowns(cooldowns)
	explorationConfig := store.NormalizeRoutingExplorationConfig(canonicalModel)
	var explorationStates map[uuid.UUID]store.RouteExplorationState
	if explorationConfig.Enabled {
		states, stateErr := store.NewRouteExplorationRepository(s.db.DB()).ListByCanonicalModel(ctx, canonicalModel.ID)
		if stateErr != nil {
			return CandidateList{}, stateErr
		}
		explorationStates = make(map[uuid.UUID]store.RouteExplorationState, len(states))
		for _, state := range states {
			explorationStates[state.SiteModelID] = state
		}
	}
	allowedSites := uuidSet(query.AllowedSiteIDs)
	allowedModels := uuidSet(query.AllowedSiteModelIDs)
	excludedSites := uuidSet(query.ExcludeSiteIDs)
	excludedModels := uuidSet(query.ExcludeSiteModelIDs)

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
		if availableAPIKeys <= 0 {
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
		if _, ok := cooldownState.sites[row.SiteID]; ok {
			continue
		}
		if _, ok := cooldownState.models[row.SiteModelID]; ok {
			continue
		}
		cooling, coolingReason := false, ""
		if item, ok := cooldownState.transientModels[row.SiteModelID]; ok {
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
		exploration := buildCandidateExploration(explorationConfig, explorationStates[row.SiteModelID], row.ModelTotalRequestCount, nullableTimePointer(row.ModelLastUsedAt), time.Now())
		if cooling && exploration.Status != "disabled" && exploration.Status != "normal" && exploration.Status != "completed" {
			exploration.Status = "cooldown"
		}
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
			Exploration: exploration,
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
	}, nil
}

var routingPreferences = []string{
	store.RoutingPreferenceDefault,
	store.RoutingPreferenceValue,
	store.RoutingPreferenceSpeed,
}

func buildCandidateExploration(config store.RoutingExplorationConfig, state store.RouteExplorationState, totalRequests int, lastUsedAt *time.Time, now time.Time) CandidateExploration {
	result := CandidateExploration{
		Enabled:    config.Enabled,
		Status:     "disabled",
		LastUsedAt: lastUsedAt,
	}
	if !config.Enabled {
		return result
	}

	cycleKind, cycleKey, target := explorationCycle(config, state, totalRequests, lastUsedAt, now)
	result.Mode = cycleKind
	result.CycleKey = cycleKey
	result.Target = target
	result.Attempts = state.Attempts
	result.LastAttemptAt = state.LastAttemptAt
	if state.CycleKind != cycleKind || state.CycleKey != cycleKey || state.Target != target {
		result.Attempts = 0
		result.LastAttemptAt = nil
	}
	if result.Attempts > target {
		result.Attempts = target
	}
	result.Remaining = maxInt(target-result.Attempts, 0)
	if target <= 0 {
		result.Status = "normal"
		return result
	}
	if result.Remaining == 0 {
		result.Status = "completed"
		return result
	}
	if state.CycleKind == cycleKind && state.CycleKey == cycleKey && state.Attempts > 0 {
		result.Status = "in_progress"
	} else {
		result.Status = "pending"
	}
	return result
}

func explorationCycle(config store.RoutingExplorationConfig, state store.RouteExplorationState, totalRequests int, lastUsedAt *time.Time, now time.Time) (string, string, int) {
	if config.ResetAt != nil {
		key := "reset:" + config.ResetAt.UTC().Format(time.RFC3339Nano)
		return store.RouteExplorationCycleReset, key, config.NewTrialsPerSite
	}
	if state.Attempts < state.Target {
		if state.CycleKind == store.RouteExplorationCycleIdle {
			return store.RouteExplorationCycleIdle, state.CycleKey, config.IdleTrialsPerSite
		}
		if state.CycleKind == store.RouteExplorationCycleInitial {
			return store.RouteExplorationCycleInitial, state.CycleKey, config.NewTrialsPerSite
		}
	}
	if totalRequests == 0 {
		return store.RouteExplorationCycleInitial, "initial", config.NewTrialsPerSite
	}
	if lastUsedAt != nil && !lastUsedAt.IsZero() && now.Sub(*lastUsedAt) >= time.Duration(config.IdleAfterHours)*time.Hour {
		key := "idle:" + lastUsedAt.UTC().Format(time.RFC3339Nano)
		return store.RouteExplorationCycleIdle, key, config.IdleTrialsPerSite
	}
	return "", "", 0
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
	if query.AllowExploration {
		items = s.reserveExplorationCandidate(ctx, result.CanonicalModel, items)
	}

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
	}, nil
}

func (s *Service) reserveExplorationCandidate(ctx context.Context, model store.CanonicalModel, items []Candidate) []Candidate {
	config := store.NormalizeRoutingExplorationConfig(model)
	if !config.Enabled || len(items) == 0 {
		return items
	}
	order := make([]int, 0, len(items))
	for index, item := range items {
		if item.Cooling || item.Exploration.Status == "normal" || item.Exploration.Status == "completed" || item.Exploration.Target <= 0 {
			continue
		}
		order = append(order, index)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return explorationCandidateComesBefore(items[order[i]], items[order[j]])
	})

	for _, index := range order {
		item := items[index]
		state, reserved, err := store.NewRouteExplorationRepository(s.db.DB()).Reserve(ctx, store.RouteExplorationReservationParams{
			CanonicalModelID: model.ID,
			SiteModelID:      item.Model.SiteModelID,
			CycleKind:        item.Exploration.Mode,
			CycleKey:         item.Exploration.CycleKey,
			Target:           item.Exploration.Target,
			Now:              time.Now(),
		})
		if err != nil || !reserved {
			continue
		}
		item.Exploration.Reserved = true
		item.Exploration.Attempts = state.Attempts
		item.Exploration.Remaining = maxInt(state.Target-state.Attempts, 0)
		item.Exploration.LastAttemptAt = state.LastAttemptAt
		item.Exploration.Status = "in_progress"
		if item.Exploration.Remaining == 0 {
			item.Exploration.Status = "completed"
		}
		if index == 0 {
			items[index] = item
			return items
		}
		result := make([]Candidate, 0, len(items))
		result = append(result, item)
		result = append(result, items[:index]...)
		result = append(result, items[index+1:]...)
		for rank := range result {
			result[rank].Rank = rank + 1
		}
		return result
	}
	return items
}

func explorationCandidateComesBefore(a, b Candidate) bool {
	aInProgress := a.Exploration.Attempts > 0 && a.Exploration.Attempts < a.Exploration.Target && a.Exploration.Remaining > 0
	bInProgress := b.Exploration.Attempts > 0 && b.Exploration.Attempts < b.Exploration.Target && b.Exploration.Remaining > 0
	if aInProgress != bInProgress {
		return aInProgress
	}
	if !aInProgress {
		// Candidates that have not started a cycle keep the score-derived order
		// produced by Candidates.  Stable sorting preserves that order.
		return false
	}

	// There can be more than one active cycle after upgrading from the old
	// round-robin behavior.  Finish the most advanced cycle first, then prefer
	// the cycle that was touched most recently.
	if a.Exploration.Attempts != b.Exploration.Attempts {
		return a.Exploration.Attempts > b.Exploration.Attempts
	}
	if a.Exploration.LastAttemptAt == nil && b.Exploration.LastAttemptAt != nil {
		return false
	}
	if a.Exploration.LastAttemptAt != nil && b.Exploration.LastAttemptAt == nil {
		return true
	}
	if a.Exploration.LastAttemptAt != nil && b.Exploration.LastAttemptAt != nil && !a.Exploration.LastAttemptAt.Equal(*b.Exploration.LastAttemptAt) {
		return a.Exploration.LastAttemptAt.After(*b.Exploration.LastAttemptAt)
	}
	return a.Score > b.Score
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
	if latency == nil {
		return 0
	}
	switch {
	case *latency <= 500:
		return maxScore
	case *latency <= 1500:
		return maxScore * 0.6
	case *latency <= 3000:
		return maxScore * 0.25
	default:
		return 0
	}
}

func firstByteLatencyScore(latency *int64, maxScore float64) float64 {
	if latency == nil {
		return 0
	}
	switch {
	case *latency <= 5000:
		return maxScore
	case *latency <= 8000:
		return maxScore * 0.8
	case *latency <= 12000:
		return maxScore * 0.5
	case *latency <= 20000:
		return maxScore * 0.2
	default:
		return 0
	}
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
