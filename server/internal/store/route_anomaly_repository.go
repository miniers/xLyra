package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	routeAnomalyEvaluationInterval = 30 * time.Second
	routeAnomalyRecentWindow       = 10 * time.Minute
	routeAnomalyBaselineWindow     = 24 * time.Hour
	routeAnomalySustainWindow      = 5 * time.Minute
	routeAnomalyRecoveryWindow     = 5 * time.Minute
	routeAnomalyActiveWindow       = 15 * time.Minute
	routeAnomalyMinSamples         = 5
	routeAnomalyMaxPenalty         = -30.0
)

// RouteAnomalyState is the durable hysteresis state for one canonical-model ×
// site-model pair. Detection is intentionally independent from cooldowns: an
// anomaly reduces score temporarily, while a cooldown removes a route from
// consideration altogether.
type RouteAnomalyState struct {
	ID               uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CanonicalModelID uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:route_anomaly_states_pair_idx,priority:1;index:route_anomaly_states_model_idx"`
	SiteModelID      uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:route_anomaly_states_pair_idx,priority:2"`
	Active           bool       `gorm:"not null;default:false"`
	Penalty          float64    `gorm:"not null;default:0"`
	Metrics          JSON       `gorm:"type:jsonb;not null;default:'[]'::jsonb"`
	BadSince         *time.Time `gorm:"index:route_anomaly_states_bad_since_idx"`
	GoodSince        *time.Time
	ActiveUntil      *time.Time `gorm:"index:route_anomaly_states_active_idx,sort:desc"`
	LastEvaluatedAt  *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (RouteAnomalyState) TableName() string {
	return "route_anomaly_states"
}

// RouteAnomalyMetric is shown in the score detail when a route is actively
// penalized. RecentValue and BaselineValue retain the raw values that led to
// the penalty so the score is explainable.
type RouteAnomalyMetric struct {
	Metric          string  `json:"metric"`
	Penalty         float64 `json:"penalty"`
	RecentValue     float64 `json:"recent_value"`
	BaselineValue   float64 `json:"baseline_value"`
	RecentSamples   int     `json:"recent_samples"`
	BaselineSamples int     `json:"baseline_samples"`
	Reason          string  `json:"reason"`
}

type RouteAnomaly struct {
	Active      bool                 `json:"active"`
	Penalty     float64              `json:"penalty"`
	Metrics     []RouteAnomalyMetric `json:"metrics"`
	ActiveUntil *time.Time           `json:"active_until,omitempty"`
}

type routeAnomalyWindowMetric struct {
	RecentValue     float64
	BaselineValue   float64
	RecentSamples   int
	BaselineSamples int
	Ready           bool
}

type routeAnomalyObservation struct {
	FirstByteLatency routeAnomalyWindowMetric
	CacheHitRate     routeAnomalyWindowMetric
}

type RouteAnomalyRepository struct {
	db *gorm.DB
}

func NewRouteAnomalyRepository(db *gorm.DB) RouteAnomalyRepository {
	return RouteAnomalyRepository{db: db}
}

func (r RouteAnomalyRepository) ListByCanonicalModel(ctx context.Context, canonicalModelID uuid.UUID) ([]RouteAnomalyState, error) {
	var items []RouteAnomalyState
	if err := r.db.WithContext(ctx).Where("canonical_model_id = ?", canonicalModelID).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list route anomaly states: %w", err)
	}
	return items, nil
}

func (r RouteAnomalyRepository) ListActive(ctx context.Context, now time.Time) ([]RouteAnomalyState, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var items []RouteAnomalyState
	if err := r.db.WithContext(ctx).
		Where("active = ? AND penalty < 0 AND (active_until IS NULL OR active_until > ?)", true, now).
		Order("penalty ASC, active_until ASC").
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list active route anomaly states: %w", err)
	}
	return items, nil
}

// ClearActive removes the current penalty for one canonical-model × site-model
// pair. The evaluation timestamp is advanced so a manual clear is not
// immediately replaced by the same stale observation on the next candidate
// read.
func (r RouteAnomalyRepository) ClearActive(ctx context.Context, canonicalModelID, siteModelID uuid.UUID) error {
	if canonicalModelID == uuid.Nil || siteModelID == uuid.Nil {
		return fmt.Errorf("canonical_model_id and site_model_id are required")
	}

	now := time.Now()
	if err := r.db.WithContext(ctx).
		Model(&RouteAnomalyState{}).
		Where("canonical_model_id = ? AND site_model_id = ? AND active = ? AND penalty < ?", canonicalModelID, siteModelID, true, 0).
		Updates(map[string]any{
			"active":            false,
			"penalty":           0,
			"metrics":           JSON("[]"),
			"bad_since":         nil,
			"good_since":        nil,
			"active_until":      nil,
			"last_evaluated_at": now,
			"updated_at":        now,
		}).Error; err != nil {
		return fmt.Errorf("clear active route anomaly: %w", err)
	}
	return nil
}

