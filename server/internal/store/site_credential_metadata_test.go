package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestPatchCredentialMetaBuildsAtomicScopedMerge(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	db := storeRepositoryOfflineGorm(t).Session(&gorm.Session{DryRun: true})
	if err := db.Callback().Update().After("gorm:update").Register("test:capture_meta_patch", func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if !strings.Contains(sql, "COALESCE(meta, '{}'::jsonb) ||") || !strings.Contains(sql, `WHERE "id"`) || strings.Contains(sql, "encrypted_secret") {
			t.Fatalf("metadata patch SQL = %s", sql)
		}
		if tx.Statement.Model.(*SiteCredential).ID != id {
			t.Fatal("metadata patch targeted another credential")
		}
		tx.RowsAffected = 1
	}); err != nil {
		t.Fatal(err)
	}
	storeReplaceQueryCallback(t, db, func(tx *gorm.DB) {
		*tx.Statement.Dest.(*SiteCredential) = SiteCredential{ID: id, Meta: JSON(`{"manual":"retained","factory_identity":{"region":"eu"}}`)}
		tx.RowsAffected = 1
	})
	credential, err := NewSiteCredentialRepository(db).PatchMeta(t.Context(), id, JSON(`{"factory_identity":{"region":"eu"}}`))
	if err != nil || credential.ID != id || !strings.Contains(string(credential.Meta), "retained") {
		t.Fatalf("patched credential = %#v, %v", credential, err)
	}
}

func TestPatchCredentialMetaReportsDeletedCredential(t *testing.T) {
	t.Parallel()
	db := storeRepositoryOfflineGorm(t)
	storeReplaceUpdateCallback(t, db, func(tx *gorm.DB) { tx.RowsAffected = 0 })
	_, err := NewSiteCredentialRepository(db).PatchMeta(t.Context(), uuid.New(), JSON(`{"factory_identity":{}}`))
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("metadata patch error = %v, want missing credential", err)
	}
}
