package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xlyra/server/internal/store"
)

func applyOAuthConnectionUpdate(base store.OAuthConnection, dest any) (store.OAuthConnection, bool) {
	if item, ok := dest.(*store.OAuthConnection); ok {
		return *item, true
	}
	updates, ok := dest.(map[string]interface{})
	if !ok {
		return store.OAuthConnection{}, false
	}
	result := base
	for key, target := range map[string]*string{
		"status":                  &result.Status,
		"account_id":              &result.AccountID,
		"email":                   &result.Email,
		"encrypted_access_token":  &result.EncryptedAccessToken,
		"masked_access_token":     &result.MaskedAccessToken,
		"encrypted_refresh_token": &result.EncryptedRefreshToken,
		"masked_refresh_token":    &result.MaskedRefreshToken,
		"encrypted_id_token":      &result.EncryptedIDToken,
		"masked_id_token":         &result.MaskedIDToken,
		"token_type":              &result.TokenType,
		"scopes":                  &result.Scopes,
	} {
		if value, ok := updates[key].(string); ok {
			*target = value
		}
	}
	if value, ok := updates["metadata"].(store.JSON); ok {
		result.Metadata = value
	}
	return result, true
}

func TestHandleCodexCallbackMarksSessionFailedWhenExchangeFailsOffline(t *testing.T) {
	t.Parallel()

	sessionID := uuid.New()
	queryCount := 0
	var savedSession store.OAuthSession
	service := oauthServiceWithQueryUpdate(t, func(tx *gorm.DB) {
		session, ok := tx.Statement.Dest.(*store.OAuthSession)
		if !ok {
			tx.AddError(errors.New("unexpected codex callback query destination"))
			return
		}
		queryCount++
		*session = store.OAuthSession{
			ID:           sessionID,
			Provider:     codexProvider,
			State:        "codex-callback-state",
			Status:       "pending",
			RedirectURI:  codexRedirectURI,
			PKCEVerifier: "verifier",
			ExpiresAt:    time.Now().Add(time.Hour),
			Metadata:     store.JSON(`{"kept":"yes"}`),
		}
		tx.Statement.RowsAffected = 1
	}, func(tx *gorm.DB) {
		session, ok := tx.Statement.Dest.(*store.OAuthSession)
		if !ok {
			tx.AddError(errors.New("unexpected codex callback save destination"))
			return
		}
		savedSession = *session
		tx.Statement.RowsAffected = 1
	})
	service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != codexTokenURL {
			t.Fatalf("unexpected codex exchange request: %s %s", req.Method, req.URL.String())
		}
		return oauthHTTPResponse(http.StatusBadGateway, ` upstream down `), nil
	})}

	_, _, _, err := service.HandleCodexCallback(context.Background(), " codex-callback-state ", " code ")
	if err == nil || !strings.Contains(err.Error(), "codex token exchange returned 502: upstream down") {
		t.Fatalf("HandleCodexCallback error = %v, want exchange failure", err)
	}
	if queryCount != 2 {
		t.Fatalf("query count = %d, want get-by-state and complete get-by-id", queryCount)
	}
	if savedSession.ID != sessionID || savedSession.Status != "failed" || !savedSession.CompletedAt.Valid {
		t.Fatalf("saved session = %#v, want failed completed session", savedSession)
	}
	if string(savedSession.Metadata) != `{"kept":"yes"}` {
		t.Fatalf("saved metadata = %s, want original metadata", savedSession.Metadata)
	}
}

