package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/credential"
	"xlyra/server/internal/httpclient"
	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

func credentialAdapterIntegrationHandler(t *testing.T, candidate routeengine.Candidate, secrets []string, metadata []store.JSON) (Handler, []store.SiteCredential) {
	t.Helper()
	service := credential.NewService("adapter-integration-master-key")
	credentials := make([]store.SiteCredential, len(secrets))
	bindings := make([]store.SiteAPIKeyModel, len(secrets))
	states := make([]store.SiteAPIKeyState, len(secrets))
	for index, secret := range secrets {
		encrypted, masked, err := service.Encrypt(secret)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		credentials[index] = store.SiteCredential{
			ID: id, SiteID: candidate.Site.ID, CredentialType: "api_key:" + id.String(),
			EncryptedSecret: encrypted, MaskedSecret: masked, Meta: metadata[index],
			RoutingPriority: 5 - float64(index),
		}
		bindings[index] = store.SiteAPIKeyModel{SiteID: candidate.Site.ID, SiteCredentialID: id, SiteModelID: uuid.NullUUID{UUID: candidate.Model.SiteModelID, Valid: true}, Enabled: true, Available: true}
		states[index] = store.SiteAPIKeyState{SiteID: candidate.Site.ID, SiteCredentialID: id, Enabled: true, SyncStatus: "synced"}
	}
	db := gatewayStoreWithQueryCallback(t, func(tx *gorm.DB) {
		tx.RowsAffected = 1
		switch destination := tx.Statement.Dest.(type) {
		case *[]store.SiteAPIKeyModel:
			*destination = bindings
		case *[]store.SiteCredential:
			*destination = credentials
		case *[]store.SiteAPIKeyState:
			*destination = states
		case *[]store.RouteCooldown:
			*destination = nil
		case *[]store.SiteModelPricing:
			*destination = nil
		case *store.Site:
			*destination = store.Site{ID: candidate.Site.ID, SiteType: candidate.Site.SiteType, BaseURL: candidate.Site.BaseURL}
		case *store.SiteCredential:
			where, _ := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
			for _, expression := range where.Exprs {
				if equality, ok := expression.(clause.Eq); ok {
					for _, item := range credentials {
						if equality.Value == item.ID {
							*destination = item
							return
						}
					}
				}
			}
			tx.AddError(gorm.ErrRecordNotFound)
		default:
			tx.AddError(gorm.ErrRecordNotFound)
		}
	})
	manager := httpclient.NewManager(nil)
	client, err := manager.Client(httpclient.StreamingProfile(httpclient.DefaultProfile()))
	if err != nil {
		t.Fatal(err)
	}
	return Handler{logger: gatewayDiscardLogger(), db: db, credentials: service, clients: manager, httpClient: client}, credentials
}

