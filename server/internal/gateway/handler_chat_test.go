package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

func TestChatCompletionsGatewayUnavailableUsesSharedFailureEnvelope(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, nil, nil, nil, "")
	req := gatewayRequestWithID(http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o-mini"}`, "req-123")
	rec := httptest.NewRecorder()

	handler.ChatCompletions(rec, req)

	assertGatewayErrorEnvelope(t, rec, http.StatusServiceUnavailable, "gateway_unavailable", "req-123")
}

func TestGatewayEndpointWrappersUseSharedUnavailableEnvelope(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, nil, nil, nil, "")
	tests := []struct {
		name   string
		path   string
		body   string
		call   func(http.ResponseWriter, *http.Request)
		reqID  string
		status int
		code   string
	}{
		{
			name:  "responses",
			path:  gatewayEndpointResponses,
			body:  `{"model":"gpt-5","input":"hi"}`,
			call:  handler.Responses,
			reqID: "req-responses",
		},
		{
			name:  "messages",
			path:  gatewayEndpointMessages,
			body:  `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`,
			call:  handler.Messages,
			reqID: "req-messages",
		},
		{
			name:  "images",
			path:  gatewayEndpointImagesGenerations,
			body:  `{"model":"gpt-image-2","prompt":"draw"}`,
			call:  handler.ImagesGenerations,
			reqID: "req-images",
		},
		{
			name:  "embeddings",
			path:  gatewayEndpointEmbeddings,
			body:  `{"model":"text-embedding-3-large","input":"hi"}`,
			call:  handler.Embeddings,
			reqID: "req-embeddings",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := gatewayRequestWithID(http.MethodPost, tt.path, tt.body, tt.reqID)
			rec := httptest.NewRecorder()

			tt.call(rec, req)

			assertGatewayErrorEnvelope(t, rec, http.StatusServiceUnavailable, "gateway_unavailable", tt.reqID)
		})
	}
}

func TestWriteChatFailureUsesSharedEnvelope(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, nil, nil, nil, "")
	req := gatewayRequestWithID(http.MethodPost, "/v1/chat/completions", "", "req-456")
	rec := httptest.NewRecorder()

	handler.writeChatFailure(rec, req, gatewayEndpointChatCompletions, "req-456", uuid.Nil, time.Now(), chatFailure{
		status:  http.StatusBadRequest,
		code:    "invalid_json",
		message: "request body must be valid JSON",
		stage:   "decode",
	})

	assertGatewayErrorEnvelope(t, rec, http.StatusBadRequest, "invalid_json", "req-456")
}

func TestWriteChatFailureIncludesUnsupportedReasoningEffortValue(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, nil, nil, nil, "")
	req := gatewayRequestWithID(http.MethodPost, "/v1/chat/completions", "", "req-effort")
	rec := httptest.NewRecorder()
	message := `reasoning_effort value "light" is not supported; must be one of ` + supportedReasoningEfforts

	handler.writeChatFailure(rec, req, gatewayEndpointChatCompletions, "req-effort", uuid.Nil, time.Now(), chatFailure{
		status:  http.StatusBadRequest,
		code:    "invalid_reasoning_effort",
		message: message,
		stage:   "validate",
	})

	if !strings.Contains(rec.Body.String(), `reasoning_effort value \"light\" is not supported`) {
		t.Fatalf("response body = %s", rec.Body.String())
	}
}

func TestLockedRouteUnavailableClassifiesPreResponseFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		item   *gatewayAttemptResult
		image  bool
		params bool
		want   bool
	}{
		{name: "credential unavailable", item: &gatewayAttemptResult{errorType: "upstream_credential_unavailable"}, want: true},
		{name: "transport failure", item: &gatewayAttemptResult{errorType: "upstream_transport_error", statusCode: http.StatusBadGateway}, want: true},
		{name: "first byte timeout", item: &gatewayAttemptResult{errorType: "upstream_first_byte_timeout", statusCode: http.StatusGatewayTimeout}, want: true},
		{name: "oauth refresh failure", item: &gatewayAttemptResult{errorType: "codex_oauth_refresh_failed", statusCode: http.StatusBadGateway}, want: true},
		{name: "endpoint resolution failure", item: &gatewayAttemptResult{errorType: "protocol_resolution_failed", statusCode: http.StatusBadGateway}, want: true},
		{name: "upstream not found", item: &gatewayAttemptResult{errorType: "upstream_http_error", statusCode: http.StatusBadGateway, upstreamStatusCode: http.StatusNotFound}, want: true},
		{name: "semantic request failure", item: &gatewayAttemptResult{errorType: "upstream_response_failed", statusCode: http.StatusBadGateway, upstreamStatusCode: http.StatusOK}, want: false},
		{name: "rate limited remains upstream error", item: &gatewayAttemptResult{errorType: "upstream_http_error", statusCode: http.StatusTooManyRequests, upstreamStatusCode: http.StatusTooManyRequests}, want: false},
		{name: "response already started", item: &gatewayAttemptResult{errorType: "upstream_transport_error", statusCode: http.StatusBadGateway, responseStarted: true}, want: false},
		{name: "incompatible image", image: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lockedRouteUnavailable(tt.item, tt.image, tt.params); got != tt.want {
				t.Fatalf("lockedRouteUnavailable = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeminiGatewayErrorUsesGeminiEnvelope(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, nil, nil, nil, "")
	req := gatewayRequestWithID(http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent", "", "req-gemini")
	rec := httptest.NewRecorder()
	handler.writeChatFailure(rec, req, gatewayEndpointGeminiGenerate, "req-gemini", uuid.Nil, time.Now(), chatFailure{
		status:  http.StatusBadRequest,
		code:    "invalid_gemini_request",
		message: "contents must be a non-empty array",
		stage:   "validate",
	})

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"status":"INVALID_GEMINI_REQUEST"`) || !strings.Contains(rec.Body.String(), `"request_id":"req-gemini"`) {
		t.Fatalf("Gemini error response = %s", rec.Body.String())
	}
}

func TestResolveModelMappingNormalizesDownstreamModel(t *testing.T) {
	t.Parallel()

	handler := Handler{}
	apiKey := store.APIKey{
		ModelMappings: store.JSON(`{
			" GPT_5 Codex ": "gpt-5-codex-upstream",
			"claude-sonnet": "claude-4-sonnet"
		}`),
	}

	got, ok := handler.resolveModelMapping(apiKey, "gpt-5-codex")
	if !ok {
		t.Fatal("expected model mapping to match normalized downstream model")
	}
	if got.Target != "gpt-5-codex-upstream" {
		t.Fatalf("mapped model = %q, want gpt-5-codex-upstream", got.Target)
	}
}

func TestResolveModelMappingIgnoresInvalidOrMissingMappings(t *testing.T) {
	t.Parallel()

	handler := Handler{}
	for _, apiKey := range []store.APIKey{
		{},
		{ModelMappings: store.JSON(`{invalid-json`)},
		{ModelMappings: store.JSON(`{"claude-sonnet":"claude-4-sonnet"}`)},
	} {
		if got, ok := handler.resolveModelMapping(apiKey, "gpt-5"); ok || got.Target != "" {
			t.Fatalf("expected no mapping, got mapped=%#v ok=%v", got, ok)
		}
	}
}
