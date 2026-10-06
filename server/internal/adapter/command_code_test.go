package adapter

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func assertCommandCodePrice(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func TestCommandCodePricingCatalogMatchesFallbackModels(t *testing.T) {
	if got, want := len(commandCodePricingCatalog), 71; got != want {
		t.Fatalf("pricing catalog entries = %d, want %d", got, want)
	}

	for _, item := range commandCodeFallbackModels {
		if pricing, ok := commandCodeModelPricing(item.ID); !ok || pricing == nil {
			t.Fatalf("fallback model %q has no pricing metadata", item.ID)
		}
	}
}

func TestCommandCodeModelPricing(t *testing.T) {
	tests := []struct {
		name               string
		modelID            string
		input              float64
		output             float64
		cacheRead          float64
		cacheRatio         float64
		cacheWrite         float64
		wantCacheWrite     bool
		wantPricing        bool
		wantInputValid     bool
		wantOutputValid    bool
		wantCacheReadValid bool
	}{
		{
			name:               "laguna free",
			modelID:            "poolside/laguna-s-2.1-free",
			wantPricing:        true,
			wantInputValid:     true,
			wantOutputValid:    true,
			wantCacheReadValid: true,
		},
		{
			name:               "ling free",
			modelID:            "inclusionai/ling-3.0-flash-sante:free",
			wantPricing:        true,
			wantInputValid:     true,
			wantOutputValid:    true,
			wantCacheReadValid: true,
		},
		{
			name:               "minimax discounted",
			modelID:            "MiniMaxAI/MiniMax-M3",
			input:              0.3,
			output:             1.2,
			cacheRead:          0.06,
			cacheRatio:         0.2,
			wantPricing:        true,
			wantInputValid:     true,
			wantOutputValid:    true,
			wantCacheReadValid: true,
		},
		{
			name:               "mimo discounted",
			modelID:            "xiaomi/mimo-v2.5-pro",
			input:              0.435,
			output:             0.87,
			cacheRead:          0.0036,
			cacheRatio:         0.0036 / 0.435,
			wantPricing:        true,
			wantInputValid:     true,
			wantOutputValid:    true,
			wantCacheReadValid: true,
		},
		{
			name:               "cache write",
			modelID:            "Qwen/Qwen3.8-Max",
			input:              2,
			output:             6,
			cacheRead:          0.25,
			cacheRatio:         0.125,
			cacheWrite:         2.5,
			wantCacheWrite:     true,
			wantPricing:        true,
			wantInputValid:     true,
			wantOutputValid:    true,
			wantCacheReadValid: true,
		},
		{name: "unknown", modelID: "future-model"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricing, ok := commandCodeModelPricing(tt.modelID)
			if ok != tt.wantPricing || (pricing != nil) != tt.wantPricing {
				t.Fatalf("pricing lookup = (%#v, %v), want pricing=%v", pricing, ok, tt.wantPricing)
			}
			if !tt.wantPricing {
				return
			}

			if pricing.HasInputValue != tt.wantInputValid || pricing.HasOutputValue != tt.wantOutputValid || pricing.HasCacheRatio != tt.wantCacheReadValid {
				t.Fatalf("validity flags = input %v output %v cache %v", pricing.HasInputValue, pricing.HasOutputValue, pricing.HasCacheRatio)
			}
			assertCommandCodePrice(t, "input", pricing.InputValue, tt.input)
			assertCommandCodePrice(t, "output", pricing.OutputValue, tt.output)
			assertCommandCodePrice(t, "cache ratio", pricing.CacheRatio, tt.cacheRatio)
			if pricing.HasCreateCacheRatio != tt.wantCacheWrite {
				t.Fatalf("cache write validity = %v, want %v", pricing.HasCreateCacheRatio, tt.wantCacheWrite)
			}
			if tt.wantCacheWrite {
				assertCommandCodePrice(t, "cache write ratio", pricing.CreateCacheRatio, tt.cacheWrite/tt.input)
			} else if pricing.CreateCacheRatio != 0 {
				t.Fatalf("cache write ratio = %v, want 0", pricing.CreateCacheRatio)
			}
			assertCommandCodePrice(t, "raw input", pricing.Raw["input_value"].(float64), tt.input)
			assertCommandCodePrice(t, "raw output", pricing.Raw["output_value"].(float64), tt.output)
			assertCommandCodePrice(t, "raw cache read", pricing.Raw["cache_read_value"].(float64), tt.cacheRead)
			if pricing.Raw["source"] != "command_code" {
				t.Fatalf("pricing source = %v, want command_code", pricing.Raw["source"])
			}
		})
	}
}

