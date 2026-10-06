package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PatchMeta merges top-level namespaces without replacing concurrently edited
// credential settings or saving a stale copy of the encrypted secret.
func (r SiteCredentialRepository) PatchMeta(ctx context.Context, id uuid.UUID, patch JSON) (SiteCredential, error) {
	result := r.db.WithContext(ctx).Model(&SiteCredential{ID: id}).Updates(map[string]any{
		"meta": gorm.Expr("COALESCE(meta, '{}'::jsonb) || ?::jsonb", string(patch)),
	})
	if result.Error != nil {
		return SiteCredential{}, fmt.Errorf("patch site credential meta: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return SiteCredential{}, fmt.Errorf("patch site credential meta: %w", gorm.ErrRecordNotFound)
	}
	return r.GetByID(ctx, id)
}
