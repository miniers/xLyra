package gateway

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

func commandCodeProviderTestCandidate(endpoint string) routeengine.Candidate {
	return routeengine.Candidate{
		Site: routeengine.CandidateSite{
			SiteType: "command_code",
			BaseURL:  "https://command-code.example",
		},
		Model: routeengine.CandidateModel{
			UpstreamName:           "model-test",
			SupportedEndpointTypes: []string{endpoint},
		},
	}
}

func commandCodeProviderMetadata(plan, status, currentPeriodEnd string) store.JSON {
	return store.JSON(`{"command_code_subscription":{"plan_id":"individual-` + strings.ToLower(strings.ReplaceAll(plan, " ", "-")) + `","plan":"` + plan + `","status":"` + status + `","current_period_end":"` + currentPeriodEnd + `"}}`)
}

func TestResolveCommandCodeCredentialProtocolSelectsLegacyOrProvider(t *testing.T) {
	candidate := commandCodeProviderTestCandidate(upstreamEndpointTypeOpenAI)
	request := gatewayRequest{DownstreamPath: gatewayEndpointChatCompletions}

	legacy, err := resolveCommandCodeCredentialProtocol(request, candidate, commandCodeProviderMetadata("Go", "active", "2099-01-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := legacy.(*commandCodeProtocolAdapter); !ok {
		t.Fatalf("Go subscription adapter = %T, want legacy command code adapter", legacy)
	}

	provider, err := resolveCommandCodeCredentialProtocol(request, candidate, commandCodeProviderMetadata("Pro", "active", "2099-01-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*commandCodeProviderProtocolAdapter); !ok {
		t.Fatalf("Pro subscription adapter = %T, want provider adapter", provider)
	}

	for _, status := range []string{"canceled", "incomplete_expired"} {
		adapter, err := resolveCommandCodeCredentialProtocol(request, candidate, commandCodeProviderMetadata("Pro", status, "2099-01-01T00:00:00Z"))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := adapter.(*commandCodeProtocolAdapter); !ok {
			t.Fatalf("status %q adapter = %T, want legacy adapter", status, adapter)
		}
	}
	unknown, err := resolveCommandCodeCredentialProtocol(request, candidate, store.JSON(`{"command_code_subscription":{"plan_id":"individual-mystery","plan":"Pro","status":"active","current_period_end":"2099-01-01T00:00:00Z"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unknown.(*commandCodeProtocolAdapter); !ok {
		t.Fatalf("unknown plan adapter = %T, want legacy adapter", unknown)
	}

	expired, err := resolveCommandCodeCredentialProtocol(request, candidate, commandCodeProviderMetadata("Pro", "active", "2020-01-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := expired.(*commandCodeProtocolAdapter); !ok {
		t.Fatalf("expired subscription adapter = %T, want legacy adapter", expired)
	}
}

func TestCommandCodeProviderProtocolPathsAndRequestShapes(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		endpoint   string
		wantSuffix string
		wantModel  string
	}{
		{name: "chat", path: gatewayEndpointChatCompletions, endpoint: upstreamEndpointTypeOpenAI, wantSuffix: "/provider/v1/chat/completions", wantModel: "chat-model"},
		{name: "responses", path: gatewayEndpointResponses, endpoint: upstreamEndpointTypeOpenAIResponse, wantSuffix: "/provider/v1/responses", wantModel: "responses-model"},
		{name: "messages", path: gatewayEndpointMessages, endpoint: upstreamEndpointTypeAnthropicMessages, wantSuffix: "/provider/v1/messages", wantModel: "messages-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := commandCodeProviderTestCandidate(tc.endpoint)
			candidate.Model.UpstreamName = tc.wantModel
			request := gatewayRequest{
				DownstreamPath: tc.path,
				RequestedModel: tc.wantModel,
				Payload:        map[string]any{"model": tc.wantModel, "messages": []any{map[string]any{"role": "user", "content": "hello"}}},
			}
			adapter, err := resolveCommandCodeCredentialProtocol(request, candidate, commandCodeProviderMetadata("Pro", "active", "2099-01-01T00:00:00Z"))
			if err != nil {
				t.Fatal(err)
			}
			provider, ok := adapter.(*commandCodeProviderProtocolAdapter)
			if !ok {
				t.Fatalf("adapter = %T, want provider adapter", adapter)
			}
			if got := provider.UpstreamPath(candidate.Site.BaseURL); !strings.HasSuffix(got, tc.wantSuffix) {
				t.Fatalf("UpstreamPath = %q, want suffix %q", got, tc.wantSuffix)
			}
			payload, err := provider.BuildUpstreamPayload(request, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if payload["model"] != tc.wantModel {
				t.Fatalf("payload model = %#v, want %q", payload["model"], tc.wantModel)
			}
		})
	}
}

func TestCommandCodeProviderHeadersKeepBothAuthenticationSchemes(t *testing.T) {
	candidate := commandCodeProviderTestCandidate(upstreamEndpointTypeOpenAI)
	request := gatewayRequest{DownstreamPath: gatewayEndpointChatCompletions}
	adapter, err := resolveCommandCodeCredentialProtocol(request, candidate, commandCodeProviderMetadata("Pro", "active", "2099-01-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	provider := adapter.(*commandCodeProviderProtocolAdapter)
	req := &http.Request{Header: make(http.Header)}
	provider.ApplyUpstreamHeaders(req, "cc-key", "", true)
	if req.Header.Get("Authorization") != "Bearer cc-key" || req.Header.Get("x-api-key") != "cc-key" {
		t.Fatalf("authentication headers = %#v", req.Header)
	}
	if req.Header.Get("x-command-code-version") != "" || req.Header.Get("x-session-id") != "" {
		t.Fatalf("legacy CLI headers leaked: %#v", req.Header)
	}
}

func TestPrefixCommandCodeProviderPathPreservesQuery(t *testing.T) {
	for _, prefix := range []string{"", "/proxy"} {
		got := prefixCommandCodeProviderPath("https://example.test" + prefix + "/v1/responses?x=1")
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Path != prefix+"/provider/v1/responses" || parsed.Query().Get("x") != "1" {
			t.Fatalf("provider path = %q", got)
		}
		if twice := prefixCommandCodeProviderPath(got); twice != got {
			t.Fatalf("provider prefix applied twice: %q", twice)
		}
	}
}

func TestCommandCodeProviderPeriodEndAcceptsUnixMilliseconds(t *testing.T) {
	future := float64(time.Now().Add(time.Hour).UnixMilli())
	parsed, ok := commandCodeProviderPeriodEnd(future)
	if !ok || !parsed.After(time.Now()) {
		t.Fatalf("period end = %v, ok=%v", parsed, ok)
	}
}
