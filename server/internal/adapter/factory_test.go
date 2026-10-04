package adapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"xlyra/server/internal/upstream"
)

func TestFactoryAdapterProperties(t *testing.T) {
	t.Parallel()

	f := NewFactory()

	if types := f.SiteTypes(); len(types) != 1 || types[0] != "factory" {
		t.Fatalf("SiteTypes() = %v, want [factory]", types)
	}

	if baseURL := f.DefaultBaseURL(); baseURL != "https://api.factory.ai" {
		t.Fatalf("DefaultBaseURL() = %q, want https://api.factory.ai", baseURL)
	}

	if scope := f.Scope(); scope != HealthProbeCredentialScope {
		t.Fatalf("Scope() = %v, want HealthProbeCredentialScope", scope)
	}

	caps := f.Capabilities()
	expectedCaps := []Capability{
		CapabilityValidateCredential,
		CapabilityListModels,
		CapabilityHealthProbe,
		CapabilityFetchBalance,
	}
	for _, expected := range expectedCaps {
		if !hasCapability(caps, expected) {
			t.Fatalf("Capabilities() missing %v: %v", expected, caps)
		}
	}
}

func TestFactoryListModelsCatalog(t *testing.T) {
	t.Parallel()

	f := NewFactory()
	models, err := f.ListModels(context.Background(), SiteConfig{}, "")
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}

	expectedIDs := []string{
		// Claude series
		"claude-opus-5-5",
		"claude-opus-5-5-fast",
		"claude-opus-5",
		"claude-opus-5-fast",
		"claude-opus-4-8",
		"claude-opus-4-8-fast",
		"claude-opus-4-7",
		"claude-opus-4-7-fast",
		"claude-opus-4-6",
		"claude-opus-4-6-fast",
		"claude-opus-4-5-20251101",
		"claude-opus-4-1-20250805",
		"claude-sonnet-5-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
		"claude-fable-5.1",
		"claude-fable-5",
		"claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022",
		"atlas-07-21",
		"aster-07-15",
		// GPT series
		"gpt-6.1-sol",
		"gpt-6-sol",
		"gpt-6-astra",
		"gpt-6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-sol-fast",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5-pro",
		"gpt-5.5",
		"gpt-5.5-fast",
		"gpt-5.4",
		"gpt-5.4-fast",
		"gpt-5.4-mini",
		"gpt-5.4-mini-fast",
		"gpt-5.3-codex",
		"gpt-5.3-codex-fast",
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex",
		"gpt-5.1",
		"gpt-5-codex",
		"gpt-5-mini-2025-08-07",
		"gpt-5-2025-08-07",
		// Gemini series
		"gemini-3.8-flash",
		"gemini-3.7-flash",
		"gemini-3.6-flash",
		"gemini-3.5-flash",
		"gemini-3.1-pro-preview",
		"gemini-3-pro-preview",
		"gemini-3-flash-preview",
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"garnet-07-15",
		// Grok series
		"grok-4.7",
		"grok-4.6",
		"grok-4.5",
		// Droid Core series
		"kimi-k3",
		"kimi-k2.7-code",
		"kimi-k2.6",
		"glm-5.3",
		"glm-5.3-flash",
		"glm-5.2",
		"glm-5.2-fast",
		"glm-5.1",
		"glm-5",
		"deepseek-v4.1-flash",
		"deepseek-v4-pro",
		"deepseek-v4-flash-0731",
		"minimax-m3",
		"minimax-m2.7",
		"minimax-m2.5",
		"qwen3.8-max",
		"mistral-medium-3.5",
		"nemotron-3-ultra",
	}

	if len(models) != len(expectedIDs) {
		t.Fatalf("ListModels returned %d models, want %d", len(models), len(expectedIDs))
	}

	modelMap := make(map[string]Model, len(models))
	for _, m := range models {
		modelMap[m.UpstreamName] = m
	}

	for _, expectedID := range expectedIDs {
		m, ok := modelMap[expectedID]
		if !ok {
			t.Fatalf("model %q missing from catalog", expectedID)
		}
		if m.DisplayName == "" {
			t.Fatalf("model %q display name is empty", expectedID)
		}
		endpointTypes, _ := m.Capabilities["supported_endpoint_types"].([]string)
		if !reflect.DeepEqual(endpointTypes, []string{"openai", "openai-response", "anthropic-messages"}) {
			t.Fatalf("model %q supported_endpoint_types = %v", expectedID, endpointTypes)
		}
		if m.Capabilities["source"] != "factory_curated" {
			t.Fatalf("model %q source = %v", expectedID, m.Capabilities["source"])
		}
		if m.Capabilities["tool_call"] != true || m.Capabilities["streaming"] != true {
			t.Fatalf("model %q missing tool_call or streaming", expectedID)
		}
	}
}