func (r RouteAnomalyRepository) ClaimEvaluation(ctx context.Context, canonicalModelID, siteModelID uuid.UUID, now time.Time) (bool, error) {
	if canonicalModelID == uuid.Nil || siteModelID == uuid.Nil {
		return false, fmt.Errorf("canonical_model_id and site_model_id are required")
	}
	if now.IsZero() {
		now = time.Now()
	}

	insert := r.db.WithContext(ctx).Exec(`
		INSERT INTO route_anomaly_states
			(id, canonical_model_id, site_model_id, active, penalty, metrics, last_evaluated_at, created_at, updated_at)
		VALUES (?, ?, ?, FALSE, 0, '[]'::jsonb, ?, ?, ?)
		ON CONFLICT (canonical_model_id, site_model_id) DO NOTHING
	`, uuid.New(), canonicalModelID, siteModelID, now, now, now)
	if insert.Error != nil {
		return false, fmt.Errorf("claim route anomaly evaluation: %w", insert.Error)
	}
	if insert.RowsAffected == 1 {
		return true, nil
	}

	result := r.db.WithContext(ctx).
		Model(&RouteAnomalyState{}).
		Where("canonical_model_id = ? AND site_model_id = ?", canonicalModelID, siteModelID).
		Where("last_evaluated_at IS NULL OR last_evaluated_at <= ?", now.Add(-routeAnomalyEvaluationInterval)).
		Updates(map[string]any{
			"last_evaluated_at": now,
			"updated_at":        now,
		})
	if result.Error != nil {
		return false, fmt.Errorf("claim route anomaly evaluation: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (r RouteAnomalyRepository) SaveEvaluation(ctx context.Context, state RouteAnomalyState) error {
	if state.CanonicalModelID == uuid.Nil || state.SiteModelID == uuid.Nil {
		return fmt.Errorf("canonical_model_id and site_model_id are required")
	}
	if state.ID == uuid.Nil {
		state.ID = uuid.New()
	}
	if len(state.Metrics) == 0 {
		state.Metrics = JSON("[]")
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "canonical_model_id"},
			{Name: "site_model_id"},
		},
		Where: clause.Where{
			Exprs: []clause.Expression{
				clause.Expr{SQL: "route_anomaly_states.updated_at IS NULL OR route_anomaly_states.updated_at <= EXCLUDED.updated_at"},
			},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"active",
			"penalty",
			"metrics",
			"bad_since",
			"good_since",
			"active_until",
			"last_evaluated_at",
			"updated_at",
		}),
	}).Create(&state).Error; err != nil {
		return fmt.Errorf("save route anomaly state: %w", err)
	}
	return nil
}

func evaluateRouteAnomalyState(previous RouteAnomalyState, observation routeAnomalyObservation, now time.Time) RouteAnomalyState {
	if now.IsZero() {
		now = time.Now()
	}
	if previous.ID == uuid.Nil {
		previous.ID = uuid.New()
	}
	if previous.Metrics == nil {
		previous.Metrics = JSON("[]")
	}

	metrics := detectRouteAnomalyMetrics(observation)
	penalty := routeAnomalyPenalty(metrics)
	previous.LastEvaluatedAt = timePointer(now)

	switch {
	case penalty < 0:
		previous.GoodSince = nil
		previous.Metrics = routeAnomalyMetricsJSON(metrics)
		if previous.Active {
			previous.Penalty = penalty
			previous.ActiveUntil = timePointer(now.Add(routeAnomalyActiveWindow))
			previous.BadSince = nil
			return previous
		}
		if previous.BadSince == nil {
			previous.BadSince = timePointer(now)
		}
		if now.Sub(*previous.BadSince) >= routeAnomalySustainWindow {
			previous.Active = true
			previous.Penalty = penalty
			previous.ActiveUntil = timePointer(now.Add(routeAnomalyActiveWindow))
			previous.BadSince = nil
			return previous
		}
		previous.Active = false
		previous.Penalty = 0
		previous.ActiveUntil = nil
		return previous

	case !observationHasData(observation):
		// Do not count a quiet route as recovered. If traffic stops, keep the
		// last penalty for its bounded active window and re-evaluate on the next
		// request with fresh data.
		if previous.Active && (previous.ActiveUntil == nil || previous.ActiveUntil.After(now)) {
			return previous
		}
		return clearRouteAnomalyState(previous)

	case previous.Active:
		if previous.GoodSince == nil {
			previous.GoodSince = timePointer(now)
		}
		if now.Sub(*previous.GoodSince) >= routeAnomalyRecoveryWindow || (previous.ActiveUntil != nil && !previous.ActiveUntil.After(now)) {
			return clearRouteAnomalyState(previous)
		}
		return previous

	default:
		previous.Active = false
		previous.Penalty = 0
		previous.BadSince = nil
		previous.GoodSince = nil
		previous.ActiveUntil = nil
		previous.Metrics = JSON("[]")
		return previous
	}
}

