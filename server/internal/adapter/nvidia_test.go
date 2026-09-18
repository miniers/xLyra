package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nvidiaTestEntry(name, publisher string, free, chat bool) map[string]any {
	tags := []string{}
	if free {
		tags = append(tags, "nim_type_preview")
	}
	if chat {
		tags = append(tags, "playgroundtype_chat")
	}
	return map[string]any{
		"resourceType": "ENDPOINT", "name": name, "displayName": name,
		"labels": []any{map[string]any{"key": "publisher", "values": []string{publisher}}, map[string]any{"key": "general", "unresolvedValues": tags}},
	}
}

func TestNVIDIAModelsKeepNonFreeChatAndCacheCatalog(t *testing.T) {
	var catalogs, inventories atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog" {
			if r.Header.Get("Authorization") != "" {
				t.Error("public catalog received a secret")
			}
			catalogs.Add(1)
			var query struct {
				Page    int `json:"page"`
				Filters []struct {
					Field string `json:"field"`
				} `json:"filters"`
			}
			if err := json.Unmarshal([]byte(r.URL.Query().Get("q")), &query); err != nil {
				t.Error(err)
			}
			for _, filter := range query.Filters {
				if filter.Field == "nimType" {
					t.Error("catalog must include non-free models")
				}
			}
			items := []any{nvidiaTestEntry("a-paid", "vendor", false, true), nvidiaTestEntry("glm-5-3", "z-ai", true, true)}
			if query.Page == 1 {
				expired := nvidiaTestEntry("old", "vendor", true, true)
				expired["attributes"] = []any{map[string]any{"key": "DEPRECATION", "value": "01/01/2020"}}
				items = []any{expired, nvidiaTestEntry("embed", "vendor", true, false), nvidiaTestEntry("missing", "vendor", true, true)}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resultPageTotal": 2, "results": []any{map[string]any{"resources": items}}})
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-do-not-forward" {
			t.Errorf("inference request authorization = %q", r.Header.Get("Authorization"))
		}
		inventories.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/a-paid"},{"id":"z-ai/glm-5.3"},{"id":"vendor/embed"},{"id":"vendor/old"},{"id":"vendor/legacy"},{"id":"vendor/a-paid"}]}`))
	}))
	defer server.Close()
	a := NewNVIDIA()
	a.catalogURL = server.URL + "/catalog"
	site := SiteConfig{BaseURL: server.URL + "/v1/"}
	for i := 0; i < 2; i++ {
		models, err := a.ListModels(t.Context(), site, "secret-do-not-forward")
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 3 || models[0].UpstreamName != "z-ai/glm-5.3" || models[0].DisplayName != "z-ai/glm-5.3 [free]" || models[1].DisplayName != "vendor/a-paid" || models[2].DisplayName != "vendor/legacy" {
			t.Fatalf("models = %#v", models)
		}
		if models[1].Capabilities["free_endpoint"] != false || !reflect.DeepEqual(models[0].Capabilities["supported_endpoint_types"], []string{"openai"}) {
			t.Fatalf("capabilities = %#v", models)
		}
		models[0].Capabilities["free_endpoint"] = false
	}
	if catalogs.Load() != 2 || inventories.Load() != 2 {
		t.Fatalf("catalog=%d inventory=%d", catalogs.Load(), inventories.Load())
	}
	if _, ok := any(a).(CredentialValidator); ok {
		t.Fatal("public directory must not validate credentials")
	}
	if a.Scope() != HealthProbeSiteScope {
		t.Fatal("health probe must be site scoped")
	}
}

func TestNVIDIACatalogExpirationFailureBackoffAndRecovery(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	var calls int
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			http.Error(w, "unavailable", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resultPageTotal": 1, "results": []any{map[string]any{"resources": []any{nvidiaTestEntry("model", "vendor", true, true)}}}})
	}))
	defer server.Close()
	a := NewNVIDIA()
	a.catalogURL = server.URL
	a.now = func() time.Time { return now }
	_, at, stale, err := a.catalog(t.Context(), SiteConfig{})
	if err != nil || stale {
		t.Fatalf("initial: %v %v", stale, err)
	}
	now = now.Add(nvidiaCatalogTTL)
	fail = true
	_, staleAt, stale, err := a.catalog(t.Context(), SiteConfig{})
	if err != nil || !stale || !staleAt.Equal(at) {
		t.Fatalf("fallback: %v %v", stale, err)
	}
	_, _, _, _ = a.catalog(t.Context(), SiteConfig{})
	if calls != 2 {
		t.Fatalf("failure backoff made %d calls", calls)
	}
	now = at.Add(nvidiaCatalogMaxAge)
	if _, _, _, err := a.catalog(t.Context(), SiteConfig{}); err == nil {
		t.Fatal("expired stale cache accepted")
	}
	fail = false
	now = now.Add(nvidiaCatalogRetry)
	if _, _, stale, err := a.catalog(t.Context(), SiteConfig{}); err != nil || stale {
		t.Fatalf("recovery: %v %v", stale, err)
	}
}

func TestNVIDIACatalogCoalescesRequestsAndCancelsWaiters(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"resultPageTotal": 1, "results": []any{map[string]any{"resources": []any{nvidiaTestEntry("model", "vendor", true, true)}}}})
	}))
	defer server.Close()
	a := NewNVIDIA()
	a.catalogURL = server.URL
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, _, _, err := a.catalog(t.Context(), SiteConfig{}); err != nil {
			t.Error(err)
		}
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, _, err := a.catalog(ctx, SiteConfig{}); done <- err }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("cancel = %v", err)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, err := a.catalog(t.Context(), SiteConfig{}); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent fetches=%d", calls.Load())
	}
}

func TestNVIDIACatalogRejectsEmptyAndMalformedResults(t *testing.T) {
	for _, body := range []string{`{}`, `not-json`, `{"resultPageTotal":1,"results":[]}`, `{"resultPageTotal":21}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			a := NewNVIDIA()
			a.catalogURL = server.URL
			if _, _, _, err := a.catalog(t.Context(), SiteConfig{}); err == nil {
				t.Fatal("expected invalid catalog error")
			}
		})
	}
}

func TestNVIDIAListModelsDegradesGracefullyOnCatalogFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog" {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/v1/models" {
			if r.Header.Get("Authorization") != "Bearer test-credential" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"meta/llama-3.3-70b-instruct"},{"id":"vendor/embed-model"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	a := NewNVIDIA()
	a.catalogURL = server.URL + "/catalog"
	site := SiteConfig{BaseURL: server.URL}

	// Test that ProbeHealth forwards the credential and succeeds despite catalog failure
	models, err := a.ProbeHealth(t.Context(), site, "test-credential")
	if err != nil {
		t.Fatalf("ProbeHealth returned unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].UpstreamName != "meta/llama-3.3-70b-instruct" {
		t.Fatalf("unexpected models: %#v", models)
	}
	if models[0].Capabilities["source"] != "nvidia_inventory" || models[0].Capabilities["catalog_stale"] != true {
		t.Fatalf("unexpected capabilities: %#v", models[0].Capabilities)
	}
}

