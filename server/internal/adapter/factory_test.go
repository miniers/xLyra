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
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
		// GPT series
		"gpt-5.5",
		"gpt-5.5-pro",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex",
		"gpt-5.2",
		// Droid Core series
		"deepseek-v4-pro",
		"glm-5.2",
		"glm-5.1",
		"kimi-k2.7-code",
		"kimi-k2.6",
		"minimax-m3",
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
