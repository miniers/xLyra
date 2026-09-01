package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type OAuthConnection struct {
	ID                    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Provider              string    `gorm:"uniqueIndex:oauth_connections_provider_email_unique,priority:1"`
	SiteID                *uuid.UUID
	Status                string
	AccountID             string
	Email                 string `gorm:"uniqueIndex:oauth_connections_provider_email_unique,priority:2"`
	EncryptedAccessToken  string
	MaskedAccessToken     string
	EncryptedRefreshToken string
	MaskedRefreshToken    string
	EncryptedIDToken      string
	MaskedIDToken         string
	TokenType             string
	Scopes                string
	ExpiresAt             sql.NullTime
	LastRefreshAt         sql.NullTime
	LastSyncAt            sql.NullTime
	RawProfile            JSON `gorm:"type:jsonb"`
	Metadata              JSON `gorm:"type:jsonb"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type UpsertOAuthConnectionParams struct {
	Provider              string
	SiteID                uuid.UUID
	Status                string
	AccountID             string
	Email                 string
	EncryptedAccessToken  string
	MaskedAccessToken     string
	EncryptedRefreshToken string
	MaskedRefreshToken    string
	EncryptedIDToken      string
	MaskedIDToken         string
	TokenType             string
	Scopes                string
	ExpiresAt             any
	LastRefreshAt         any
	LastSyncAt            any
	RawProfile            JSON
	Metadata              JSON
}

type OAuthConnectionRepository struct {
	db *gorm.DB
}

func NewOAuthConnectionRepository(db *gorm.DB) OAuthConnectionRepository {
	return OAuthConnectionRepository{db: db}
}

func (r OAuthConnectionRepository) UpsertByProviderEmail(ctx context.Context, params UpsertOAuthConnectionParams) (OAuthConnection, error) {
	provider := strings.TrimSpace(params.Provider)
	email := strings.TrimSpace(params.Email)
	if email == "" {
		return OAuthConnection{}, fmt.Errorf("upsert oauth connection: email is required")
	}

	var connection OAuthConnection
	db := r.db.WithContext(ctx)
	err := db.Where(&OAuthConnection{Provider: provider, Email: email}).First(&connection).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return OAuthConnection{}, fmt.Errorf("upsert oauth connection: %w", err)
	}
	connection.Provider = provider
	if params.SiteID != uuid.Nil {
		siteID := params.SiteID
		connection.SiteID = &siteID
	} else if err == gorm.ErrRecordNotFound {
		connection.SiteID = nil
	}
	connection.Status = defaultOAuthConnectionString(params.Status, "connected")
	connection.AccountID = params.AccountID
	connection.Email = email
	connection.EncryptedAccessToken = params.EncryptedAccessToken
	connection.MaskedAccessToken = params.MaskedAccessToken
	connection.EncryptedRefreshToken = params.EncryptedRefreshToken
	connection.MaskedRefreshToken = params.MaskedRefreshToken
	connection.EncryptedIDToken = params.EncryptedIDToken
	connection.MaskedIDToken = params.MaskedIDToken
	connection.TokenType = defaultOAuthConnectionString(params.TokenType, "Bearer")
	connection.Scopes = params.Scopes
	connection.ExpiresAt = nullTimeFromAny(params.ExpiresAt)
	connection.LastRefreshAt = nullTimeFromAny(params.LastRefreshAt)
	connection.LastSyncAt = nullTimeFromAny(params.LastSyncAt)
	connection.RawProfile = jsonDefault(params.RawProfile, "{}")
	connection.Metadata = jsonDefault(params.Metadata, "{}")
	if err == gorm.ErrRecordNotFound {
		if err := db.Create(&connection).Error; err != nil {
			return OAuthConnection{}, fmt.Errorf("upsert oauth connection: %w", err)
		}
		return connection, nil
	}
	if err := db.Save(&connection).Error; err != nil {
		return OAuthConnection{}, fmt.Errorf("upsert oauth connection: %w", err)
	}
	return connection, nil
}

func (r OAuthConnectionRepository) GetByID(ctx context.Context, id uuid.UUID) (OAuthConnection, error) {
	var connection OAuthConnection
	if err := r.db.WithContext(ctx).Where(&OAuthConnection{ID: id}).First(&connection).Error; err != nil {
		return OAuthConnection{}, fmt.Errorf("get oauth connection: %w", err)
	}
	return connection, nil
}

func (r OAuthConnectionRepository) GetBySiteID(ctx context.Context, siteID uuid.UUID) (OAuthConnection, error) {
	var connections []OAuthConnection
	if err := r.db.WithContext(ctx).Find(&connections).Error; err != nil {
		return OAuthConnection{}, fmt.Errorf("get oauth connection by site: %w", err)
	}
	for _, connection := range connections {
		if connection.SiteID != nil && *connection.SiteID == siteID {
			return connection, nil
		}
	}
	return OAuthConnection{}, fmt.Errorf("get oauth connection by site: %w", gorm.ErrRecordNotFound)
}

func (r OAuthConnectionRepository) List(ctx context.Context) ([]OAuthConnection, error) {
	var items []OAuthConnection
	if err := r.db.WithContext(ctx).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list oauth connections: %w", err)
	}
	filtered := items[:0]
	for _, item := range items {
		if item.SiteID != nil {
			filtered = append(filtered, item)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
		}
		return filtered[i].ID.String() < filtered[j].ID.String()
	})
	return filtered, nil
}

func (r OAuthConnectionRepository) DeleteBySiteID(ctx context.Context, siteID uuid.UUID) error {
	if siteID == uuid.Nil {
		return nil
	}
	var connections []OAuthConnection
	if err := r.db.WithContext(ctx).Find(&connections).Error; err != nil {
		return fmt.Errorf("delete oauth connections by site: %w", err)
	}
	for _, connection := range connections {
		if connection.SiteID == nil || *connection.SiteID != siteID {
			continue
		}
		if err := r.db.WithContext(ctx).Delete(&connection).Error; err != nil {
			return fmt.Errorf("delete oauth connections by site: %w", err)
		}
	}
	return nil
}

func (r OAuthConnectionRepository) Save(ctx context.Context, connection OAuthConnection) (OAuthConnection, error) {
	if err := r.db.WithContext(ctx).Save(&connection).Error; err != nil {
		return OAuthConnection{}, fmt.Errorf("save oauth connection: %w", err)
	}
	return connection, nil
}

// WithRefreshLock keeps one PostgreSQL session pinned while the caller reads,
// refreshes, and conditionally persists a single OAuth connection.
func (r OAuthConnectionRepository) WithRefreshLock(ctx context.Context, id uuid.UUID, fn func(OAuthConnectionRepository) error) error {
	if r.db == nil {
		return fmt.Errorf("oauth connection refresh lock: database is not initialized")
	}
	if id == uuid.Nil {
		return fmt.Errorf("oauth connection refresh lock: connection id is required")
	}
	if fn == nil {
		return fmt.Errorf("oauth connection refresh lock: callback is required")
	}

	key1, key2 := oauthRefreshAdvisoryLockKeys(id)
	return r.db.WithContext(ctx).Connection(func(conn *gorm.DB) (callbackErr error) {
		if err := conn.WithContext(ctx).Exec("SELECT pg_advisory_lock(?, ?)", key1, key2).Error; err != nil {
			return fmt.Errorf("acquire oauth connection refresh lock: %w", err)
		}
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := conn.WithContext(unlockCtx).Exec("SELECT pg_advisory_unlock(?, ?)", key1, key2).Error; err != nil && callbackErr == nil {
				callbackErr = fmt.Errorf("release oauth connection refresh lock: %w", err)
			}
		}()
		return fn(NewOAuthConnectionRepository(conn.WithContext(ctx)))
	})
}

func oauthRefreshAdvisoryLockKeys(id uuid.UUID) (int32, int32) {
	return int32(binary.BigEndian.Uint32(id[0:4])), int32(binary.BigEndian.Uint32(id[4:8]))
}

func (r OAuthConnectionRepository) SaveCodexRefreshIfRefreshTokenMatches(ctx context.Context, connection OAuthConnection, expectedEncryptedRefreshToken string) (bool, error) {
	updates := map[string]any{
		"status":                 connection.Status,
		"account_id":             connection.AccountID,
		"email":                  connection.Email,
		"encrypted_access_token": connection.EncryptedAccessToken,
		"masked_access_token":    connection.MaskedAccessToken,
		"encrypted_id_token":     connection.EncryptedIDToken,
		"masked_id_token":        connection.MaskedIDToken,
		"token_type":             connection.TokenType,
		"scopes":                 connection.Scopes,
		"expires_at":             connection.ExpiresAt,
		"last_refresh_at":        connection.LastRefreshAt,
		"raw_profile":            connection.RawProfile,
		"metadata":               connection.Metadata,
		"updated_at":             time.Now().UTC(),
	}
	if connection.EncryptedRefreshToken != expectedEncryptedRefreshToken {
		updates["encrypted_refresh_token"] = connection.EncryptedRefreshToken
		updates["masked_refresh_token"] = connection.MaskedRefreshToken
	}
	result := r.db.WithContext(ctx).
		Model(&OAuthConnection{}).
		Where("id = ? AND encrypted_refresh_token = ?", connection.ID, expectedEncryptedRefreshToken).
		Updates(updates)
	if result.Error != nil {
		return false, fmt.Errorf("save codex refresh: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (r OAuthConnectionRepository) MarkCodexRefreshFailureIfRefreshTokenMatches(ctx context.Context, id uuid.UUID, expectedEncryptedRefreshToken string, metadata JSON) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&OAuthConnection{}).
		Where("id = ? AND encrypted_refresh_token = ?", id, expectedEncryptedRefreshToken).
		Updates(map[string]any{
			"status":     "reconnect_required",
			"metadata":   metadata,
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return false, fmt.Errorf("save codex refresh failure: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func defaultOAuthConnectionString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
