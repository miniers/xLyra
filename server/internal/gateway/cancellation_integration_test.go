package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"xlyra/server/internal/auth"
	"xlyra/server/internal/credential"
	"xlyra/server/internal/httpclient"
	"xlyra/server/internal/inflight"
	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

type cancellationGatewayProtocol struct {
	upstreamDone <-chan struct{}
}

func (p cancellationGatewayProtocol) ProtocolName() string { return "cancellation_test" }

func (p cancellationGatewayProtocol) BuildUpstreamPayload(gatewayRequest, routeengine.Candidate) (map[string]any, error) {
	return map[string]any{"model": "test-model", "stream": true}, nil
}

func (p cancellationGatewayProtocol) UpstreamPath(baseURL string) string { return baseURL + "/stream" }

func (p cancellationGatewayProtocol) TransformBufferedResponse(int, http.Header, []byte) (gatewayBufferedResponse, error) {
	return gatewayBufferedResponse{StatusCode: http.StatusOK}, nil
}

func (p cancellationGatewayProtocol) ProxyStream(ctx context.Context, _ http.ResponseWriter, _ *http.Response, _ time.Time, _ routeengine.Candidate) (streamCaptureState, bool, error) {
	select {
	case <-ctx.Done():
		return streamCaptureState{endReason: "downstream_client_cancelled"}, false, ctx.Err()
	case <-p.upstreamDone:
		return streamCaptureState{}, false, errors.New("upstream ended before cancellation")
	}
}

func TestForwardGatewayRequestCancellationClosesUpstreamConnection(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"id\":\"cancel\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(upstreamDone)
	}))
	defer upstream.Close()

	siteID := uuid.New()
	siteModelID := uuid.New()
	credentialID := uuid.New()
	credentialService := credential.NewService("test-master-key")
	encrypted, masked, err := credentialService.Encrypt("test-secret")
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	db := gatewayGormWithQueryCallback(t, func(tx *gorm.DB) {
		switch dest := tx.Statement.Dest.(type) {
		case *[]store.SiteAPIKeyModel:
			*dest = []store.SiteAPIKeyModel{{SiteID: siteID, SiteCredentialID: credentialID, SiteModelID: uuid.NullUUID{UUID: siteModelID, Valid: true}, Available: true, Enabled: true}}
			tx.Statement.RowsAffected = 1
		case *[]store.SiteCredential:
			*dest = []store.SiteCredential{{ID: credentialID, SiteID: siteID, CredentialType: "api_key", EncryptedSecret: encrypted, MaskedSecret: masked, Meta: store.JSON(`{"enabled":true}`)}}
			tx.Statement.RowsAffected = 1
		case *[]store.SiteAPIKeyState:
			*dest = []store.SiteAPIKeyState{{SiteID: siteID, SiteCredentialID: credentialID, Enabled: true, SyncStatus: "synced"}}
			tx.Statement.RowsAffected = 1
		case *[]store.RouteCooldown:
			*dest = nil
			tx.Statement.RowsAffected = 0
		case *[]store.SiteModelPricing:
			*dest = nil
			tx.Statement.RowsAffected = 0
		case *store.Site:
			*dest = store.Site{ID: siteID, BaseURL: upstream.URL}
			tx.Statement.RowsAffected = 1
		default:
			tx.AddError(gorm.ErrRecordNotFound)
		}
	})

	manager := httpclient.NewManager(nil)
	client, err := manager.Client(httpclient.StreamingProfile(httpclient.DefaultProfile()))
	if err != nil {
		t.Fatalf("create http client: %v", err)
	}
	handler := Handler{
		logger:      gatewayDiscardLogger(),
		db:          gatewayStoreWithGorm(t, db),
		credentials: credentialService,
		clients:     manager,
		httpClient:  client,
		recorder:    Recorder{},
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	protocol := cancellationGatewayProtocol{upstreamDone: upstreamDone}
	done := make(chan gatewayAttemptResult, 1)
	go func() {
		done <- handler.forwardGatewayRequest(ctx, httptest.NewRecorder(), "cancel-test", 1, uuid.New(), uuid.New(), routeengine.Candidate{
			Site:  routeengine.CandidateSite{ID: siteID, BaseURL: upstream.URL},
			Model: routeengine.CandidateModel{SiteModelID: siteModelID, UpstreamName: "test-model"},
		}, gatewayRequest{DownstreamPath: gatewayEndpointChatCompletions, Stream: true}, nil, protocol)
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel(context.Canceled)

	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream connection was not cancelled")
	}
	select {
	case result := <-done:
		if result.errorType != "downstream_client_cancelled" {
			t.Fatalf("result error type = %q, want downstream_client_cancelled", result.errorType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway request did not return after cancellation")
	}
}

func TestChatCompletionsCancelRequestClosesUpstreamConnection(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(upstreamDone)
	}))
	t.Cleanup(upstream.Close)

	canonical := store.CanonicalModel{ID: uuid.New(), ModelKey: "cancel-model", Provider: "openai", Status: "active"}
	site := store.Site{
		ID:       uuid.New(),
		Name:     "Cancellation Site",
		Slug:     "cancellation-site",
		SiteType: "openai",
		BaseURL:  upstream.URL,
		Enabled:  true,
		Status:   "active",
	}
	credentialService := credential.NewService("test-master-key")
	encrypted, masked, err := credentialService.Encrypt("test-secret")
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	apiKey := store.APIKey{
		ID:             uuid.New(),
		Name:           "cancellation-key",
		Status:         "active",
		ModelPolicy:    "allow_all",
		SitePolicy:     "allow_all",
		QuotaUnlimited: true,
	}
	harness := &softMappingHarness{
		apiKey:          apiKey,
		canonicalModels: map[string]store.CanonicalModel{canonical.ModelKey: canonical},
		siteModels:      []store.SiteModel{softMappingSiteModel(canonical, site, "cancel-upstream-model")},
		sites:           map[uuid.UUID]store.Site{site.ID: site},
		credentials: map[uuid.UUID]store.SiteCredential{
			site.ID: {
				ID:              uuid.New(),
				SiteID:          site.ID,
				CredentialType:  "api_key",
				EncryptedSecret: encrypted,
				MaskedSecret:    masked,
			},
		},
	}
	handler := newSoftMappingHandler(t, harness)

	requestID := "cancel-handler-" + uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"cancel-model","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithAPIKey(req.Context(), apiKey))
	req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, requestID))
	recorder := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() {
		handler.ChatCompletions(recorder, req)
		close(handlerDone)
	}()

	deadline := time.After(2 * time.Second)
	for {
		request, ok := inflight.CurrentRequest(requestID)
		if ok && request.CanCancel {
			break
		}
		select {
		case <-deadline:
			t.Fatal("live request did not expose a cancel control")
		case <-time.After(5 * time.Millisecond):
		}
	}

	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not start")
	}
	if !inflight.CancelRequest(requestID) {
		t.Fatal("cancel request was not accepted")
	}

	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream connection was not cancelled")
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway handler did not return after cancellation")
	}
	if request, ok := inflight.CurrentRequest(requestID); !ok || request.Phase != inflight.PhaseCancelled {
		t.Fatalf("cancelled request = %#v, found = %v", request, ok)
	}
}
