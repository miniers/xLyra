package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
			if r.Header.Get("User-Agent") != "cli" {
				http.Error(w, "bad user-agent", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"custom-model-1","context_length":128000}]}`))
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
	if len(models) != 1 || models[0].UpstreamName != "custom-model-1" {
		t.Fatalf("expected custom-model-1, got %#v", models)
	}
	if models[0].Capabilities["context_length"] != 128000 {
		t.Fatalf("expected context_length 128000, got %v", models[0].Capabilities["context_length"])
	}
}
