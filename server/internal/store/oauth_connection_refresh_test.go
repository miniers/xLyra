package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestOAuthConnectionRefreshCASIncludesExpectedRefreshTokenOffline(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	var captured map[string]any
	var where clause.Where
	storeReplaceUpdateCallback(t, db, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			tx.AddError(errors.New("unexpected codex refresh update destination"))
			return
		}
		captured = updates
		where, _ = tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
		tx.Statement.RowsAffected = 1
	})

	connection := OAuthConnection{
		ID:                    uuid.New(),
		Status:                "connected",
		EncryptedAccessToken:  "encrypted-access",
		MaskedAccessToken:     "masked-access",
		EncryptedRefreshToken: "encrypted-refresh-next",
		MaskedRefreshToken:    "masked-refresh-next",
		Metadata:              JSON(`{"kept":true}`),
	}
	matched, err := NewOAuthConnectionRepository(db).SaveCodexRefreshIfRefreshTokenMatches(context.Background(), connection, "encrypted-refresh-old")
	if err != nil {
		t.Fatalf("SaveCodexRefreshIfRefreshTokenMatches returned error: %v", err)
	}
	if !matched {
		t.Fatal("SaveCodexRefreshIfRefreshTokenMatches = false, want true")
	}
	if captured["encrypted_refresh_token"] != "encrypted-refresh-next" || captured["metadata"] == nil {
		t.Fatalf("captured updates = %#v, want rotated token and metadata", captured)
	}
	if len(where.Exprs) != 1 {
		t.Fatalf("where clause = %#v, want one conditional expression", where)
	}
	expr, ok := where.Exprs[0].(clause.Expr)
	if !ok || expr.SQL != "id = ? AND encrypted_refresh_token = ?" || len(expr.Vars) != 2 || expr.Vars[0] != connection.ID || expr.Vars[1] != "encrypted-refresh-old" {
		t.Fatalf("where expression = %#v, want id and expected refresh token", where.Exprs)
	}
}

func TestOAuthConnectionRefreshCASReturnsFalseWhenPredicateMissesOffline(t *testing.T) {
	t.Parallel()

	db := storeRepositoryOfflineGorm(t)
	storeReplaceUpdateCallback(t, db, func(tx *gorm.DB) {
		tx.Statement.RowsAffected = 0
	})

	matched, err := NewOAuthConnectionRepository(db).MarkCodexRefreshFailureIfRefreshTokenMatches(context.Background(), uuid.New(), "encrypted-refresh-old", JSON(`{"last_error":"refresh failed"}`))
	if err != nil {
		t.Fatalf("MarkCodexRefreshFailureIfRefreshTokenMatches returned error: %v", err)
	}
	if matched {
		t.Fatal("MarkCodexRefreshFailureIfRefreshTokenMatches = true, want false")
	}
}

func TestOAuthConnectionRefreshAdvisoryLockKeysAreStable(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	key1, key2 := oauthRefreshAdvisoryLockKeys(id)
	if key1 != int32(0x00112233) || key2 != int32(0x44556677) {
		t.Fatalf("advisory lock keys = (%d, %d), want (%d, %d)", key1, key2, int32(0x00112233), int32(0x44556677))
	}
}

func TestOAuthConnectionRefreshLockValidatesInputsOffline(t *testing.T) {
	t.Parallel()

	if err := (OAuthConnectionRepository{}).WithRefreshLock(context.Background(), uuid.New(), func(OAuthConnectionRepository) error { return nil }); err == nil {
		t.Fatal("nil repository should reject refresh lock")
	}
	db := storeRepositoryOfflineGorm(t)
	repo := NewOAuthConnectionRepository(db)
	if err := repo.WithRefreshLock(context.Background(), uuid.Nil, func(OAuthConnectionRepository) error { return nil }); err == nil {
		t.Fatal("nil connection id should reject refresh lock")
	}
	if err := repo.WithRefreshLock(context.Background(), uuid.New(), nil); err == nil {
		t.Fatal("nil refresh lock callback should reject refresh lock")
	}
}