func TestHandleAntigravityCallbackMarksSessionFailedWhenUserInfoFailsOffline(t *testing.T) {
	t.Parallel()

	sessionID := uuid.New()
	queryCount := 0
	var savedSession store.OAuthSession
	service := oauthServiceWithQueryUpdate(t, func(tx *gorm.DB) {
		session, ok := tx.Statement.Dest.(*store.OAuthSession)
		if !ok {
			tx.AddError(errors.New("unexpected antigravity callback query destination"))
			return
		}
		queryCount++
		*session = store.OAuthSession{
			ID:          sessionID,
			Provider:    antigravityProvider,
			State:       "antigravity-callback-state",
			Status:      "pending",
			RedirectURI: antigravityRedirectURI,
			ExpiresAt:   time.Now().Add(time.Hour),
			Metadata:    store.JSON(`{"oauth_client_key":"antigravity-client"}`),
		}
		tx.Statement.RowsAffected = 1
	}, func(tx *gorm.DB) {
		session, ok := tx.Statement.Dest.(*store.OAuthSession)
		if !ok {
			tx.AddError(errors.New("unexpected antigravity callback save destination"))
			return
		}
		savedSession = *session
		tx.Statement.RowsAffected = 1
	})
	service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case antigravityTokenURL:
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected antigravity token method: %s", req.Method)
			}
			return oauthHTTPResponse(http.StatusOK, `{"access_token":"access-token","refresh_token":"refresh-token","token_type":"Bearer","expires_in":900}`), nil
		case antigravityUserInfoURL:
			if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer access-token" {
				t.Fatalf("unexpected antigravity userinfo request: %s %#v", req.Method, req.Header)
			}
			return oauthHTTPResponse(http.StatusForbidden, ` forbidden `), nil
		default:
			t.Fatalf("unexpected antigravity callback URL: %s", req.URL.String())
			return nil, nil
		}
	})}

	_, _, _, err := service.HandleAntigravityCallback(context.Background(), "antigravity-callback-state", "code")
	if err == nil || !strings.Contains(err.Error(), "antigravity userinfo returned 403: forbidden") {
		t.Fatalf("HandleAntigravityCallback error = %v, want userinfo failure", err)
	}
	if queryCount != 2 {
		t.Fatalf("query count = %d, want get-by-state and complete get-by-id", queryCount)
	}
	if savedSession.ID != sessionID || savedSession.Status != "failed" || !savedSession.CompletedAt.Valid {
		t.Fatalf("saved session = %#v, want failed completed session", savedSession)
	}
}

func TestRefreshCodexConnectionMarksReconnectRequiredOnRefreshFailureOffline(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	bootstrap := NewService(nil, "master-key")
	encryptedRefresh, _, err := bootstrap.credentials.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	connection := store.OAuthConnection{
		ID:                    connectionID,
		Provider:              codexProvider,
		Status:                "connected",
		Email:                 "user@example.com",
		EncryptedRefreshToken: encryptedRefresh,
		Metadata:              store.JSON(`{"token_mode":"oauth_refresh"}`),
	}
	var saved store.OAuthConnection
	service := oauthServiceWithQueryUpdate(t, func(tx *gorm.DB) {
		item, ok := tx.Statement.Dest.(*store.OAuthConnection)
		if !ok {
			tx.AddError(errors.New("unexpected codex refresh query destination"))
			return
		}
		*item = connection
		tx.Statement.RowsAffected = 1
	}, func(tx *gorm.DB) {
		updated, ok := applyOAuthConnectionUpdate(connection, tx.Statement.Dest)
		if !ok {
			tx.AddError(errors.New("unexpected codex refresh save destination"))
			return
		}
		saved = updated
		tx.Statement.RowsAffected = 1
	})
	service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != codexTokenURL {
			t.Fatalf("unexpected codex refresh request: %s %s", req.Method, req.URL.String())
		}
		return oauthHTTPResponse(http.StatusUnauthorized, ` invalid_grant `), nil
	})}

	_, err = service.RefreshCodexConnection(context.Background(), connectionID)
	if err == nil || !strings.Contains(err.Error(), "codex token refresh returned 401: invalid_grant") {
		t.Fatalf("RefreshCodexConnection error = %v, want refresh failure", err)
	}
	if saved.ID != connectionID || saved.Status != "reconnect_required" {
		t.Fatalf("saved connection = %#v, want reconnect_required", saved)
	}
	var meta map[string]any
	if err := json.Unmarshal(saved.Metadata, &meta); err != nil {
		t.Fatalf("decode saved metadata: %v", err)
	}
	if meta["token_mode"] != "oauth_refresh" || meta["last_error"] != "codex token refresh returned 401: invalid_grant" || meta["last_error_code"] != "invalid_grant" || strings.TrimSpace(stringFromAny(meta["last_error_at"])) == "" {
		t.Fatalf("saved metadata = %#v, want merged refresh error", meta)
	}
}