func TestCommandCodeFallbackModels(t *testing.T) {
	adapter := NewCommandCode()
	models, err := adapter.ListModels(context.Background(), SiteConfig{}, "")
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected fallback models, got empty")
	}

	foundSonnet := false
	for _, m := range models {
		if m.UpstreamName == "claude-sonnet-4-6" {
			foundSonnet = true
			if m.Capabilities["source"] != "command_code_spec" {
				t.Fatalf("expected capability source command_code_spec, got %v", m.Capabilities["source"])
			}
			endpoints, _ := m.Capabilities["supported_endpoint_types"].([]string)
			if len(endpoints) != 3 || endpoints[2] != "openai-response" {
				t.Fatalf("expected supported_endpoint_types to contain openai-response, got %#v", endpoints)
			}
		}
	}
	if !foundSonnet {
		t.Fatal("expected claude-sonnet-4-6 in fallback models")
	}
}

func TestCommandCodeCatalogFetch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/provider/v1/models" {
			if r.Header.Get("Authorization") != "Bearer test-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.Header.Get("x-api-key") != "test-key" {
				http.Error(w, "missing x-api-key", http.StatusBadRequest)
				return
			}
			if r.Header.Get("User-Agent") != "cli" {
				http.Error(w, "bad user-agent", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"custom-model-1","context_length":128000,"display_name":"Custom Model","supported_endpoints":["/v1/responses"]},{"id":"MiniMaxAI/MiniMax-M3"},{"id":"poolside/laguna-s-2.1-free"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	adapter := NewCommandCode()
	models, err := adapter.ListModels(context.Background(), SiteConfig{BaseURL: ts.URL}, "test-key")
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 3 || models[0].UpstreamName != "custom-model-1" {
		t.Fatalf("expected custom-model-1, got %#v", models)
	}
	if models[0].Capabilities["context_length"] != 128000 {
		t.Fatalf("expected context_length 128000, got %v", models[0].Capabilities["context_length"])
	}
	if models[0].DisplayName != "Custom Model" {
		t.Fatalf("expected display_name Custom Model, got %q", models[0].DisplayName)
	}
	endpoints, _ := models[0].Capabilities["supported_endpoint_types"].([]string)
	if len(endpoints) != 1 || endpoints[0] != "openai-response" {
		t.Fatalf("expected response-only capability, got %#v", endpoints)
	}
	if models[0].Pricing != nil {
		t.Fatal("unknown live model must not get pricing metadata")
	}
	byName := map[string]Model{}
	for _, model := range models {
		byName[model.UpstreamName] = model
	}
	if pricing := byName["MiniMaxAI/MiniMax-M3"].Pricing; pricing == nil {
		t.Fatal("known live model must get pricing metadata")
	} else {
		assertCommandCodePrice(t, "live MiniMax input", pricing.InputValue, 0.3)
		assertCommandCodePrice(t, "live MiniMax output", pricing.OutputValue, 1.2)
	}
	if pricing := byName["poolside/laguna-s-2.1-free"].Pricing; pricing == nil || !pricing.HasInputValue || pricing.InputValue != 0 {
		t.Fatalf("free live model pricing = %#v, want valid zero input price", pricing)
	}
}

func TestCommandCodeModelCatalogParsesNameModalitiesAndLegacyCapabilities(t *testing.T) {
	models := commandCodeModelsFromItems([]commandCodeModelItem{
		{
			Name:        "named-model",
			DisplayName: "Named Model",
			Modalities:  commandCodeModelModalities{Input: []string{"text", "image"}},
		},
		{ID: "legacy-model"},
		{ID: "embedding-only-model", SupportedEndpoints: []string{"embeddings"}},
	})
	if len(models) != 2 || models[0].UpstreamName != "named-model" || models[0].DisplayName != "Named Model" {
		t.Fatalf("models = %#v", models)
	}
	modalities, _ := models[0].Capabilities["modalities_input"].([]string)
	if len(modalities) != 2 || modalities[1] != "image" {
		t.Fatalf("modalities = %#v", modalities)
	}
	legacy, _ := models[1].Capabilities["supported_endpoint_types"].([]string)
	if len(legacy) != 3 {
		t.Fatalf("legacy endpoints = %#v", legacy)
	}
}

func TestCommandCodeProbeHealthValidatesCredentialBeforeCatalogFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alpha/whoami":
			if r.Header.Get("Authorization") != "Bearer valid-key" || r.Header.Get("x-api-key") != "valid-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/provider/v1/models":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCommandCode()
	models, err := adapter.ProbeHealth(t.Context(), SiteConfig{BaseURL: server.URL}, "valid-key")
	if err != nil || len(models) == 0 {
		t.Fatalf("valid credential should survive catalog fallback: models=%d err=%v", len(models), err)
	}
	if _, err := adapter.ProbeHealth(t.Context(), SiteConfig{BaseURL: server.URL}, "bad-key"); err == nil {
		t.Fatal("invalid credential should fail before catalog fallback")
	}
}

