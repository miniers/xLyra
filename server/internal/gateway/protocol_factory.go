package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/adapter"
	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

const (
	factorySystemPrefix        = "You are Droid, an AI software engineering agent built by Factory.\n\n"
	factorySystemDeclaration   = "You are Droid, an AI software engineering agent built by Factory."
	factoryHeaderClientVersion = "0.232.0"
	factoryHeaderUserAgent     = "factory-cli/0.232.0"
	factoryHeaderClient        = "cli"
	factoryOpenAIPlatform      = "org-bHuLtG1fGmYk5YaOihAAXFBw"
	factoryAnthropicVersion    = "2023-06-01"
)

type factoryRouteTarget int

const (
	factoryRouteClaude factoryRouteTarget = iota
	factoryRouteMiniMax
	factoryRouteGPT
	factoryRouteCoreOpenSource
)

func classifyFactoryRoute(model string) factoryRouteTarget {
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "minimax") {
		return factoryRouteMiniMax
	}
	if strings.HasPrefix(lower, "claude") {
		return factoryRouteClaude
	}
	if strings.HasPrefix(lower, "gpt") || strings.Contains(lower, "codex") {
		return factoryRouteGPT
	}
	return factoryRouteCoreOpenSource
}

type factoryProtocolAdapter struct {
	request    gatewayRequest
	candidate  routeengine.Candidate
	target     factoryRouteTarget
	delegate   gatewayProtocolAdapter
	db         *store.Store
	downstream canonicalProtocol
}

var (
	_ gatewayProtocolAdapter                = (*factoryProtocolAdapter)(nil)
	_ gatewayProtocolHeaderAdapter          = (*factoryProtocolAdapter)(nil)
	_ gatewayProtocolCredentialCapabilities = (*factoryProtocolAdapter)(nil)
)

func newFactoryProtocolAdapter(request gatewayRequest, candidate routeengine.Candidate, dbs ...*store.Store) *factoryProtocolAdapter {
	var db *store.Store
	if len(dbs) > 0 {
		db = dbs[0]
	}
	model := candidate.Model.UpstreamName
	if model == "" {
		model = request.RequestedModel
	}
	target := classifyFactoryRoute(model)
	downstream := downstreamCanonicalProtocol(request.DownstreamPath)

	var delegate gatewayProtocolAdapter
	switch target {
	case factoryRouteClaude, factoryRouteMiniMax:
		delegate = newAnthropicMessagesProtocolAdapter(downstream)
	case factoryRouteGPT:
		delegate = newOpenAIResponsesProtocolAdapterForCandidate(request, candidate)
	case factoryRouteCoreOpenSource:
		delegate = newOpenAIChatProtocolAdapter(request, candidate)
	}

	return &factoryProtocolAdapter{
		request:    request,
		candidate:  candidate,
		target:     target,
		delegate:   delegate,
		db:         db,
		downstream: downstream,
	}
}

func (a *factoryProtocolAdapter) ProtocolName() string {
	return "factory"
}

func (a *factoryProtocolAdapter) CredentialEndpointTypes() []string {
	return textCredentialEndpointTypes()
}

func (a *factoryProtocolAdapter) UpstreamPath(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = adapter.FactoryDefaultBaseURL
	}
	switch a.target {
	case factoryRouteClaude, factoryRouteMiniMax:
		return base + "/api/llm/a/v1/messages"
	case factoryRouteGPT:
		return base + "/api/llm/o/v1/responses"
	case factoryRouteCoreOpenSource:
		return base + "/api/llm/o/v1/chat/completions"
	}
	return base + "/api/llm/o/v1/chat/completions"
}

func (a *factoryProtocolAdapter) BuildUpstreamPayload(request gatewayRequest, candidate routeengine.Candidate) (map[string]any, error) {
	reqCopy := request
	if reqCopy.Canonical != nil {
		reqCopy.Canonical = ensureFactoryCanonicalSystemPrompt(reqCopy.Canonical)
	}

	payload, err := a.delegate.BuildUpstreamPayload(reqCopy, candidate)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		payload = make(map[string]any)
	}

	ensureFactoryPayloadSystemPrompt(payload, a.target)

	modelName := candidate.Model.UpstreamName
	if modelName == "" {
		modelName = request.RequestedModel
	}
	if modelName != "" {
		payload["model"] = modelName
	}

	return payload, nil
}

func (a *factoryProtocolAdapter) TransformBufferedResponse(statusCode int, headers http.Header, body []byte) (gatewayBufferedResponse, error) {
	return a.delegate.TransformBufferedResponse(statusCode, headers, body)
}

func (a *factoryProtocolAdapter) ProxyStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, startedAt time.Time, candidate routeengine.Candidate) (streamCaptureState, bool, error) {
	return a.delegate.ProxyStream(ctx, w, resp, startedAt, candidate)
}

func (a *factoryProtocolAdapter) ApplyUpstreamHeaders(req *http.Request, upstreamKey string, accountID string, _ bool) {
	if req == nil {
		return
	}

	if strings.TrimSpace(upstreamKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(upstreamKey))
	}
	req.Header.Set("X-Factory-Client", factoryHeaderClient)
	req.Header.Set("X-Client-Version", factoryHeaderClientVersion)
	req.Header.Set("User-Agent", factoryHeaderUserAgent)

	if orgID := a.resolveOrgID(accountID); orgID != "" {
		req.Header.Set("X-Factory-Org-Id", orgID)
	}

	req.Header.Set("x-session-id", uuid.NewString())
	req.Header.Set("x-assistant-message-id", uuid.NewString())

	switch a.target {
	case factoryRouteClaude:
		req.Header.Set("x-api-provider", "anthropic")
		req.Header.Set("anthropic-version", factoryAnthropicVersion)
	case factoryRouteMiniMax:
		req.Header.Set("x-api-provider", "fireworks")
		req.Header.Set("anthropic-version", factoryAnthropicVersion)
	case factoryRouteGPT:
		req.Header.Set("x-api-provider", "openai")
		req.Header.Set("OpenAI-Platform", factoryOpenAIPlatform)
	case factoryRouteCoreOpenSource:
		req.Header.Set("x-api-provider", "fireworks")
	}
}