func TestRefreshCodexConnectionSkipsPreviouslyInvalidatedTokenOffline(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	bootstrap := NewService(nil, "master-key")
	encryptedRefresh, _, err := bootstrap.credentials.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	connection := store.OAuthConnection{
		ID:                    connectionID,
		Provider:              codexProvider,
		Status:                "reconnect_required",
		EncryptedRefreshToken: encryptedRefresh,
		Metadata:              store.JSON(`{"last_error":"codex token refresh returned 401: {\"error\":{\"code\":\"refresh_token_invalidated\"}}"}`),
	}
	service := oauthServiceWithQueryUpdate(t, func(tx *gorm.DB) {
		item, ok := tx.Statement.Dest.(*store.OAuthConnection)
		if !ok {
			tx.AddError(errors.New("unexpected codex refresh query destination"))
			return
		}
		*item = connection
		tx.Statement.RowsAffected = 1
	}, func(tx *gorm.DB) {
		tx.AddError(errors.New("previously invalidated token must not be saved"))
	})
	var calls int
	service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return oauthHTTPResponse(http.StatusUnauthorized, `refresh_token_invalidated`), nil
	})}

	_, err = service.RefreshCodexConnection(context.Background(), connectionID)
	if err == nil || !strings.Contains(err.Error(), "requires reauthorization; sign in again") {
		t.Fatalf("RefreshCodexConnection error = %v, want reauthorization guidance", err)
	}
	if calls != 0 {
		t.Fatalf("refresh endpoint calls = %d, want 0 after permanent auth failure", calls)
	}
}

func TestRefreshCodexConnectionPreservesRefreshTokenWhenRotationTokenOmittedOffline(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	bootstrap := NewService(nil, "master-key")
	encryptedRefresh, maskedRefresh, err := bootstrap.credentials.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	connection := store.OAuthConnection{
		ID:                    connectionID,
		Provider:              codexProvider,
		Status:                "connected",
		EncryptedRefreshToken: encryptedRefresh,
		MaskedRefreshToken:    maskedRefresh,
		Metadata:              store.JSON(`{"token_mode":"oauth_refresh"}`),
	}
	var saved store.OAuthConnection
	service := oauthServiceWithQueryUpdate(t, func(tx *gorm.DB) {
		item, ok := tx.Statement.Dest.(*store.OAuthConnection)
		if !ok {
			tx.AddError(errors.New("unexpected codex refresh query destination"))
			return
		}
		*item = connection
		tx.Statement.RowsAffected = 1
	}, func(tx *gorm.DB) {
		updated, ok := applyOAuthConnectionUpdate(connection, tx.Statement.Dest)
		if !ok {
			tx.AddError(errors.New("unexpected codex refresh save destination"))
			return
		}
		saved = updated
		tx.Statement.RowsAffected = 1
	})
	idToken := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"user@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"acct-123"}}`)) + ".signature"
	service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != codexTokenURL {
			t.Fatalf("unexpected codex refresh request: %s %s", req.Method, req.URL.String())
		}
		return oauthHTTPResponse(http.StatusOK, `{"access_token":"access-2","id_token":"`+idToken+`","token_type":"Bearer","expires_in":3600}`), nil
	})}

	details, err := service.RefreshCodexConnection(context.Background(), connectionID)
	if err != nil {
		t.Fatalf("RefreshCodexConnection returned error: %v", err)
	}
	if saved.EncryptedRefreshToken != encryptedRefresh || saved.MaskedRefreshToken != maskedRefresh || details.RefreshToken != "refresh-token" {
		t.Fatalf("refresh token was not preserved: saved=%#v details=%#v", saved, details)
	}
}

