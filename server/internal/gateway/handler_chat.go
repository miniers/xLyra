package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"xlyra/server/internal/auth"
	"xlyra/server/internal/catalog"
	"xlyra/server/internal/inflight"
	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

func (h Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, chatCompletionsEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) Responses(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, responsesEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) Messages(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, anthropicMessagesEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, imagesGenerationsEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) ImagesEdits(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, imagesEditsEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) Embeddings(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, embeddingsEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) AudioSpeech(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, audioSpeechEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) TypeSafeSystemOne(w http.ResponseWriter, r *http.Request) {
	h.serveEndpoint(w, r, typeSafeSystemOneEndpointAdapter{}, openAIProtocolResolver{db: h.db})
}

func (h Handler) GeminiGenerateContent(stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.serveEndpoint(w, r, geminiGenerateContentEndpointAdapter{stream: stream}, openAIProtocolResolver{db: h.db})
	}
}

func rateLimitTokenCount(result gatewayAttemptResult) int64 {
	return int64(result.promptTokens) + int64(result.completionTokens) + int64(result.audioOutputTokens)
}

func inflightTokenAmount(result gatewayAttemptResult) inflight.TokenAmount {
	return inflight.TokenAmount{
		Total:  rateLimitTokenCount(result),
		Input:  int64(result.promptTokens),
		Output: int64(result.completionTokens) + int64(result.audioOutputTokens),
		Cached: int64(result.cachedPromptTokens),
	}
}