func detectRouteAnomalyMetrics(observation routeAnomalyObservation) []RouteAnomalyMetric {
	metrics := make([]RouteAnomalyMetric, 0, 2)
	if observation.FirstByteLatency.Ready {
		ratio := safeRatio(observation.FirstByteLatency.RecentValue, observation.FirstByteLatency.BaselineValue)
		delta := observation.FirstByteLatency.RecentValue - observation.FirstByteLatency.BaselineValue
		penalty := 0.0
		reason := ""
		switch {
		case ratio >= 2 && delta >= 500:
			penalty = -20
			reason = "recent first-byte latency is at least 2x baseline and 500ms slower"
		case ratio >= 1.5 && delta >= 500:
			penalty = -10
			reason = "recent first-byte latency is at least 1.5x baseline and 500ms slower"
		}
		if penalty < 0 {
			metrics = append(metrics, RouteAnomalyMetric{
				Metric:          "first_byte_latency",
				Penalty:         penalty,
				RecentValue:     observation.FirstByteLatency.RecentValue,
				BaselineValue:   observation.FirstByteLatency.BaselineValue,
				RecentSamples:   observation.FirstByteLatency.RecentSamples,
				BaselineSamples: observation.FirstByteLatency.BaselineSamples,
				Reason:          reason,
			})
		}
	}

	if observation.CacheHitRate.Ready {
		drop := observation.CacheHitRate.BaselineValue - observation.CacheHitRate.RecentValue
		relativeDrop := safeRatio(drop, observation.CacheHitRate.BaselineValue)
		penalty := 0.0
		reason := ""
		switch {
		case drop >= 0.35 || relativeDrop >= 0.5:
			penalty = -10
			reason = "recent cache hit rate dropped by at least 35 percentage points or 50%"
		case drop >= 0.2 || relativeDrop >= 0.4:
			penalty = -5
			reason = "recent cache hit rate dropped by at least 20 percentage points or 40%"
		}
		if penalty < 0 {
			metrics = append(metrics, RouteAnomalyMetric{
				Metric:          "cache_hit_rate",
				Penalty:         penalty,
				RecentValue:     observation.CacheHitRate.RecentValue,
				BaselineValue:   observation.CacheHitRate.BaselineValue,
				RecentSamples:   observation.CacheHitRate.RecentSamples,
				BaselineSamples: observation.CacheHitRate.BaselineSamples,
				Reason:          reason,
			})
		}
	}

	return metrics
}

func routeAnomalyPenalty(metrics []RouteAnomalyMetric) float64 {
	penalty := 0.0
	for _, metric := range metrics {
		if metric.Penalty < 0 {
			penalty += metric.Penalty
		}
	}
	if penalty < routeAnomalyMaxPenalty {
		return routeAnomalyMaxPenalty
	}
	return penalty
}

func routeAnomalyFromState(state RouteAnomalyState, now time.Time) RouteAnomaly {
	if !state.Active || state.Penalty >= 0 || (state.ActiveUntil != nil && !state.ActiveUntil.After(now)) {
		return RouteAnomaly{}
	}
	var metrics []RouteAnomalyMetric
	if len(state.Metrics) > 0 {
		_ = json.Unmarshal(state.Metrics, &metrics)
	}
	return RouteAnomaly{
		Active:      true,
		Penalty:     state.Penalty,
		Metrics:     metrics,
		ActiveUntil: state.ActiveUntil,
	}
}

func routeAnomalyMetricsJSON(metrics []RouteAnomalyMetric) JSON {
	data, err := json.Marshal(metrics)
	if err != nil {
		return JSON("[]")
	}
	return JSON(data)
}

func clearRouteAnomalyState(state RouteAnomalyState) RouteAnomalyState {
	state.Active = false
	state.Penalty = 0
	state.BadSince = nil
	state.GoodSince = nil
	state.ActiveUntil = nil
	state.Metrics = JSON("[]")
	return state
}

func observationHasData(observation routeAnomalyObservation) bool {
	return observation.FirstByteLatency.Ready || observation.CacheHitRate.Ready
}

func safeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 || math.IsNaN(numerator) || math.IsNaN(denominator) || math.IsInf(numerator, 0) || math.IsInf(denominator, 0) {
		return 0
	}
	return numerator / denominator
}

func timePointer(value time.Time) *time.Time {
	return &value
}