func TestFactoryListModelsRemoteWithMerge(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models" {
			if r.Header.Get("Authorization") != "Bearer test-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("X-Factory-Client") != "cli" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": [
					{"id": "custom-factory-model-1", "name": "Custom Model 1", "owned_by": "custom", "context_window": 128000},
					{"id": "gpt-6-sol", "name": "GPT-6 Sol Override"}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	f := NewFactory()
	models, err := f.ListModels(context.Background(), SiteConfig{BaseURL: server.URL}, "test-key")
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}

	modelMap := make(map[string]Model, len(models))
	for _, m := range models {
		modelMap[m.UpstreamName] = m
	}

	// Remote custom model should be included
	custom, ok := modelMap["custom-factory-model-1"]
	if !ok {
		t.Fatalf("custom model missing from remote models")
	}
	if custom.DisplayName != "Custom Model 1" {
		t.Fatalf("custom display name = %q, want Custom Model 1", custom.DisplayName)
	}
	if custom.Capabilities["source"] != "factory_remote" {
		t.Fatalf("custom source = %v, want factory_remote", custom.Capabilities["source"])
	}

	// Curated models should still be merged and present
	if _, ok := modelMap["claude-opus-5-5"]; !ok {
		t.Fatalf("curated model claude-opus-5-5 missing from merged catalog")
	}
}

func TestFactoryValidateCredentials_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/cli/whoami" {
			t.Fatalf("path = %s, want /api/cli/whoami", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer fact-secret-key" {
			t.Fatalf("Authorization = %q, want Bearer fact-secret-key", auth)
		}
		if ua := r.Header.Get("User-Agent"); ua != "factory-cli/0.232.0" {
			t.Fatalf("User-Agent = %q, want factory-cli/0.232.0", ua)
		}
		if ver := r.Header.Get("X-Client-Version"); ver != "0.232.0" {
			t.Fatalf("X-Client-Version = %q, want 0.232.0", ver)
		}
		if client := r.Header.Get("X-Factory-Client"); client != "cli" {
			t.Fatalf("X-Factory-Client = %q, want cli", client)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orgId":"org-12345","userId":"usr-67890"}`))
	}))
	t.Cleanup(server.Close)

	f := NewFactory()
	err := f.ValidateCredentials(context.Background(), SiteConfig{BaseURL: server.URL}, "fact-secret-key")
	if err != nil {
		t.Fatalf("ValidateCredentials returned unexpected error: %v", err)
	}

	orgID, userID, err := f.Whoami(context.Background(), SiteConfig{BaseURL: server.URL}, "fact-secret-key")
	if err != nil {
		t.Fatalf("Whoami returned unexpected error: %v", err)
	}
	if orgID != "org-12345" || userID != "usr-67890" {
		t.Fatalf("Whoami = (%q, %q), want (org-12345, usr-67890)", orgID, userID)
	}
}

func TestFactoryValidateCredentials_Errors(t *testing.T) {
	t.Parallel()

	t.Run("401 Unauthorized", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		}))
		t.Cleanup(server.Close)

		f := NewFactory()
		err := f.ValidateCredentials(context.Background(), SiteConfig{BaseURL: server.URL}, "bad-key")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var httpErr *upstream.HTTPError
		if !errors.As(err, &httpErr) || httpErr.Failure.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected upstream 401 error, got %v", err)
		}
	})

	t.Run("Empty whoami response", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}))
		t.Cleanup(server.Close)

		f := NewFactory()
		err := f.ValidateCredentials(context.Background(), SiteConfig{BaseURL: server.URL}, "test-key")
		if err == nil {
			t.Fatal("expected error for empty whoami response, got nil")
		}
	})
}