func (h Handler) serveEndpoint(
	w http.ResponseWriter,
	r *http.Request,
	endpoint gatewayEndpointAdapter,
	resolver upstreamProtocolResolver,
) {
	defer inflight.Enter()()

	requestID := middleware.GetReqID(r.Context())
	if requestID == "" || inflight.IsActive(requestID) {
		requestID = uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
	}
	startedAt := time.Now()
	ctx := withRequestStartedAt(r.Context(), startedAt)
	r = r.WithContext(ctx)

	if h.auth == nil || h.router == nil || h.db == nil {
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, uuid.Nil, startedAt, chatFailure{
			status:  http.StatusServiceUnavailable,
			code:    "gateway_unavailable",
			message: "gateway service is not available",
			stage:   "gateway",
		})
		return
	}

	apiKey, ok := auth.APIKeyFromContext(r.Context())
	if !ok {
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, uuid.Nil, startedAt, chatFailure{
			status:  http.StatusUnauthorized,
			code:    "unauthorized",
			message: "valid api key is required",
			stage:   "auth",
		})
		return
	}
	requestCtx, cancelRequest := context.WithCancelCause(ctx)
	defer cancelRequest(nil)
	requestControl := newGatewayRequestControl(cancelRequest)
	ctx = withGatewayRequestControl(requestCtx, requestControl)
	ctx = WithAPIKeyBillingMultiplier(ctx, apiKey.BillingMultiplier)
	r = r.WithContext(ctx)

	request, failure := endpoint.DecodeRequest(r)
	if failure != nil {
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, *failure)
		return
	}

	request.OriginalModel = request.RequestedModel
	ctx = withRequestedModel(ctx, request.OriginalModel)
	ctx = withReasoningEffort(ctx, reasoningEffortFromPayload(request.Payload))
	r = r.WithContext(ctx)
	originalModel := request.RequestedModel
	mappingRule, hasMapping := h.resolveModelMapping(apiKey, request.RequestedModel)
	softFallbackTarget := ""
	if hasMapping {
		if mappingRule.Mode == store.APIKeyModelRuleModeSoft {
			softFallbackTarget = mappingRule.Target
		} else {
			request.RequestedModel = mappingRule.Target
			ctx = withModelMapping(ctx, originalModel, mappingRule.Target, store.APIKeyModelRuleModeHard)
			r = r.WithContext(ctx)
		}
	}

	// Register before route planning and rate-limit acquisition so an admin can
	// cancel work that is still in the gateway preflight phase.
	liveRequest := inflight.Request{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID.String(),
		APIKeyName:  apiKey.Name,
		ModelKey:    request.RequestedModel,
		Stream:      request.Stream,
		StartedAt:   startedAt,
		InputTokens: estimateGatewayInputTokens(request),
	}
	for !inflight.Start(liveRequest) {
		requestID = uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
		liveRequest.RequestID = requestID
	}
	inflight.AttachControl(requestID, requestControl.Cancel)
	flowPhase := inflight.PhaseFailed
	defer func() {
		inflight.Finish(requestID, flowPhase)
	}()

	imageIntent := codexImageIntentForRequest(request)
	bridge, bridgeSkipReason := bridgeContextForRequest(apiKey, request, imageIntent)
	if bridge != nil {
		defer bridge.Close()
	}
	if h.logger != nil && codexPayloadHasImageGenerationTool(request.Payload) {
		if bridge != nil {
			h.logger.InfoContext(ctx, "image bridge gate open", "scope", "gateway", "request_id", requestID, "bridge_model", bridge.cfg.Model, "max_calls", bridge.cfg.MaxCalls)
		} else {
			h.logger.InfoContext(ctx, "image bridge gate closed", "scope", "gateway", "request_id", requestID, "reason", bridgeSkipReason)
		}
	}

	setup := h.setupRoute(ctx, apiKey.ID, endpoint, request, imageIntent, bridge != nil)
	if setup.failure != nil && softFallbackTarget != "" && !routeSetupHasStrictLock(setup) {
		fallbackCtx := withModelMapping(ctx, originalModel, softFallbackTarget, store.APIKeyModelRuleModeSoft)
		fallbackRequest := request
		fallbackRequest.RequestedModel = softFallbackTarget
		fallbackIntent := codexImageIntentForRequest(fallbackRequest)
		fallbackSetup := h.setupRoute(fallbackCtx, apiKey.ID, endpoint, fallbackRequest, fallbackIntent, bridge != nil)
		ctx = fallbackCtx
		r = r.WithContext(ctx)
		request = fallbackRequest
		imageIntent = fallbackIntent
		setup = fallbackSetup
		softFallbackTarget = ""
		if h.logger != nil {
			h.logger.InfoContext(ctx, "soft model mapping fallback engaged at planning", "scope", "gateway", "request_id", requestID, "original_model", originalModel, "fallback_model", request.RequestedModel, "fallback_ok", setup.failure == nil)
		}
	}
	if errors.Is(context.Cause(ctx), context.Canceled) {
		flowPhase = inflight.PhaseCancelled
		return
	}
	if setup.failure != nil {
		ctx = withRouteLockMetadata(ctx, setup.routingLock, false)
		r = r.WithContext(ctx)
		if setup.noRoute != nil {
			h.writeNoRouteFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, request, *setup.noRoute)
			return
		}
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, *setup.failure)
		return
	}
	access := setup.access
	plan := setup.plan
	ctx = withRouteLockMetadata(ctx, plan.RoutingLock, false)
	r = r.WithContext(ctx)
	request.EffectiveModelKey = plan.CanonicalModel.ModelKey
	request.APIKeyGatewayConfig = apiKey.Gateway()
	inflight.SetModel(requestID, plan.CanonicalModel.ModelKey, plan.CanonicalModel.Provider)

	reservation, limitErr, limitAcquireErr := h.acquireRateLimit(ctx, apiKey.ID, endpoint, request, startedAt)
	if errors.Is(context.Cause(ctx), context.Canceled) {
		flowPhase = inflight.PhaseCancelled
		return
	}
	if limitAcquireErr != nil {
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, chatFailure{
			status:         http.StatusInternalServerError,
			code:           "rate_limit_unavailable",
			message:        "failed to check rate limit",
			requestedModel: request.RequestedModel,
			stream:         request.Stream,
			stage:          "rate_limit",
		})
		return
	}
	if limitErr != nil {
		h.writeRateLimitFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, request, *limitErr)
		return
	}
	ctx = h.withCacheObservation(ctx, apiKey.ID, plan.CanonicalModel.ID, request)
	ctx = h.withCacheShadowAffinity(ctx, apiKey.ID, plan.CanonicalModel.ID, request, plan, resolver)
	r = r.WithContext(ctx)
	actualRateLimitTokens := int64(0)
	if reservation != nil {
		ctx = withRateLimitMetadata(ctx, reservation.Metadata(0))
		settlementCtx := ctx
		defer func() {
			h.settleRateLimit(settlementCtx, reservation, actualRateLimitTokens)
		}()
	}
	if requestUsesDownstreamSSE(request) {
		streamCtx, cancelStream := context.WithCancel(ctx)
		streamSession := newDownstreamSSESession(streamCtx, w, request.DownstreamPath, cancelStream)
		defer cancelStream()
		defer streamSession.Close()
		ctx = streamCtx
		r = r.WithContext(ctx)
		w = streamSession
	}

	attempts := append([]routeengine.Candidate{plan.Selected}, plan.Failover...)
	attempts = h.prioritizeCacheSessionAffinity(ctx, attempts)
	waitDeadline := startedAt.Add(upstreamRateLimitMaxWait)
	failoverDeadline := startedAt.Add(gatewayFailoverBudget(request))

	engageSoftFallback := func() bool {
		if softFallbackTarget == "" || routeLockIsStrict(plan.RoutingLock) {
			return false
		}
		target := softFallbackTarget
		softFallbackTarget = ""
		fallbackCtx := withModelMapping(ctx, originalModel, target, store.APIKeyModelRuleModeSoft)
		fallbackRequest := request
		fallbackRequest.RequestedModel = target
		fallbackIntent := codexImageIntentForRequest(fallbackRequest)
		fallbackSetup := h.setupRoute(fallbackCtx, apiKey.ID, endpoint, fallbackRequest, fallbackIntent, bridge != nil)
		if fallbackSetup.failure != nil {
			if h.logger != nil {
				h.logger.InfoContext(ctx, "soft model mapping fallback unavailable", "scope", "gateway", "request_id", requestID, "original_model", originalModel, "fallback_model", target, "reason", fallbackSetup.failure.code)
			}
			return false
		}
		ctx = fallbackCtx
		r = r.WithContext(ctx)
		request = fallbackRequest
		request.EffectiveModelKey = fallbackSetup.plan.CanonicalModel.ModelKey
		imageIntent = fallbackIntent
		access = fallbackSetup.access
		plan = fallbackSetup.plan
		ctx = withRouteLockMetadata(ctx, plan.RoutingLock, false)
		request.EffectiveModelKey = plan.CanonicalModel.ModelKey
		request.APIKeyGatewayConfig = apiKey.Gateway()
		ctx = h.withCacheObservation(ctx, apiKey.ID, plan.CanonicalModel.ID, request)
		ctx = h.withCacheShadowAffinity(ctx, apiKey.ID, plan.CanonicalModel.ID, request, plan, resolver)
		r = r.WithContext(ctx)
		attempts = append([]routeengine.Candidate{plan.Selected}, plan.Failover...)
		attempts = h.prioritizeCacheSessionAffinity(ctx, attempts)
		inflight.SetModel(requestID, plan.CanonicalModel.ModelKey, plan.CanonicalModel.Provider)
		if h.logger != nil {
			h.logger.InfoContext(ctx, "soft model mapping fallback engaged after upstream failures", "scope", "gateway", "request_id", requestID, "original_model", originalModel, "fallback_model", target)
		}
		return true
	}

	var lastFailure *gatewayAttemptResult
	var retainedRateLimitFailure *gatewayAttemptResult
	skippedImageUnsupported := false
	var skippedGrokIncompatible []string
	for {
		lastFailure = nil
		skippedImageUnsupported = false
		skippedGrokIncompatible = nil
		waitableRateLimit := true
		failoverBudgetExceeded := false
	candidateLoop:
		for index, candidate := range attempts {
			ctx = withRouteLockMetadata(ctx, plan.RoutingLock, plan.RoutingLock != nil && !candidate.Locked)
			r = r.WithContext(ctx)
			if errors.Is(context.Cause(ctx), context.Canceled) {
				flowPhase = inflight.PhaseCancelled
				return
			}
			if !time.Now().Before(failoverDeadline) {
				failoverBudgetExceeded = true
				break candidateLoop
			}
			if isGrokSite(candidate.Site.SiteType) {
				if incompatible := grokIncompatibleRequestParams(request); len(incompatible) > 0 {
					skippedGrokIncompatible = appendUniqueStrings(skippedGrokIncompatible, incompatible...)
					continue
				}
			}
			if bridge != nil {
				bridgeProtocol, bridgeResolveErr := resolver.Resolve(ctx, request, candidate)
				if grokProtocol, ok := bridgeProtocol.(*grokResponsesProtocolAdapter); bridgeResolveErr == nil && ok {
					if incompatible := grokProtocol.incompatibleRequestParams(request); len(incompatible) > 0 {
						skippedGrokIncompatible = appendUniqueStrings(skippedGrokIncompatible, incompatible...)
						continue
					}
				}
				if bridgeResolveErr == nil && candidateRequiresImageBridge(candidate, bridgeProtocol) {
					result := h.forwardBridgedResponses(ctx, w, requestID, index+1, apiKey.ID, plan.CanonicalModel.ID, candidate, request, reservation, resolver, bridge)
					if errors.Is(context.Cause(ctx), context.Canceled) {
						flowPhase = inflight.PhaseCancelled
						return
					}
					if result.success {
						actualRateLimitTokens = rateLimitTokenCount(result)
						inflight.AddTokens(requestID, inflightTokenAmount(result))
						h.clearCooldownAfterRecovery(ctx, candidate)
						flowPhase = inflight.PhaseCompleted
						return
					}
					if result.errorType == "downstream_client_cancelled" {
						flowPhase = inflight.PhaseCancelled
						return
					}
					lastFailure = &result
					retainedRateLimitFailure = retainUpstreamRateLimitFailure(retainedRateLimitFailure, result)
					h.cooldownAfterFailure(ctx, candidate, result)
					if result.responseStarted {
						return
					}
					if !upstreamRateLimitWaitable(candidate, result) {
						waitableRateLimit = false
					}
					continue
				}
			}

			attemptRequest, imagePolicyOK := requestForCodexImagePolicy(request, candidate, imageIntent)
			if !imagePolicyOK {
				skippedImageUnsupported = true
				continue
			}
			protocol, resolveErr := resolver.Resolve(ctx, attemptRequest, candidate)
			if resolveErr != nil {
				if routeLockIsStrict(plan.RoutingLock) {
					h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, chatFailure{
						status:         http.StatusServiceUnavailable,
						code:           "route_lock_unavailable",
						message:        "locked upstream route channel is unavailable",
						requestedModel: access.ModelKey,
						stream:         request.Stream,
						stage:          "protocol_resolve",
					})
					return
				}
				h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, chatFailure{
					status:         http.StatusBadGateway,
					code:           "protocol_resolution_failed",
					message:        resolveErr.Error(),
					requestedModel: access.ModelKey,
					stream:         request.Stream,
					stage:          "protocol_resolve",
				})
				return
			}
			if grokProtocol, ok := protocol.(*grokResponsesProtocolAdapter); ok {
				if incompatible := grokProtocol.incompatibleRequestParams(attemptRequest); len(incompatible) > 0 {
					skippedGrokIncompatible = appendUniqueStrings(skippedGrokIncompatible, incompatible...)
					continue
				}
			}

			result := h.forwardGatewayRequest(withCredentialProtocolResolver(ctx, resolver), w, requestID, index+1, apiKey.ID, plan.CanonicalModel.ID, candidate, attemptRequest, reservation, protocol)
			if errors.Is(context.Cause(ctx), context.Canceled) {
				flowPhase = inflight.PhaseCancelled
				return
			}
			if result.success {
				// Settle only the attempt actually served to the client; tokens from
				// failed attempts retried via failover must not be added on top.
				actualRateLimitTokens = rateLimitTokenCount(result)
				inflight.AddTokens(requestID, inflightTokenAmount(result))
				h.clearCooldownAfterRecovery(ctx, candidate)
				if !request.Stream {
					copyUpstreamResponse(w, result, candidate.Site.Name, h.exposeRouteSite)
				}
				flowPhase = inflight.PhaseCompleted
				return
			}
			if result.errorType == "downstream_client_cancelled" {
				flowPhase = inflight.PhaseCancelled
				return
			}
			if bridge != nil && bridgeRescueEligible(result) {
				h.logger.InfoContext(ctx, "image bridge rescue after upstream image rejection", "scope", "gateway", "request_id", requestID, "site", candidate.Site.Slug, "upstream_status", result.upstreamStatusCode)
				result = h.forwardBridgedResponses(ctx, w, requestID, index+1, apiKey.ID, plan.CanonicalModel.ID, candidate, request, reservation, resolver, bridge)
				if errors.Is(context.Cause(ctx), context.Canceled) {
					flowPhase = inflight.PhaseCancelled
					return
				}
				if result.success {
					actualRateLimitTokens = rateLimitTokenCount(result)
					inflight.AddTokens(requestID, inflightTokenAmount(result))
					h.clearCooldownAfterRecovery(ctx, candidate)
					flowPhase = inflight.PhaseCompleted
					return
				}
				if result.errorType == "downstream_client_cancelled" {
					flowPhase = inflight.PhaseCancelled
					return
				}
			}

			lastFailure = &result
			retainedRateLimitFailure = retainUpstreamRateLimitFailure(retainedRateLimitFailure, result)
			h.cooldownAfterFailure(ctx, candidate, result)
			if result.responseStarted {
				return
			}
			if !upstreamRateLimitWaitable(candidate, result) {
				waitableRateLimit = false
			}
		}
		if failoverBudgetExceeded {
			if h.logger != nil {
				h.logger.WarnContext(ctx, "gateway failover budget exhausted", "scope", "gateway", "request_id", requestID, "budget", gatewayFailoverBudget(request), "latency_ms", time.Since(startedAt).Milliseconds())
			}
			break
		}

		if lastFailure == nil || !waitableRateLimit {
			if (lastFailure != nil || skippedImageUnsupported || len(skippedGrokIncompatible) > 0) && engageSoftFallback() {
				continue
			}
			break
		}
		wait := upstreamRateLimitWaitDuration(*lastFailure)
		if time.Now().Add(wait).After(waitDeadline) || time.Now().Add(wait).After(failoverDeadline) {
			if engageSoftFallback() {
				continue
			}
			break
		}
		h.logger.InfoContext(ctx, "waiting for upstream rate limit window", "scope", "gateway", "request_id", requestID, "wait", wait.String(), "retry_after_seconds", lastFailure.retryAfterSeconds)
		if !sleepWithContext(ctx, wait) {
			flowPhase = inflight.PhaseCancelled
			return
		}
	}

	if errors.Is(context.Cause(ctx), context.Canceled) {
		flowPhase = inflight.PhaseCancelled
		return
	}
	if routeLockIsStrict(plan.RoutingLock) && lockedRouteUnavailable(lastFailure, skippedImageUnsupported, len(skippedGrokIncompatible) > 0) {
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, chatFailure{
			status:         http.StatusServiceUnavailable,
			code:           "route_lock_unavailable",
			message:        "locked upstream route channel is unavailable",
			requestedModel: access.ModelKey,
			stream:         request.Stream,
			stage:          "route_attempt",
		})
		return
	}
	lastFailure = preferredFinalGatewayFailure(lastFailure, retainedRateLimitFailure)
	if lastFailure == nil && len(skippedGrokIncompatible) > 0 {
		message := "no available upstream route candidate can preserve parameters: " + strings.Join(skippedGrokIncompatible, ", ")
		if bridge != nil && bridge.FailAll("upstream_parameter_not_supported", message) {
			return
		}
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, chatFailure{
			status:         http.StatusUnprocessableEntity,
			code:           "upstream_parameter_not_supported",
			message:        message,
			requestedModel: access.ModelKey,
			stream:         request.Stream,
			stage:          "route_plan",
		})
		return
	}
	if bridge != nil && bridge.FailAll("upstream_failed", "all upstream route candidates failed") {
		return
	}

	if lastFailure == nil && skippedImageUnsupported {
		h.writeChatFailure(w, r, endpoint.DownstreamPath(), requestID, apiKey.ID, startedAt, chatFailure{
			status:         http.StatusBadRequest,
			code:           "image_generation_not_supported",
			message:        "no available upstream route candidate supports image generation",
			requestedModel: access.ModelKey,
			stream:         request.Stream,
			stage:          "route_plan",
		})
		return
	}
	if lastFailure != nil && upstreamModelUnavailableFailure(*lastFailure) {
		if h.logger != nil {
			h.logger.WarnContext(r.Context(), "all upstream candidates rejected the requested model", "scope", "gateway", "endpoint", endpoint.DownstreamPath(), "request_id", requestID, "status_code", http.StatusBadGateway, "error_code", "upstream_failed", "latency_ms", time.Since(startedAt).Milliseconds())
		}
		h.writeGatewayError(w, r, http.StatusBadGateway, "upstream_failed", "all upstream route candidates failed")
		return
	}
	if lastFailure != nil && (lastFailure.statusCode == http.StatusTooManyRequests || lastFailure.upstreamStatusCode == http.StatusTooManyRequests) {
		if len(lastFailure.body) == 0 {
			lastFailure.contentType = "application/json"
			lastFailure.body = gatewayRequestFailureBody(nonEmptyString(lastFailure.errorType, "rate_limit_exceeded"), nonEmptyString(lastFailure.errorMessage, "rate limit exceeded"), requestID)
		}
		writeUpstreamFailure(w, *lastFailure, requestID)
		return
	}
	if lastFailure != nil && len(lastFailure.body) > 0 && lastFailure.statusCode >= 400 {
		writeUpstreamFailure(w, *lastFailure, requestID)
		return
	}

	h.logger.WarnContext(r.Context(), "gateway request failed without upstream response", "scope", "gateway", "endpoint", endpoint.DownstreamPath(), "request_id", requestID, "status_code", http.StatusBadGateway, "error_code", "upstream_failed", "latency_ms", time.Since(startedAt).Milliseconds())
	h.writeGatewayError(w, r, http.StatusBadGateway, "upstream_failed", "all upstream route candidates failed")
}