func TestRefreshCodexConnectionPreservesIdentityWhenIDTokenMissingOrInvalidOffline(t *testing.T) {
	t.Parallel()

	for _, idToken := range []string{"", "not-a-jwt"} {
		idToken := idToken
		name := "invalid"
		if idToken == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			connectionID := uuid.New()
			bootstrap := NewService(nil, "master-key")
			encryptedRefresh, maskedRefresh, err := bootstrap.credentials.Encrypt("refresh-token")
			if err != nil {
				t.Fatalf("encrypt refresh token: %v", err)
			}
			encryptedIDToken, _, err := bootstrap.credentials.Encrypt("old-id-token")
			if err != nil {
				t.Fatalf("encrypt id token: %v", err)
			}
			connection := store.OAuthConnection{
				ID:                    connectionID,
				Provider:              codexProvider,
				Status:                "connected",
				AccountID:             "old-account",
				Email:                 "old@example.com",
				EncryptedRefreshToken: encryptedRefresh,
				MaskedRefreshToken:    maskedRefresh,
				EncryptedIDToken:      encryptedIDToken,
				RawProfile:            store.JSON(`{"old":"profile"}`),
				Metadata:              store.JSON(`{"keep":"yes","plan_type":"plus","last_error":"stale","last_error_code":"invalid_grant","last_error_at":"2026-08-31T00:00:00Z"}`),
				TokenType:             "Bearer",
				Scopes:                "openid profile email offline_access",
			}
			var saved store.OAuthConnection
			service := oauthServiceWithQueryUpdate(t, func(tx *gorm.DB) {
				item, ok := tx.Statement.Dest.(*store.OAuthConnection)
				if !ok {
					tx.AddError(errors.New("unexpected codex refresh query destination"))
					return
				}
				*item = connection
				tx.Statement.RowsAffected = 1
			}, func(tx *gorm.DB) {
				updated, ok := applyOAuthConnectionUpdate(connection, tx.Statement.Dest)
				if !ok {
					tx.AddError(errors.New("unexpected codex refresh save destination"))
					return
				}
				saved = updated
				tx.Statement.RowsAffected = 1
			})
			body := `{"access_token":"access-2","expires_in":3600`
			if idToken != "" {
				body += `,"id_token":"` + idToken + `"`
			}
			body += `}`
			service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != codexTokenURL {
					t.Fatalf("unexpected codex refresh request: %s %s", req.Method, req.URL.String())
				}
				return oauthHTTPResponse(http.StatusOK, body), nil
			})}

			details, err := service.RefreshCodexConnection(context.Background(), connectionID)
			if err != nil {
				t.Fatalf("RefreshCodexConnection returned error: %v", err)
			}
			if details.AccessToken != "access-2" || details.IDToken != "old-id-token" || details.AccountID != "old-account" || details.Email != "old@example.com" {
				t.Fatalf("details = %#v, want new access and preserved identity", details)
			}
			if saved.EncryptedRefreshToken != encryptedRefresh || saved.MaskedRefreshToken != maskedRefresh || saved.EncryptedIDToken != encryptedIDToken {
				t.Fatalf("rotated optional tokens were not preserved: saved=%#v", saved)
			}
			var meta map[string]any
			if err := json.Unmarshal(saved.Metadata, &meta); err != nil {
				t.Fatalf("decode saved metadata: %v", err)
			}
			if meta["keep"] != "yes" || meta["plan_type"] != "plus" || meta["last_error"] != nil || meta["last_error_code"] != nil || meta["last_error_at"] != nil {
				t.Fatalf("metadata = %#v, want unrelated fields preserved and old error cleared", meta)
			}
		})
	}
}

func TestRefreshAntigravityConnectionMarksReconnectRequiredOnRefreshFailureOffline(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	service := NewService(nil, "master-key")
	encryptedRefresh, _, err := service.credentials.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	connection := store.OAuthConnection{
		ID:                    connectionID,
		Provider:              antigravityProvider,
		Status:                "connected",
		EncryptedRefreshToken: encryptedRefresh,
		Metadata:              store.JSON(`{"oauth_client_key":"antigravity-client"}`),
	}
	service.httpClient = &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != antigravityTokenURL {
			t.Fatalf("unexpected antigravity refresh request: %s %s", req.Method, req.URL.String())
		}
		return oauthHTTPResponse(http.StatusBadRequest, ` invalid_grant `), nil
	})}

	_, err = service.refreshAntigravityConnection(context.Background(), store.OAuthConnectionRepository{}, connection)
	if err == nil || !strings.Contains(err.Error(), "antigravity token refresh returned 400: invalid_grant") {
		t.Fatalf("refreshAntigravityConnection error = %v, want refresh failure", err)
	}
	var fail *refreshFail
	if !errors.As(err, &fail) {
		t.Fatalf("refreshAntigravityConnection error = %v, want refreshFail carrying the reconnection state", err)
	}
	if fail.connection.ID != connectionID || fail.connection.Status != "reconnect_required" {
		t.Fatalf("refresh fail connection = %#v, want reconnect_required", fail.connection)
	}
	var meta map[string]any
	if err := json.Unmarshal(fail.connection.Metadata, &meta); err != nil {
		t.Fatalf("decode saved metadata: %v", err)
	}
	if meta["oauth_client_key"] != "antigravity-client" || meta["last_error"] != "antigravity token refresh returned 400: invalid_grant" || strings.TrimSpace(stringFromAny(meta["last_error_at"])) == "" {
		t.Fatalf("saved metadata = %#v, want merged antigravity refresh error", meta)
	}
}