func (a *factoryProtocolAdapter) resolveOrgID(accountID string) string {
	if trimmed := strings.TrimSpace(accountID); trimmed != "" {
		return trimmed
	}
	if a.request.DownstreamHeaders != nil {
		for _, key := range []string{"X-Factory-Org-Id", "x-factory-org-id", "x-org-id", "X-Org-Id"} {
			if v := strings.TrimSpace(a.request.DownstreamHeaders.Get(key)); v != "" {
				return v
			}
		}
	}
	if a.candidate.Credential.GroupName != nil && strings.TrimSpace(*a.candidate.Credential.GroupName) != "" {
		return strings.TrimSpace(*a.candidate.Credential.GroupName)
	}
	if a.db != nil {
		if a.candidate.Credential.ID != nil {
			cred, err := store.NewSiteCredentialRepository(a.db.DB()).GetByID(context.Background(), *a.candidate.Credential.ID)
			if err == nil && len(cred.Meta) > 0 {
				var credMeta map[string]any
				if json.Unmarshal(cred.Meta, &credMeta) == nil {
					for _, k := range []string{"orgId", "org_id", "organization", "org"} {
						if v, ok := credMeta[k].(string); ok && strings.TrimSpace(v) != "" {
							return strings.TrimSpace(v)
						}
					}
				}
			}
		}
		site, err := store.NewSiteRepository(a.db.DB()).GetByID(context.Background(), a.candidate.Site.ID)
		if err == nil && len(site.Meta) > 0 {
			var siteMeta map[string]any
			if json.Unmarshal(site.Meta, &siteMeta) == nil {
				for _, k := range []string{"orgId", "org_id", "organization", "org"} {
					if v, ok := siteMeta[k].(string); ok && strings.TrimSpace(v) != "" {
						return strings.TrimSpace(v)
					}
				}
			}
		}
	}
	return ""
}

func ensureFactoryCanonicalSystemPrompt(req *canonicalRequest) *canonicalRequest {
	if req == nil {
		return nil
	}
	cloned := *req
	trimmedInst := strings.TrimSpace(cloned.Instructions)
	if trimmedInst == "" {
		cloned.Instructions = factorySystemPrefix
	} else if !strings.HasPrefix(trimmedInst, factorySystemDeclaration) {
		cloned.Instructions = factorySystemPrefix + trimmedInst
	}
	return &cloned
}

func ensureFactoryPayloadSystemPrompt(payload map[string]any, target factoryRouteTarget) {
	switch target {
	case factoryRouteClaude, factoryRouteMiniMax:
		sysVal, exists := payload["system"]
		if !exists || sysVal == nil {
			payload["system"] = factorySystemPrefix
			return
		}
		switch v := sysVal.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if !strings.HasPrefix(trimmed, factorySystemDeclaration) {
				payload["system"] = factorySystemPrefix + v
			}
		case []any:
			if len(v) == 0 {
				payload["system"] = factorySystemPrefix
				return
			}
			if first, ok := v[0].(map[string]any); ok {
				text := anyString(first["text"])
				if !strings.HasPrefix(strings.TrimSpace(text), factorySystemDeclaration) {
					first["text"] = factorySystemPrefix + text
				}
			} else {
				payload["system"] = append([]any{map[string]any{"type": "text", "text": factorySystemPrefix}}, v...)
			}
		default:
			payload["system"] = factorySystemPrefix
		}

	case factoryRouteGPT:
		instVal, exists := payload["instructions"]
		if !exists || instVal == nil {
			payload["instructions"] = factorySystemPrefix
			return
		}
		switch v := instVal.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				payload["instructions"] = factorySystemPrefix
			} else if !strings.HasPrefix(trimmed, factorySystemDeclaration) {
				payload["instructions"] = factorySystemPrefix + v
			}
		default:
			payload["instructions"] = factorySystemPrefix
		}

	case factoryRouteCoreOpenSource:
		var messages []any
		switch v := payload["messages"].(type) {
		case []any:
			messages = v
		case []map[string]any:
			messages = make([]any, len(v))
			for i, m := range v {
				messages[i] = m
			}
		}

		if len(messages) == 0 {
			payload["messages"] = []any{
				map[string]any{
					"role":    "system",
					"content": factorySystemPrefix,
				},
			}
			return
		}

		foundSystem := false
		for _, item := range messages {
			msgMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(anyString(msgMap["role"])))
			if role == "system" || role == "developer" {
				foundSystem = true
				switch content := msgMap["content"].(type) {
				case string:
					trimmed := strings.TrimSpace(content)
					if !strings.HasPrefix(trimmed, factorySystemDeclaration) {
						msgMap["content"] = factorySystemPrefix + content
					}
				case []any:
					if len(content) > 0 {
						if firstPart, ok := content[0].(map[string]any); ok {
							text := anyString(firstPart["text"])
							if !strings.HasPrefix(strings.TrimSpace(text), factorySystemDeclaration) {
								firstPart["text"] = factorySystemPrefix + text
							}
						}
					}
				default:
					msgMap["content"] = factorySystemPrefix
				}
				break
			}
		}

		if !foundSystem {
			sysMsg := map[string]any{
				"role":    "system",
				"content": factorySystemPrefix,
			}
			messages = append([]any{sysMsg}, messages...)
		}
		payload["messages"] = messages
	}
}