func TestForwardCommandCodeSwitchesProtocolWithCredential(t *testing.T) {
	legacyInitialized := make(chan struct{})
	var callsMu sync.Mutex
	var mainCalls []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.URL.Path {
		case "/provider/v1/chat/completions":
			callsMu.Lock()
			mainCalls = append(mainCalls, "provider:"+key)
			callsMu.Unlock()
			if key != "pro-key" || r.Header.Get("x-api-key") != key || r.Header.Get("x-command-code-version") != "" {
				t.Errorf("provider headers = %#v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"key revoked"}}`))
		case "/alpha/generate":
			callsMu.Lock()
			mainCalls = append(mainCalls, "legacy:"+key)
			callsMu.Unlock()
			if key != "go-key" || r.Header.Get("x-command-code-version") == "" {
				t.Errorf("legacy headers = %#v", r.Header)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("{\"type\":\"start\"}\n{\"type\":\"text-start\",\"id\":\"text-0\"}\n{\"type\":\"text-delta\",\"id\":\"text-0\",\"text\":\"legacy reply\"}\n{\"type\":\"finish\",\"finishReason\":\"stop\",\"totalUsage\":{\"inputTokens\":3,\"outputTokens\":2}}\n"))
		case "/alpha/fingerprint/record":
			if key != "go-key" {
				t.Errorf("Provider key initialized the CLI: %q", key)
			}
		case "/alpha/lifecycle-events":
			if key != "go-key" {
				t.Errorf("Provider key sent a CLI lifecycle event: %q", key)
			} else {
				close(legacyInitialized)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	candidate := commandCodeProviderTestCandidate(upstreamEndpointTypeOpenAI)
	candidate.Site.ID, candidate.Model.SiteModelID = uuid.New(), uuid.New()
	candidate.Site.BaseURL = upstream.URL
	handler, credentials := credentialAdapterIntegrationHandler(t, candidate, []string{"pro-key", "go-key"}, []store.JSON{
		commandCodeProviderMetadata("Pro", "active", "2099-01-01T00:00:00Z"),
		commandCodeProviderMetadata("Go", "active", "2099-01-01T00:00:00Z"),
	})
	request := typedGatewayRequest(gatewayEndpointChatCompletions, map[string]any{"model": "model-test", "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	request.Diagnostic = true
	result := handler.forwardGatewayRequest(t.Context(), httptest.NewRecorder(), uuid.NewString(), 1, uuid.New(), uuid.New(), candidate, request, nil, newCommandCodeProtocolAdapter(request, candidate))
	if !result.success || result.credentialID != credentials[1].ID || !strings.Contains(string(result.body), "legacy reply") {
		t.Fatalf("failover result: success=%v credential=%s error=%s body=%s", result.success, result.credentialID, result.errorMessage, result.body)
	}
	callsMu.Lock()
	got := strings.Join(mainCalls, ",")
	callsMu.Unlock()
	if got != "provider:pro-key,legacy:go-key" {
		t.Fatalf("credential requests = %q", got)
	}
	proInitializationKey := fmt.Sprintf("%s:%s:%s", candidate.Site.ID, credentials[0].ID, upstream.URL)
	ccKeyStateMu.Lock()
	_, providerInitialized := ccKeyStateStore[proInitializationKey]
	ccKeyStateMu.Unlock()
	if providerInitialized {
		t.Fatal("Provider request initialized the CLI state")
	}
	select {
	case <-legacyInitialized:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy credential did not initialize the CLI")
	}
}

func TestForwardFactorySwitchesIdentityHostWithCredential(t *testing.T) {
	var callsMu sync.Mutex
	var calls []string
	newUpstream := func(secret string, fail bool) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callsMu.Lock()
			calls = append(calls, secret)
			callsMu.Unlock()
			if r.URL.Path != "/api/llm/o/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("X-Factory-Org-Id") != "" {
				t.Errorf("Factory request path=%s headers=%#v", r.URL.Path, r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			if fail {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"key revoked"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"chat-1","object":"chat.completion","model":"gemini-test","choices":[{"index":0,"message":{"role":"assistant","content":"regional reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
		}))
		t.Cleanup(server.Close)
		return server
	}
	first, second := newUpstream("first-key", true), newUpstream("second-key", false)
	candidate := routeengine.Candidate{
		Site:  routeengine.CandidateSite{ID: uuid.New(), SiteType: "factory", BaseURL: adapter.FactoryDefaultBaseURL},
		Model: routeengine.CandidateModel{SiteModelID: uuid.New(), UpstreamName: "gemini-test", SupportedEndpointTypes: []string{upstreamEndpointTypeOpenAI}},
	}
	staleOrg := "previous-key-org"
	candidate.Credential.GroupName = &staleOrg
	metadata := make([]store.JSON, 2)
	for index, base := range []string{first.URL, second.URL} {
		metadata[index], _ = json.Marshal(map[string]any{"factory_identity": map[string]any{"prem_base_host": base, "org_id": "automatic-org-must-not-be-used"}})
	}
	handler, credentials := credentialAdapterIntegrationHandler(t, candidate, []string{"first-key", "second-key"}, metadata)
	request := typedGatewayRequest(gatewayEndpointChatCompletions, map[string]any{"model": "gemini-test", "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	request.Diagnostic = true
	result := handler.forwardGatewayRequest(t.Context(), httptest.NewRecorder(), uuid.NewString(), 1, uuid.New(), uuid.New(), candidate, request, nil, newFactoryProtocolAdapter(request, candidate, handler.db))
	if !result.success || result.credentialID != credentials[1].ID || !strings.Contains(string(result.body), "regional reply") {
		t.Fatalf("failover result: success=%v credential=%s error=%s body=%s", result.success, result.credentialID, result.errorMessage, result.body)
	}
	callsMu.Lock()
	got := strings.Join(calls, ",")
	callsMu.Unlock()
	if got != "first-key,second-key" {
		t.Fatalf("Factory credential requests = %q", got)
	}
}

func TestForwardCommandCodeProviderConvertsResponsesStreamToMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider/v1/responses" || r.Header.Get("Authorization") != "Bearer pro-key" || r.Header.Get("x-api-key") != "pro-key" {
			t.Errorf("unexpected Provider request: path=%s headers=%#v", r.URL.Path, r.Header)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["input"] == nil || payload["messages"] != nil {
			t.Errorf("invalid Responses payload: %#v err=%v", payload, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp-provider","model":"model-test"}}`,
			`data: {"type":"response.output_text.delta","delta":"provider reply"}`,
			`data: {"type":"response.completed","response":{"id":"resp-provider","model":"model-test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"provider reply"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
			"",
		}, "\n\n")))
	}))
	t.Cleanup(upstream.Close)
	candidate := commandCodeProviderTestCandidate(upstreamEndpointTypeOpenAIResponse)
	candidate.Site.ID, candidate.Model.SiteModelID = uuid.New(), uuid.New()
	candidate.Site.BaseURL = upstream.URL
	handler, credentials := credentialAdapterIntegrationHandler(t, candidate, []string{"pro-key"}, []store.JSON{commandCodeProviderMetadata("Pro", "active", "2099-01-01T00:00:00Z")})
	request := typedGatewayRequest(gatewayEndpointMessages, map[string]any{"model": "model-test", "stream": true, "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	request.Stream = true
	request.Diagnostic = true
	recorder := httptest.NewRecorder()
	result := handler.forwardGatewayRequest(t.Context(), recorder, uuid.NewString(), 1, uuid.New(), uuid.New(), candidate, request, nil, newCommandCodeProtocolAdapter(request, candidate))
	if !result.success || !result.streamCompleted || result.promptTokens != 3 || result.completionTokens != 2 {
		t.Fatalf("Provider stream result: success=%v completed=%v tokens=%d/%d error=%s body=%s", result.success, result.streamCompleted, result.promptTokens, result.completionTokens, result.errorMessage, recorder.Body.String())
	}
	assertGatewayBodyContainsAll(t, recorder.Body.String(), "event: message_start", "event: content_block_delta", "provider reply", "event: message_stop")
	if strings.Contains(recorder.Body.String(), "event: response.completed") {
		t.Fatalf("upstream Responses events leaked to Messages client: %s", recorder.Body.String())
	}
	initializationKey := fmt.Sprintf("%s:%s:%s", candidate.Site.ID, credentials[0].ID, upstream.URL)
	ccKeyStateMu.Lock()
	_, initialized := ccKeyStateStore[initializationKey]
	ccKeyStateMu.Unlock()
	if initialized {
		t.Fatal("Provider stream initialized the CLI state")
	}
}
