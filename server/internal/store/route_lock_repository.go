package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RouteLockPolicyStrict   = "strict"
	RouteLockPolicyFallback = "fallback"
)

// RouteLock pins a canonical model to one site-model while the record is
// active. It is intentionally separate from CanonicalModel because it is an
// operational, usually short-lived routing override.
type RouteLock struct {
	ID               uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	CanonicalModelID uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:route_locks_canonical_model_unique_idx;index:route_locks_canonical_model_idx"`
	SiteModelID      uuid.UUID  `gorm:"type:uuid;not null;index:route_locks_site_model_idx"`
	ExpiresAt        *time.Time `gorm:"index:route_locks_expires_at_idx,sort:desc"`
	Policy           string     `gorm:"not null;default:'strict'"`
	Reason           string     `gorm:"not null;default:''"`
	CreatedByAdminID *uuid.UUID `gorm:"type:uuid"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (RouteLock) TableName() string {
	return "route_locks"
}

type UpsertRouteLockParams struct {
	CanonicalModelID uuid.UUID
	SiteModelID      uuid.UUID
	ExpiresAt        *time.Time
	Policy           string
	Reason           string
	CreatedByAdminID *uuid.UUID
}

type RouteLockRepository struct {
	db *gorm.DB
}

func NewRouteLockRepository(db *gorm.DB) RouteLockRepository {
	return RouteLockRepository{db: db}
}

func NormalizeRouteLockPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RouteLockPolicyFallback:
		return RouteLockPolicyFallback
	default:
		return RouteLockPolicyStrict
	}
}

func ValidRouteLockPolicy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RouteLockPolicyStrict, RouteLockPolicyFallback:
		return true
	default:
		return false
	}
}

func RouteLockIsActive(item RouteLock, now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	return item.ExpiresAt == nil || item.ExpiresAt.After(now)
}

func (r RouteLockRepository) GetByCanonicalModel(ctx context.Context, canonicalModelID uuid.UUID) (RouteLock, error) {
	if canonicalModelID == uuid.Nil {
		return RouteLock{}, fmt.Errorf("canonical_model_id is required")
	}
	if r.db == nil {
		return RouteLock{}, fmt.Errorf("get route lock: store is not initialized")
	}

	var item RouteLock
	if err := r.db.WithContext(ctx).
		Where("canonical_model_id = ?", canonicalModelID).
		First(&item).Error; err != nil {
		return RouteLock{}, fmt.Errorf("get route lock: %w", err)
	}
	return item, nil
}

func (r RouteLockRepository) GetActiveByCanonicalModel(ctx context.Context, canonicalModelID uuid.UUID, now time.Time) (RouteLock, error) {
	if canonicalModelID == uuid.Nil {
		return RouteLock{}, fmt.Errorf("canonical_model_id is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if r.db == nil {
		return RouteLock{}, fmt.Errorf("get active route lock: store is not initialized")
	}

	var item RouteLock
	if err := r.db.WithContext(ctx).
		Where("canonical_model_id = ? AND (expires_at IS NULL OR expires_at > ?)", canonicalModelID, now).
		First(&item).Error; err != nil {
		return RouteLock{}, fmt.Errorf("get active route lock: %w", err)
	}
	return item, nil
}

func (r RouteLockRepository) Upsert(ctx context.Context, params UpsertRouteLockParams) (RouteLock, error) {
	if params.CanonicalModelID == uuid.Nil {
		return RouteLock{}, fmt.Errorf("canonical_model_id is required")
	}
	if params.SiteModelID == uuid.Nil {
		return RouteLock{}, fmt.Errorf("site_model_id is required")
	}
	now := time.Now()
	if params.ExpiresAt != nil {
		if params.ExpiresAt.IsZero() || !params.ExpiresAt.After(now) {
			return RouteLock{}, fmt.Errorf("expires_at must be in the future")
		}
		expiresAt := params.ExpiresAt.UTC()
		params.ExpiresAt = &expiresAt
	}
	if strings.TrimSpace(params.Policy) == "" {
		params.Policy = RouteLockPolicyStrict
	}
	if !ValidRouteLockPolicy(params.Policy) {
		return RouteLock{}, fmt.Errorf("policy must be strict or fallback")
	}
	params.Policy = NormalizeRouteLockPolicy(params.Policy)
	params.Reason = strings.TrimSpace(params.Reason)
	if len(params.Reason) > 500 {
		return RouteLock{}, fmt.Errorf("reason must be at most 500 characters")
	}
	if params.CreatedByAdminID != nil && *params.CreatedByAdminID == uuid.Nil {
		params.CreatedByAdminID = nil
	}
	if r.db == nil {
		return RouteLock{}, fmt.Errorf("upsert route lock: store is not initialized")
	}

	// Validate the relation before writing. A site-model can be remapped after a
	// lock was created, so this check is intentionally performed on every update.
	var target SiteModel
	if err := r.db.WithContext(ctx).
		Where("id = ? AND canonical_model_id = ?", params.SiteModelID, params.CanonicalModelID).
		First(&target).Error; err != nil {
		return RouteLock{}, fmt.Errorf("validate route lock target: %w", err)
	}

	item := RouteLock{
		ID:               uuid.New(),
		CanonicalModelID: params.CanonicalModelID,
		SiteModelID:      params.SiteModelID,
		ExpiresAt:        params.ExpiresAt,
		Policy:           params.Policy,
		Reason:           params.Reason,
		CreatedByAdminID: params.CreatedByAdminID,
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "canonical_model_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"site_model_id",
			"expires_at",
			"policy",
			"reason",
			"created_by_admin_id",
			"updated_at",
		}),
	}).Create(&item).Error; err != nil {
		return RouteLock{}, fmt.Errorf("upsert route lock: %w", err)
	}

	result, err := r.GetByCanonicalModel(ctx, params.CanonicalModelID)
	if err != nil {
		return RouteLock{}, err
	}
	return result, nil
}

func (r RouteLockRepository) Delete(ctx context.Context, canonicalModelID uuid.UUID) error {
	if canonicalModelID == uuid.Nil {
		return fmt.Errorf("canonical_model_id is required")
	}
	if r.db == nil {
		return fmt.Errorf("delete route lock: store is not initialized")
	}
	if err := r.db.WithContext(ctx).
		Where("canonical_model_id = ?", canonicalModelID).
		Delete(&RouteLock{}).Error; err != nil {
		return fmt.Errorf("delete route lock: %w", err)
	}
	return nil
}