func TestCommandCodeSubscriptionPlanPrefixes(t *testing.T) {
	tests := map[string]string{
		"individual-provider-v2": "Provider",
		"individual-pro-v1":      "Pro",
		"individual-goat":        "GOAT",
		"individual-go":          "Go",
		"teams-pro-enterprise":   "Teams Pro",
	}
	for planID, want := range tests {
		if got, ok := CommandCodeSubscriptionPlan(planID); !ok || got != want {
			t.Errorf("commandCodeSubscriptionPlan(%q) = (%q, %v), want (%q, true)", planID, got, ok, want)
		}
	}
	if _, ok := CommandCodeSubscriptionPlan("individual-unknown"); ok {
		t.Fatal("unknown plan must not be treated as a known subscription")
	}
}

func TestCommandCodeFetchCredentialMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/billing/subscriptions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer cc-key" || r.Header.Get("x-api-key") != "cc-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"planId":"individual-goat","status":"active","currentPeriodEnd":1770000000000}}`))
	}))
	defer server.Close()

	metadata, err := NewCommandCode().FetchCredentialMetadata(t.Context(), SiteConfig{BaseURL: server.URL}, "cc-key")
	if err != nil {
		t.Fatalf("FetchCredentialMetadata failed: %v", err)
	}
	subscription, ok := metadata["command_code_subscription"].(map[string]any)
	if !ok || subscription["plan"] != "GOAT" || subscription["plan_id"] != "individual-goat" || subscription["status"] != "active" {
		t.Fatalf("subscription metadata = %#v", metadata)
	}
	if _, ok := subscription["current_period_end"]; !ok || subscription["synced_at"] == "" {
		t.Fatalf("subscription timestamps missing: %#v", subscription)
	}
}

func TestCommandCodeFetchCredentialMetadataRecordsUnknownSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"planId":"individual-mystery","status":"active"}}`))
	}))
	defer server.Close()

	metadata, err := NewCommandCode().FetchCredentialMetadata(t.Context(), SiteConfig{BaseURL: server.URL}, "cc-key")
	if err != nil {
		t.Fatal(err)
	}
	subscription := metadata["command_code_subscription"].(map[string]any)
	if subscription["plan_id"] != "individual-mystery" || subscription["plan"] != "" {
		t.Fatalf("unknown plan metadata must replace the previous subscription: %#v", metadata)
	}
}

func TestCommandCodeFetchCredentialMetadataWithoutSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(server.Close)
	metadata, err := NewCommandCode().FetchCredentialMetadata(t.Context(), SiteConfig{BaseURL: server.URL}, "cc-key")
	if err != nil {
		t.Fatal(err)
	}
	subscription := metadata["command_code_subscription"].(map[string]any)
	if subscription["plan_id"] != "" || subscription["plan"] != "" || subscription["status"] != "" {
		t.Fatalf("missing subscription must clear the previous plan: %#v", metadata)
	}
}

func TestCommandCodeSubscriptionResponseShape(t *testing.T) {
	var payload commandCodeSubscriptionResponse
	if err := json.Unmarshal([]byte(`{"success":true,"data":{"planId":"individual-pro","status":"active","currentPeriodEnd":"2026-10-07T00:00:00Z"}}`), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data == nil || payload.Data.PlanID != "individual-pro" || string(payload.Data.CurrentPeriodEnd) != `"2026-10-07T00:00:00Z"` {
		t.Fatalf("payload = %#v", payload)
	}
}
