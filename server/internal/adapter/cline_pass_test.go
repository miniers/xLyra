package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClinePassMetadata(t *testing.T) {
	t.Parallel()

	cp := NewClinePass()
	if types := cp.SiteTypes(); len(types) != 1 || types[0] != "cline_pass" {
		t.Fatalf("SiteTypes() = %v, want ['cline_pass']", types)
	}
	if cp.DefaultBaseURL() != clinePassDefaultBaseURL {
		t.Fatalf("DefaultBaseURL() = %q, want %q", cp.DefaultBaseURL(), clinePassDefaultBaseURL)
	}
	if cp.Scope() != HealthProbeCredentialScope {
		t.Fatalf("Scope() = %q, want %q", cp.Scope(), HealthProbeCredentialScope)
	}

	caps := cp.Capabilities()
	hasValidate := false
	hasList := false
	hasHealth := false
	for _, c := range caps {
		if c == CapabilityValidateCredential {
			hasValidate = true
		}
		if c == CapabilityListModels {
			hasList = true
		}
		if c == CapabilityHealthProbe {
			hasHealth = true
		}
	}
	if !hasValidate || !hasList || !hasHealth {
		t.Fatalf("Capabilities missing expected items: %v", caps)
	}
}

func TestClinePassBaseURLNormalization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  string
	}{
		{"", "https://api.cline.bot/api/v1"},
		{"https://api.cline.bot", "https://api.cline.bot/api/v1"},
		{"https://api.cline.bot/", "https://api.cline.bot/api/v1"},
		{"https://api.cline.bot/api/v1", "https://api.cline.bot/api/v1"},
		{"https://api.cline.bot/v1", "https://api.cline.bot/v1"},
		{"https://custom.proxy.com", "https://custom.proxy.com/api/v1"},
	}

	for _, tc := range cases {
		got := clinePassBaseURL(tc.input)
		if got != tc.want {
			t.Errorf("clinePassBaseURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestClinePassListModelsOnline(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "cline-pass/qwen3.7-max", "object": "model", "owned_by": "cline"},
				{"id": "cline-pass/deepseek-v4-pro", "object": "model", "owned_by": "cline"},
			},
		})
	}))
	defer server.Close()

	cp := NewClinePass()
	models, err := cp.ListModels(t.Context(), SiteConfig{BaseURL: server.URL + "/api/v1"}, "test-key")
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].UpstreamName != "qwen3.7-max" {
		t.Errorf("model[0].UpstreamName = %q, want qwen3.7-max", models[0].UpstreamName)
	}
	if models[1].UpstreamName != "deepseek-v4-pro" {
		t.Errorf("model[1].UpstreamName = %q, want deepseek-v4-pro", models[1].UpstreamName)
	}
}

func TestClinePassListModelsFallbackOn404(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	cp := NewClinePass()
	models, err := cp.ListModels(t.Context(), SiteConfig{BaseURL: server.URL}, "test-key")
	if err != nil {
		t.Fatalf("ListModels failed on fallback: %v", err)
	}
	if len(models) != len(clinePassFallbackModels) {
		t.Fatalf("got %d fallback models, want %d", len(models), len(clinePassFallbackModels))
	}
}

func TestClinePassListModelsFiltersOpenRouterModels(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "openai/gpt-4o", "object": "model", "owned_by": "openai"},
				{"id": "anthropic/claude-3-5-sonnet", "object": "model", "owned_by": "anthropic"},
				{"id": "cline-pass/glm-5.2", "object": "model", "owned_by": "cline"}, // retired
				{"id": "sao10k/l3.3-euryale-70b", "object": "model", "owned_by": "sao10k"},
			},
		})
	}))
	defer server.Close()

	cp := NewClinePass()
	models, err := cp.ListModels(t.Context(), SiteConfig{BaseURL: server.URL + "/api/v1"}, "test-key")
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	// All returned models were non-cline-pass or retired, so it must fall back to the 12 curated models
	if len(models) != len(clinePassFallbackModels) {
		t.Fatalf("got %d models, want %d curated fallback models", len(models), len(clinePassFallbackModels))
	}
}

func TestClinePassValidateCredentials(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer valid-key" {
			if r.URL.Path == "/api/v1/users/me" {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":    "user_123",
					"email": "user@example.com",
				})
				return
			}
		}
		http.Error(w, `{"error":{"message":"Invalid API key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	cp := NewClinePass()
	err := cp.ValidateCredentials(t.Context(), SiteConfig{BaseURL: server.URL}, "valid-key")
	if err != nil {
		t.Fatalf("expected valid key to pass, got: %v", err)
	}

	err = cp.ValidateCredentials(t.Context(), SiteConfig{BaseURL: server.URL}, "invalid-key")
	if err == nil {
		t.Fatal("expected invalid key to fail, got nil")
	}
}

func TestClinePassValidateCredentialsFallbackModels(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/users/me" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/v1/models" {
			if r.Header.Get("Authorization") == "Bearer valid-fallback" {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
				return
			}
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cp := NewClinePass()
	if err := cp.ValidateCredentials(t.Context(), SiteConfig{BaseURL: server.URL}, "valid-fallback"); err != nil {
		t.Fatalf("expected valid fallback key to pass, got: %v", err)
	}
	if err := cp.ValidateCredentials(t.Context(), SiteConfig{BaseURL: server.URL}, "bad-key"); err == nil {
		t.Fatal("expected bad key to fail on models fallback")
	}
}
