package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RouteScoreAdjustmentStep     = 5.0
	RouteScoreAdjustmentDuration = 10 * time.Minute
)

// RouteScoreAdjustment stores the current temporary score adjustment for one
// canonical-model × site-model pair. A zero delta is retained so repeated
// opposite clicks can safely use the same row; active reads ignore it.
type RouteScoreAdjustment struct {
	ID               uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CanonicalModelID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:route_score_adjustments_pair_idx,priority:1;index:route_score_adjustments_model_idx"`
	SiteModelID      uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:route_score_adjustments_pair_idx,priority:2"`
	Delta            float64   `gorm:"type:numeric(8,2);not null;default:0"`
	ActiveUntil      time.Time `gorm:"not null;index:route_score_adjustments_active_idx,sort:desc"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RouteScoreAdjustmentRepository struct {
	db *gorm.DB
}

func NewRouteScoreAdjustmentRepository(db *gorm.DB) RouteScoreAdjustmentRepository {
	return RouteScoreAdjustmentRepository{db: db}
}

func (r RouteScoreAdjustmentRepository) ListActiveByCanonicalModel(ctx context.Context, canonicalModelID uuid.UUID, now time.Time) ([]RouteScoreAdjustment, error) {
	if canonicalModelID == uuid.Nil {
		return nil, fmt.Errorf("canonical_model_id is required")
	}
	if now.IsZero() {
		now = time.Now()
	}

	var items []RouteScoreAdjustment
	if err := r.db.WithContext(ctx).
		Where("canonical_model_id = ? AND delta <> 0 AND active_until > ?", canonicalModelID, now).
		Order("active_until DESC").
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list active route score adjustments: %w", err)
	}
	return items, nil
}

func (r RouteScoreAdjustmentRepository) Apply(ctx context.Context, canonicalModelID, siteModelID uuid.UUID, delta float64, now time.Time) (RouteScoreAdjustment, error) {
	if canonicalModelID == uuid.Nil || siteModelID == uuid.Nil {
		return RouteScoreAdjustment{}, fmt.Errorf("canonical_model_id and site_model_id are required")
	}
	if delta != RouteScoreAdjustmentStep && delta != -RouteScoreAdjustmentStep {
		return RouteScoreAdjustment{}, fmt.Errorf("route score adjustment must be %+g or %+g", RouteScoreAdjustmentStep, -RouteScoreAdjustmentStep)
	}
	if now.IsZero() {
		now = time.Now()
	}

	var item RouteScoreAdjustment
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := RouteScoreAdjustment{
			ID:               uuid.New(),
			CanonicalModelID: canonicalModelID,
			SiteModelID:      siteModelID,
			ActiveUntil:      now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return fmt.Errorf("seed route score adjustment: %w", err)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("canonical_model_id = ? AND site_model_id = ?", canonicalModelID, siteModelID).
			First(&item).Error; err != nil {
			return fmt.Errorf("lock route score adjustment: %w", err)
		}

		item = applyRouteScoreAdjustment(item, delta, now)
		if err := tx.Save(&item).Error; err != nil {
			return fmt.Errorf("save route score adjustment: %w", err)
		}
		return nil
	})
	if err != nil {
		return RouteScoreAdjustment{}, err
	}
	return item, nil
}

func applyRouteScoreAdjustment(previous RouteScoreAdjustment, delta float64, now time.Time) RouteScoreAdjustment {
	if !previous.ActiveUntil.After(now) {
		previous.Delta = 0
	}
	previous.Delta += delta
	previous.ActiveUntil = now.Add(RouteScoreAdjustmentDuration)
	return previous
}