func lockedRouteUnavailable(result *gatewayAttemptResult, skippedImage, skippedParameters bool) bool {
	if result == nil {
		return skippedImage || skippedParameters
	}
	if result.responseStarted {
		return false
	}
	if result.upstreamStatusCode == http.StatusTooManyRequests || result.statusCode == http.StatusTooManyRequests {
		return false
	}
	if strings.HasPrefix(result.errorType, "upstream_credential_") {
		return true
	}
	switch result.errorType {
	case "codex_oauth_refresh_failed", "antigravity_oauth_refresh_failed", "claude_code_oauth_refresh_failed", "grok_oauth_refresh_failed",
		"upstream_client_unavailable", "upstream_request_build_failed", "protocol_resolution_failed":
		return true
	case "upstream_transport_error", "upstream_timeout", "upstream_first_byte_timeout", "upstream_response_read_failed", "upstream_response_transform_failed",
		"upstream_stream_preoutput_too_large", "upstream_stream_error", "upstream_stream_failed", "upstream_stream_incomplete", "upstream_stream_eof",
		"upstream_stream_read_failed", "upstream_stream_empty", "upstream_stream_parse_failed":
		return result.upstreamStatusCode == 0 || result.upstreamStatusCode >= http.StatusInternalServerError
	case "upstream_http_error":
		return result.upstreamStatusCode == 0 || result.upstreamStatusCode == http.StatusUnauthorized || result.upstreamStatusCode == http.StatusForbidden || result.upstreamStatusCode == http.StatusNotFound || result.upstreamStatusCode >= http.StatusInternalServerError
	case "upstream_response_failed":
		return result.upstreamStatusCode == 0 || result.upstreamStatusCode >= http.StatusInternalServerError
	default:
		return false
	}
}

