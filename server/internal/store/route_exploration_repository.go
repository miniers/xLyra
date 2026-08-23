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
	RouteExplorationCycleInitial = "initial"
	RouteExplorationCycleIdle    = "idle"
	RouteExplorationCycleReset   = "reset"
)

// RouteExplorationState is the durable cursor for one canonical-model ×
// site-model pair.  A cycle key changes on reset or when a previously used
// pair becomes idle again; keeping the cursor in the database makes multiple
// gateway workers share the quota safely.
type RouteExplorationState struct {
	ID               uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CanonicalModelID uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:route_exploration_state_pair_idx,priority:1;index:route_exploration_states_model_idx"`
	SiteModelID      uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:route_exploration_state_pair_idx,priority:2"`
	CycleKind        string     `gorm:"not null"`
	CycleKey         string     `gorm:"not null"`
	Attempts         int        `gorm:"not null;default:0"`
	Target           int        `gorm:"not null;default:0"`
	LastAttemptAt    *time.Time `gorm:"index:route_exploration_states_last_attempt_idx,sort:desc"`
	CompletedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RouteExplorationReservationParams struct {
	CanonicalModelID uuid.UUID
	SiteModelID      uuid.UUID
	CycleKind        string
	CycleKey         string
	Target           int
	Now              time.Time
}

type RouteExplorationRepository struct {
	db *gorm.DB
}

func NewRouteExplorationRepository(db *gorm.DB) RouteExplorationRepository {
	return RouteExplorationRepository{db: db}
}

func (r RouteExplorationRepository) ListByCanonicalModel(ctx context.Context, canonicalModelID uuid.UUID) ([]RouteExplorationState, error) {
	var items []RouteExplorationState
	if err := r.db.WithContext(ctx).Where("canonical_model_id = ?", canonicalModelID).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list route exploration states: %w", err)
	}
	return items, nil
}

// Reserve atomically advances a pair's cursor.  It returns false when the
// current cycle has consumed its target; callers should then use the regular
// score ordering.  Reservation failures are returned to the caller so the
// gateway can fail safe to normal routing.
func (r RouteExplorationRepository) Reserve(ctx context.Context, params RouteExplorationReservationParams) (RouteExplorationState, bool, error) {
	if params.CanonicalModelID == uuid.Nil || params.SiteModelID == uuid.Nil {
		return RouteExplorationState{}, false, fmt.Errorf("canonical_model_id and site_model_id are required")
	}
	if params.CycleKind == "" || params.CycleKey == "" {
		return RouteExplorationState{}, false, fmt.Errorf("exploration cycle kind and key are required")
	}
	if params.Target < 1 {
		return RouteExplorationState{}, false, fmt.Errorf("exploration target must be positive")
	}
	if params.Now.IsZero() {
		params.Now = time.Now()
	}

	var state RouteExplorationState
	reserved := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := RouteExplorationState{
			ID:               uuid.New(),
			CanonicalModelID: params.CanonicalModelID,
			SiteModelID:      params.SiteModelID,
			CycleKind:        params.CycleKind,
			CycleKey:         params.CycleKey,
			Target:           params.Target,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return fmt.Errorf("seed route exploration state: %w", err)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("canonical_model_id = ? AND site_model_id = ?", params.CanonicalModelID, params.SiteModelID).First(&state).Error; err != nil {
			return fmt.Errorf("lock route exploration state: %w", err)
		}

		if state.CycleKey != params.CycleKey || state.CycleKind != params.CycleKind || state.Target != params.Target {
			state.CycleKind = params.CycleKind
			state.CycleKey = params.CycleKey
			state.Attempts = 0
			state.Target = params.Target
			state.LastAttemptAt = nil
			state.CompletedAt = nil
		}
		if state.Attempts >= state.Target {
			return nil
		}

		state.Attempts++
		now := params.Now
		state.LastAttemptAt = &now
		if state.Attempts >= state.Target {
			state.CompletedAt = &now
		}
		if err := tx.Save(&state).Error; err != nil {
			return fmt.Errorf("advance route exploration state: %w", err)
		}
		reserved = true
		return nil
	})
	if err != nil {
		return RouteExplorationState{}, false, err
	}
	return state, reserved, nil
}