func TestFactoryFetchBalance_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/billing/limits" {
			t.Fatalf("path = %s, want /api/billing/limits", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer fact-balance-key" {
			t.Fatalf("Authorization = %q, want Bearer fact-balance-key", auth)
		}
		if ua := r.Header.Get("User-Agent"); ua != "factory-cli/0.232.0" {
			t.Fatalf("User-Agent = %q, want factory-cli/0.232.0", ua)
		}
		if ver := r.Header.Get("X-Client-Version"); ver != "0.232.0" {
			t.Fatalf("X-Client-Version = %q, want 0.232.0", ver)
		}
		if client := r.Header.Get("X-Factory-Client"); client != "cli" {
			t.Fatalf("X-Factory-Client = %q, want cli", client)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"limits": {
				"standard": {
					"fiveHour": { "usedPercent": 15.5 },
					"weekly": { "usedPercent": 42.0 },
					"monthly": { "usedPercent": 75.3 }
				},
				"core": {
					"fiveHour": { "usedPercent": 0.0 },
					"weekly": { "usedPercent": 10.0 },
					"monthly": { "usedPercent": 20.0 }
				}
			}
		}`))
	}))
	t.Cleanup(server.Close)

	f := NewFactory()
	snapshot, err := f.FetchBalance(context.Background(), SiteConfig{BaseURL: server.URL}, SystemAuth{AccessToken: "fact-balance-key"})
	if err != nil {
		t.Fatalf("FetchBalance returned error: %v", err)
	}

	rawMap, ok := snapshot.Raw.(map[string]any)
	if !ok {
		t.Fatalf("snapshot.Raw is not map[string]any: %#v", snapshot.Raw)
	}
	if rawMap["type"] != "factory" {
		t.Fatalf("snapshot.Raw type = %v, want factory", rawMap["type"])
	}

	standard, ok := rawMap["standard"].(map[string]any)
	if !ok {
		t.Fatalf("standard is not map[string]any: %#v", rawMap["standard"])
	}
	fiveHour, ok := standard["fiveHour"].(map[string]any)
	if !ok || fiveHour["usedPercent"] != 15.5 {
		t.Fatalf("standard fiveHour = %#v, want usedPercent 15.5", standard["fiveHour"])
	}
	weekly, ok := standard["weekly"].(map[string]any)
	if !ok || weekly["usedPercent"] != 42.0 {
		t.Fatalf("standard weekly = %#v, want usedPercent 42.0", standard["weekly"])
	}
	monthly, ok := standard["monthly"].(map[string]any)
	if !ok || monthly["usedPercent"] != 75.3 {
		t.Fatalf("standard monthly = %#v, want usedPercent 75.3", standard["monthly"])
	}

	core, ok := rawMap["core"].(map[string]any)
	if !ok {
		t.Fatalf("core is not map[string]any: %#v", rawMap["core"])
	}
	coreWeekly, ok := core["weekly"].(map[string]any)
	if !ok || coreWeekly["usedPercent"] != 10.0 {
		t.Fatalf("core weekly = %#v, want usedPercent 10.0", core["weekly"])
	}
}

func TestFactoryFetchBalance_Errors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"forbidden"}`))
	}))
	t.Cleanup(server.Close)

	f := NewFactory()
	_, err := f.FetchBalance(context.Background(), SiteConfig{BaseURL: server.URL}, SystemAuth{AccessToken: "bad-key"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var httpErr *upstream.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Failure.StatusCode != http.StatusForbidden {
		t.Fatalf("expected upstream 403 error, got %v", err)
	}
}

func TestFactoryProbeHealth(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer valid-key" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"orgId":"org-ok","userId":"usr-ok"}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(server.Close)

	f := NewFactory()

	// Valid key
	models, err := f.ProbeHealth(context.Background(), SiteConfig{BaseURL: server.URL}, "valid-key")
	if err != nil {
		t.Fatalf("ProbeHealth with valid key failed: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("ProbeHealth returned empty models")
	}

	// Invalid key
	_, err = f.ProbeHealth(context.Background(), SiteConfig{BaseURL: server.URL}, "invalid-key")
	if err == nil {
		t.Fatal("expected error with invalid key, got nil")
	}
}

func TestFactoryRegistryIntegration(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	mod, ok := reg.ModuleForSiteType("factory")
	if !ok {
		t.Fatal("factory module not found in default registry")
	}

	if _, ok := AsCredentialValidator(mod); !ok {
		t.Fatal("factory module does not implement CredentialValidator")
	}
	if _, ok := AsGatewayModelLister(mod); !ok {
		t.Fatal("factory module does not implement GatewayModelLister")
	}
	if _, ok := AsHealthProbe(mod); !ok {
		t.Fatal("factory module does not implement HealthProbe")
	}
	if _, ok := AsBalanceFetcher(mod); !ok {
		t.Fatal("factory module does not implement BalanceFetcher")
	}
}