func upstreamModelUnavailableFailure(result gatewayAttemptResult) bool {
	if result.upstreamStatusCode == http.StatusNotFound || result.statusCode == http.StatusNotFound {
		return true
	}
	code := strings.ToLower(strings.TrimSpace(result.upstreamErrorCode))
	message := strings.ToLower(strings.TrimSpace(result.errorMessage))
	return strings.Contains(code, "model_not_found") || strings.Contains(code, "model-not-found") ||
		(strings.Contains(message, "model") && strings.Contains(message, "not found"))
}

func retainUpstreamRateLimitFailure(retained *gatewayAttemptResult, result gatewayAttemptResult) *gatewayAttemptResult {
	if result.upstreamStatusCode != http.StatusTooManyRequests && result.statusCode != http.StatusTooManyRequests {
		return retained
	}
	copied := result
	return &copied
}

func preferredFinalGatewayFailure(lastFailure *gatewayAttemptResult, retainedRateLimitFailure *gatewayAttemptResult) *gatewayAttemptResult {
	if retainedRateLimitFailure != nil && (lastFailure == nil || lastFailure.errorType == "upstream_credential_unavailable") {
		return retainedRateLimitFailure
	}
	return lastFailure
}

func (h Handler) resolveModelMapping(apiKey store.APIKey, requestedModel string) (store.APIKeyModelRule, bool) {
	rule, ok := matchModelRule(apiKey.ModelRules(), requestedModel)
	if !ok {
		return store.APIKeyModelRule{}, false
	}
	if catalog.NormalizeModelKey(rule.Target) == catalog.NormalizeModelKey(requestedModel) {
		return store.APIKeyModelRule{}, false
	}
	return rule, true
}

type routeSetup struct {
	access      auth.APIKeyRouteAccess
	plan        routeengine.SelectionPlan
	failure     *chatFailure
	noRoute     *routeAccessForNoRoute
	routingLock *store.RouteLock
}

func routeLockIsStrict(lock *store.RouteLock) bool {
	return lock != nil && store.NormalizeRouteLockPolicy(lock.Policy) == store.RouteLockPolicyStrict
}

func routeSetupHasStrictLock(setup routeSetup) bool {
	if routeLockIsStrict(setup.plan.RoutingLock) {
		return true
	}
	if routeLockIsStrict(setup.routingLock) {
		return true
	}
	return setup.failure != nil && setup.failure.code == "route_lock_unavailable"
}

func (h Handler) setupRoute(
	ctx context.Context,
	apiKeyID uuid.UUID,
	endpoint gatewayEndpointAdapter,
	request gatewayRequest,
	imageIntent codexImageGenerationIntent,
	bridged bool,
) routeSetup {
	access, err := h.auth.ResolveAPIKeyRouteAccess(ctx, apiKeyID, request.RequestedModel)
	if err != nil {
		if errors.Is(err, auth.ErrModelNotAllowed) {
			return routeSetup{failure: &chatFailure{
				status:         http.StatusForbidden,
				code:           "model_not_allowed",
				message:        "api key is not allowed to access this model",
				requestedModel: request.RequestedModel,
				stream:         request.Stream,
				stage:          "authz",
			}}
		}
		if errors.Is(err, auth.ErrSiteNotAllowed) {
			return routeSetup{failure: &chatFailure{
				status:         http.StatusForbidden,
				code:           "site_not_allowed",
				message:        "api key is not allowed to access any site",
				requestedModel: request.RequestedModel,
				stream:         request.Stream,
				stage:          "authz",
			}}
		}
		return routeSetup{failure: &chatFailure{
			status:         http.StatusBadRequest,
			code:           "invalid_model",
			message:        err.Error(),
			requestedModel: request.RequestedModel,
			stream:         request.Stream,
			stage:          "authz",
		}}
	}

	endpointType := endpoint.RouteEndpointType()
	if request.Canonical != nil && request.Canonical.Image != nil {
		endpointType = "gemini-image"
	}
	plan, err := h.router.Plan(ctx, routeengine.CandidateQuery{
		ModelKey:            access.ModelKey,
		EndpointType:        endpointType,
		ImageGeneration:     imageIntent == codexImageIntentExplicit && !bridged,
		AllowedSiteIDs:      access.AllowedSiteIDs,
		AllowedSiteModelIDs: access.AllowedSiteModelIDs,
		FailoverLimit:       3,
	})
	if err != nil {
		if errors.Is(err, routeengine.ErrRouteLockUnavailable) {
			var lockUnavailable *routeengine.RouteLockUnavailableError
			errors.As(err, &lockUnavailable)
			return routeSetup{failure: &chatFailure{
				status:         http.StatusServiceUnavailable,
				code:           "route_lock_unavailable",
				message:        "locked upstream route channel is unavailable",
				requestedModel: access.ModelKey,
				stream:         request.Stream,
				stage:          "route_plan",
			}, routingLock: routeLockFromUnavailableError(lockUnavailable)}
		}
		if errors.Is(err, routeengine.ErrNoRouteCandidates) {
			if imageIntent == codexImageIntentExplicit {
				return routeSetup{failure: &chatFailure{
					status:         http.StatusBadRequest,
					code:           "image_generation_not_supported",
					message:        "no available upstream route candidate supports image generation",
					requestedModel: access.ModelKey,
					stream:         request.Stream,
					stage:          "route_plan",
				}}
			}
			return routeSetup{
				failure: &chatFailure{
					status:         http.StatusServiceUnavailable,
					code:           "no_route_candidates",
					message:        "no available upstream route candidates",
					requestedModel: access.ModelKey,
					stream:         request.Stream,
					stage:          "route_plan",
				},
				noRoute: &routeAccessForNoRoute{
					ModelKey:            access.ModelKey,
					AllowedSiteIDs:      access.AllowedSiteIDs,
					AllowedSiteModelIDs: access.AllowedSiteModelIDs,
				},
			}
		}
		return routeSetup{failure: &chatFailure{
			status:         http.StatusBadGateway,
			code:           "route_selection_failed",
			message:        err.Error(),
			requestedModel: access.ModelKey,
			stream:         request.Stream,
			stage:          "route_plan",
		}}
	}

	return routeSetup{access: access, plan: plan}
}

func routeLockFromUnavailableError(err *routeengine.RouteLockUnavailableError) *store.RouteLock {
	if err == nil || err.Lock == nil {
		return nil
	}
	lock := *err.Lock
	return &lock
}
