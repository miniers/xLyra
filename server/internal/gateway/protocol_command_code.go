package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	routeengine "xlyra/server/internal/router"
)

func isCommandCodeSite(siteType string) bool {
	return strings.EqualFold(strings.TrimSpace(siteType), "command_code")
}

var ccToolNameAliases = map[string]string{
	"bash_output":         "shell_output",
	"task_output":         "shell_output",
	"tool_search":         "search_tools",
	"read_multiple_files": "read_file",
}

func mapCCToolName(name string) string {
	if aliased, ok := ccToolNameAliases[name]; ok {
		return aliased
	}
	return name
}

var ccReverseToolAliases = map[string]string{
	"shell_output": "bash_output",
	"search_tools": "tool_search",
	"read_file":    "read_multiple_files",
}

func reverseMapCCToolName(name string) string {
	if original, ok := ccReverseToolAliases[name]; ok {
		return original
	}
	return name
}

func ccGenerateTraceparent() string {
	traceID := make([]byte, 16)
	spanID := make([]byte, 8)
	_, _ = rand.Read(traceID)
	_, _ = rand.Read(spanID)
	return fmt.Sprintf("00-%x-%x-01", traceID, spanID)
}

func ccFakeThinkingSignature(thinkingText string) string {
	if thinkingText == "" {
		thinkingText = "dsh-proxy-thinking"
	}
	digest := sha256.Sum256([]byte(thinkingText))
	seed := digest[:]
	raw := make([]byte, 2+len(seed))
	raw[0] = 0x12
	raw[1] = byte(len(seed))
	copy(raw[2:], seed)
	return base64.StdEncoding.EncodeToString(raw)
}

type commandCodeProtocolAdapter struct {
	request            gatewayRequest
	candidate          routeengine.Candidate
	downstreamProtocol canonicalProtocol
	sessionID          string
}

func newCommandCodeProtocolAdapter(request gatewayRequest, candidate routeengine.Candidate) *commandCodeProtocolAdapter {
	downstream := downstreamCanonicalProtocol(request.DownstreamPath)
	return &commandCodeProtocolAdapter{
		request:            request,
		candidate:          candidate,
		downstreamProtocol: downstream,
	}
}

func (a *commandCodeProtocolAdapter) ProtocolName() string {
	switch a.downstreamProtocol {
	case canonicalProtocolOpenAIChat:
		return "command_code_to_chat_completions"
	case canonicalProtocolOpenAIResponses:
		return "command_code_to_responses"
	case canonicalProtocolAnthropicMessages:
		return "command_code_to_anthropic_messages"
	}
	return "command_code"
}

func (a *commandCodeProtocolAdapter) usesRawBufferedResponse() bool {
	return true
}

func (a *commandCodeProtocolAdapter) UpstreamPath(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.commandcode.ai"
	}
	return base + "/alpha/generate"
}

func (a *commandCodeProtocolAdapter) BuildUpstreamPayload(request gatewayRequest, candidate routeengine.Candidate) (map[string]any, error) {
	workingDir := "C:\\Users\\dev\\projects\\app"

	cfg := map[string]any{
		"workingDir":    workingDir,
		"date":          time.Now().Format("2006-01-02"),
		"environment":   ccDevicePlatform,
		"structure":     []any{},
		"isGitRepo":     false,
		"currentBranch": "",
		"mainBranch":    "",
		"gitStatus":     "",
		"recentCommits": []any{},
	}

	modelName := strings.TrimSpace(candidate.Model.UpstreamName)
	if modelName == "" {
		modelName = strings.TrimSpace(request.RequestedModel)
	}
	if modelName == "" {
		modelName = "deepseek/deepseek-v4-flash"
	}

	maxTokens := 64000
	for _, name := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if mt, ok := intFromAny(request.Payload[name]); ok && mt > 0 {
			maxTokens = mt
			break
		}
	}
	if maxTokens > 200000 {
		maxTokens = 200000
	}

	// 1. Build messages & extract system
	systemBlocks := []map[string]any{}
	ccMessages := []map[string]any{}

	if request.Canonical != nil {
		toolNames := make(map[string]string)
		for _, msg := range request.Canonical.Messages {
			for _, call := range msg.ToolCalls {
				toolNames[call.ID] = call.Name
			}
		}
		if strings.TrimSpace(request.Canonical.Instructions) != "" {
			systemBlocks = append(systemBlocks, map[string]any{
				"type": "text",
				"text": strings.TrimSpace(request.Canonical.Instructions),
			})
		}

		for _, msg := range request.Canonical.Messages {
			if strings.EqualFold(msg.Role, "system") {
				for _, part := range msg.Content {
					if strings.TrimSpace(part.Text) != "" {
						systemBlocks = append(systemBlocks, map[string]any{
							"type": "text",
							"text": part.Text,
						})
					}
				}
				continue
			}

			if strings.EqualFold(msg.Role, "user") {
				parts := []map[string]any{}
				for _, part := range msg.Content {
					if part.Type == "image" || part.ImageURL != "" {
						mediaType := nonEmptyString(part.MimeType, "image/jpeg")
						imageRef := strings.TrimSpace(part.ImageURL)
						if imageRef == "" {
							imageRef = strings.TrimSpace(part.FileData)
						}
						if strings.HasPrefix(imageRef, "data:") {
							chunks := strings.SplitN(strings.TrimPrefix(imageRef, "data:"), ";base64,", 2)
							if len(chunks) == 2 {
								if strings.TrimSpace(chunks[0]) != "" {
									mediaType = strings.TrimSpace(chunks[0])
								}
							}
						} else if imageRef != "" && !strings.HasPrefix(imageRef, "http://") && !strings.HasPrefix(imageRef, "https://") {
							imageRef = "data:" + mediaType + ";base64," + imageRef
						}
						parts = append(parts, map[string]any{
							"type": "image", "image": imageRef, "mimeType": mediaType,
						})
					} else {
						parts = append(parts, map[string]any{
							"type": "text",
							"text": part.Text,
						})
					}
				}
				if len(parts) == 0 {
					parts = append(parts, map[string]any{"type": "text", "text": ""})
				}
				ccMessages = append(ccMessages, map[string]any{
					"role":    "user",
					"content": parts,
				})
			} else if strings.EqualFold(msg.Role, "assistant") {
				parts := []map[string]any{}
				for _, thinking := range msg.Thinking {
					if thinking.Thinking != "" {
						parts = append(parts, map[string]any{"type": "reasoning", "text": thinking.Thinking})
					}
				}
				for _, part := range msg.Content {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": part.Text,
					})
				}
				for _, tc := range msg.ToolCalls {
					var input any
					if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
						input = map[string]any{}
					}
					parts = append(parts, map[string]any{
						"type":       "tool-call",
						"toolCallId": tc.ID,
						"toolName":   mapCCToolName(tc.Name),
						"input":      input,
					})
				}
				ccMessages = append(ccMessages, map[string]any{
					"role":    "assistant",
					"content": parts,
				})
			} else if strings.EqualFold(msg.Role, "tool") {
				val := ""
				switch v := msg.Output.(type) {
				case string:
					val = v
				default:
					b, _ := json.Marshal(v)
					val = string(b)
				}
				ccMessages = append(ccMessages, map[string]any{
					"role": "tool",
					"content": []map[string]any{
						{
							"type":       "tool-result",
							"toolCallId": msg.ToolCallID,
							"toolName":   mapCCToolName(nonEmptyString(toolNames[msg.ToolCallID], msg.Name)),
							"output": map[string]any{
								"type":  "text",
								"value": val,
							},
						},
					},
				})
			}
		}
	}

	params := map[string]any{
		"model":      modelName,
		"messages":   ccMessages,
		"max_tokens": maxTokens,
		"stream":     true,
	}

	if len(systemBlocks) > 0 {
		params["system"] = systemBlocks
	} else {
		// Empty system placeholder to suppress upstream 7.5K token prompt injection
		params["system"] = []map[string]any{
			{"type": "text", "text": " "},
		}
	}

	if temp, ok := request.Payload["temperature"]; ok && temp != nil {
		params["temperature"] = temp
	}
	if re, ok := request.Payload["reasoning_effort"]; ok && re != nil {
		params["reasoning_effort"] = re
	}

	if request.Canonical != nil && len(request.Canonical.Tools) > 0 {
		wireTools := make([]map[string]any, 0, len(request.Canonical.Tools))
		for _, t := range request.Canonical.Tools {
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			wireTools = append(wireTools, map[string]any{
				"name":         mapCCToolName(t.Name),
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		params["tools"] = wireTools
	}

	if choice, ok := request.Payload["tool_choice"]; ok && choice != nil {
		if s, ok := choice.(string); ok {
			choiceMap := map[string]string{"auto": "auto", "none": "none", "required": "any"}
			if mapped, found := choiceMap[s]; found {
				params["tool_choice"] = map[string]any{"type": mapped}
			} else {
				params["tool_choice"] = map[string]any{"type": s}
			}
		} else if m, ok := choice.(map[string]any); ok {
			if fn, ok := m["function"].(map[string]any); ok {
				params["tool_choice"] = map[string]any{"type": "tool", "name": fn["name"]}
			} else {
				params["tool_choice"] = m
			}
		}
	}

	body := map[string]any{
		"config":         cfg,
		"memory":         "",
		"taste":          nil,
		"skills":         nil,
		"permissionMode": "standard",
		"mode":           "agent",
		"params":         params,
	}

	// Session / threadId
	sessID := a.resolveSessionID(request, "")
	if _, err := uuid.Parse(sessID); err == nil {
		body["threadId"] = sessID
	}

	return body, nil
}

func (a *commandCodeProtocolAdapter) resolveSessionID(request gatewayRequest, key string) string {
	if a.sessionID != "" {
		return a.sessionID
	}
	candidates := []string{
		request.DownstreamHeaders.Get("x-session-id"),
		request.DownstreamHeaders.Get("x-claude-code-session-id"),
		request.DownstreamHeaders.Get("session_id"),
	}
	for _, id := range candidates {
		id = strings.TrimSpace(id)
		if len(id) >= 8 {
			a.sessionID = id
			return id
		}
	}
	if strings.TrimSpace(key) != "" {
		a.sessionID = ccSessionForKey(key, time.Now())
	} else {
		a.sessionID = uuid.NewString()
	}
	return a.sessionID
}

func (a *commandCodeProtocolAdapter) BuildUpstreamBody(_ gatewayRequest, payload map[string]any) ([]byte, string, error) {
	b, err := json.Marshal(payload)
	return b, "application/json", err
}

func (a *commandCodeProtocolAdapter) ApplyUpstreamHeaders(req *http.Request, upstreamKey string, _ string, _ bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cli")
	req.Header.Set("x-command-code-version", ccProtocolVersion)
	req.Header.Set("x-cli-environment", "production")
	req.Header.Set("x-project-slug", "c-users-dev-projects-app")
	req.Header.Set("x-taste-learning", "false")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(upstreamKey))
	req.Header.Set("traceparent", ccGenerateTraceparent())

	sessID := a.resolveSessionID(a.request, upstreamKey)
	req.Header.Set("x-session-id", sessID)

	ensureCommandCodeInitialized(req.Context(), &http.Client{Timeout: 10 * time.Second}, req.URL.Scheme+"://"+req.URL.Host, upstreamKey, "")
}

func (a *commandCodeProtocolAdapter) ProxyStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, startedAt time.Time, candidate routeengine.Candidate) (streamCaptureState, bool, error) {
	options := canonicalStreamOptions{
		IncludeUsage: chatStreamUsageEnabled(a.request.Payload),
		Candidate:    candidate,
	}
	return proxyCanonicalStream(ctx, w, resp, startedAt, canonicalProtocolCommandCode, a.downstreamProtocol, options)
}

func (a *commandCodeProtocolAdapter) TransformBufferedResponse(statusCode int, headers http.Header, body []byte) (gatewayBufferedResponse, error) {
	contentType := strings.TrimSpace(headers.Get("Content-Type"))
	if statusCode < 200 || statusCode >= 300 {
		return gatewayBufferedResponse{
			StatusCode:  statusCode,
			ContentType: contentType,
			Body:        body,
		}, nil
	}

	decoder := newCommandCodeStreamDecoder()
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var events []canonicalStreamEvent
	for scanner.Scan() {
		evs, err := decoder.DecodeLine(scanner.Bytes())
		if err != nil {
			continue
		}
		events = append(events, evs...)
	}
	if err := scanner.Err(); err != nil {
		return gatewayBufferedResponse{}, fmt.Errorf("command code stream scan: %w", err)
	}
	events = append(events, decoder.Flush()...)

	var fullText strings.Builder
	var fullReasoning strings.Builder
	var toolCalls []map[string]any
	var usage gatewayUsage
	var finishReason = "stop"
	var responseID = decoder.responseID

	for _, ev := range events {
		switch ev.Type {
		case canonicalStreamEventTextDelta:
			fullText.WriteString(ev.Delta)
		case canonicalStreamEventReasoningDelta:
			fullReasoning.WriteString(ev.Delta)
		case canonicalStreamEventToolCallDelta:
			var args any
			_ = json.Unmarshal([]byte(ev.ToolCallArgumentsDelta), &args)
			toolCalls = append(toolCalls, map[string]any{
				"id":   ev.ToolCallID,
				"type": "function",
				"function": map[string]any{
					"name":      ev.ToolCallName,
					"arguments": ev.ToolCallArgumentsDelta,
				},
			})
		case canonicalStreamEventUsage:
			usage = ev.Usage
		case canonicalStreamEventError:
			return gatewayBufferedResponse{}, fmt.Errorf("command code upstream: %s", ev.ErrorMessage)
		case canonicalStreamEventCompleted, canonicalStreamEventIncomplete:
			if ev.FinishReason != "" {
				finishReason = ev.FinishReason
			}
		}
	}

	resolvedModel := strings.TrimSpace(decoder.model)
	if resolvedModel == "" {
		resolvedModel = strings.TrimSpace(a.candidate.Model.UpstreamName)
	}
	if resolvedModel == "" {
		resolvedModel = strings.TrimSpace(a.request.RequestedModel)
	}

	var outputJSON []byte
	var err error

	switch a.downstreamProtocol {
	case canonicalProtocolAnthropicMessages:
		content := []map[string]any{}
		if fullReasoning.Len() > 0 {
			sig := ccFakeThinkingSignature(fullReasoning.String())
			content = append(content, map[string]any{
				"type":      "thinking",
				"thinking":  fullReasoning.String(),
				"signature": sig,
			})
		}
		if fullText.Len() > 0 {
			content = append(content, map[string]any{
				"type": "text",
				"text": fullText.String(),
			})
		}
		for _, tc := range toolCalls {
			fn, _ := tc["function"].(map[string]any)
			var input any = map[string]any{}
			if argsStr, ok := fn["arguments"].(string); ok {
				_ = json.Unmarshal([]byte(argsStr), &input)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    tc["id"],
				"name":  fn["name"],
				"input": input,
			})
		}
		if len(content) == 0 {
			content = append(content, map[string]any{
				"type": "text",
				"text": "",
			})
		}
		stopReason := "end_turn"
		if finishReason == "length" {
			stopReason = "max_tokens"
		} else if finishReason == "pause_turn" || finishReason == "refusal" {
			stopReason = finishReason
		} else if len(toolCalls) > 0 {
			stopReason = "tool_use"
		}
		respMap := map[string]any{
			"id":          responseID,
			"type":        "message",
			"role":        "assistant",
			"content":     content,
			"model":       resolvedModel,
			"stop_reason": stopReason,
			"usage": map[string]any{
				"input_tokens":  usage.PromptTokens,
				"output_tokens": usage.CompletionTokens,
			},
		}
		outputJSON, err = json.Marshal(respMap)

	case canonicalProtocolOpenAIResponses:
		outputItems := []map[string]any{}
		if fullReasoning.Len() > 0 {
			outputItems = append(outputItems, map[string]any{
				"id": "rs_" + uuid.NewString(), "type": "reasoning",
				"summary": []map[string]any{{"type": "summary_text", "text": fullReasoning.String()}},
			})
		}
		if fullText.Len() > 0 {
			outputItems = append(outputItems, map[string]any{
				"id": "msg_" + uuid.NewString(), "status": "completed",
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": fullText.String(), "annotations": []any{}},
				},
			})
		}
		for _, tc := range toolCalls {
			fn := tc["function"].(map[string]any)
			outputItems = append(outputItems, map[string]any{
				"id": "fc_" + uuid.NewString(), "type": "function_call", "status": "completed",
				"call_id": tc["id"], "name": fn["name"], "arguments": fn["arguments"],
			})
		}
		respMap := map[string]any{
			"id":                 responseID,
			"object":             "response",
			"status":             "completed",
			"model":              resolvedModel,
			"output":             outputItems,
			"created_at":         decoder.createdAt,
			"incomplete_details": nil,
			"usage": map[string]any{
				"total_tokens":    usage.TotalTokens,
				"input_tokens":    usage.PromptTokens,
				"output_tokens":   usage.CompletionTokens,
				"reasoning_token": usage.ReasoningTokens,
			},
		}
		if finishReason == "length" || finishReason == "pause_turn" {
			reason := finishReason
			if reason == "length" {
				reason = "max_output_tokens"
			}
			respMap["status"] = "incomplete"
			respMap["incomplete_details"] = map[string]any{"reason": reason}
		}
		outputJSON, err = json.Marshal(respMap)

	default: // OpenAI Chat Completions
		if finishReason == "pause_turn" {
			finishReason = "length"
		}
		msg := map[string]any{
			"role":    "assistant",
			"content": fullText.String(),
		}
		if fullReasoning.Len() > 0 {
			msg["reasoning_content"] = fullReasoning.String()
		}
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
			if finishReason == "stop" {
				finishReason = "tool_calls"
			}
		}
		respMap := map[string]any{
			"id":      responseID,
			"object":  "chat.completion",
			"created": decoder.createdAt,
			"model":   resolvedModel,
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       msg,
					"finish_reason": finishReason,
				},
			},
			"usage": chatCompletionUsagePayload(usage),
		}
		outputJSON, err = json.Marshal(respMap)
	}

	if err != nil {
		return gatewayBufferedResponse{}, err
	}

	return gatewayBufferedResponse{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        outputJSON,
		Usage:       usage,
	}, nil
}

// commandCodeStreamDecoder translates upstream Command Code SSE/NDJSON lines into canonicalStreamEvent
type commandCodeStreamDecoder struct {
	responseID    string
	model         string
	createdAt     int64
	createdSent   bool
	toolCallIndex int
	sawFinish     bool
	sawError      bool
	pendingFinish *canonicalStreamEvent
}

func newCommandCodeStreamDecoder() *commandCodeStreamDecoder {
	return &commandCodeStreamDecoder{
		responseID: "chatcmpl-" + uuid.NewString(),
		createdAt:  time.Now().Unix(),
	}
}

func (d *commandCodeStreamDecoder) DecodeLine(line []byte) ([]canonicalStreamEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "data:") {
		trimmed = strings.TrimSpace(trimmed[5:])
	}
	if trimmed == "[DONE]" {
		return d.Flush(), nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, nil
	}

	if m, ok := raw["model"]; ok {
		var modelStr string
		if err := json.Unmarshal(m, &modelStr); err == nil && strings.TrimSpace(modelStr) != "" {
			d.model = strings.TrimSpace(modelStr)
		}
	}

	var eventType string
	if t, ok := raw["type"]; ok {
		_ = json.Unmarshal(t, &eventType)
	}
	if eventType == "" {
		return nil, nil
	}

	var events []canonicalStreamEvent
	ensureCreated := func() {
		if !d.createdSent {
			d.createdSent = true
			events = append(events, canonicalStreamEvent{
				Type:      canonicalStreamEventCreated,
				ID:        d.responseID,
				Model:     d.model,
				CreatedAt: d.createdAt,
			})
		}
	}

	switch eventType {
	case "text-start", "start", "start-step":
		ensureCreated()

	case "text-delta":
		ensureCreated()
		var text string
		if t, ok := raw["text"]; ok {
			_ = json.Unmarshal(t, &text)
		} else if d, ok := raw["delta"]; ok {
			_ = json.Unmarshal(d, &text)
		}
		if text != "" {
			events = append(events, canonicalStreamEvent{
				Type:      canonicalStreamEventTextDelta,
				ID:        d.responseID,
				Model:     d.model,
				CreatedAt: d.createdAt,
				Delta:     text,
			})
		}

	case "reasoning-delta":
		ensureCreated()
		var text string
		if t, ok := raw["text"]; ok {
			_ = json.Unmarshal(t, &text)
		}
		if text != "" {
			events = append(events, canonicalStreamEvent{
				Type:          canonicalStreamEventReasoningDelta,
				ID:            d.responseID,
				Model:         d.model,
				CreatedAt:     d.createdAt,
				Delta:         text,
				ReasoningKind: "thinking",
			})
		}

	case "tool-call":
		ensureCreated()
		var toolCallID, toolName string
		if tc, ok := raw["toolCallId"]; ok {
			_ = json.Unmarshal(tc, &toolCallID)
		}
		if tn, ok := raw["toolName"]; ok {
			_ = json.Unmarshal(tn, &toolName)
		}
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("call_%d_%d", d.createdAt, d.toolCallIndex)
		}
		args := "{}"
		if inputRaw, ok := raw["input"]; ok {
			var strVal string
			if err := json.Unmarshal(inputRaw, &strVal); err == nil {
				args = strVal
			} else {
				args = string(inputRaw)
			}
		}
		events = append(events, canonicalStreamEvent{
			Type:                   canonicalStreamEventToolCallDelta,
			ID:                     d.responseID,
			Model:                  d.model,
			CreatedAt:              d.createdAt,
			ToolCallID:             toolCallID,
			ToolCallName:           reverseMapCCToolName(toolName),
			ToolCallArgumentsDelta: args,
			ToolCallIndex:          d.toolCallIndex,
		})
		d.toolCallIndex++

	case "finish-step", "finish":
		d.sawFinish = true
		var finishReason string
		if fr, ok := raw["finishReason"]; ok {
			_ = json.Unmarshal(fr, &finishReason)
		}
		finishReason = commandCodeFinishReason(finishReason)
		if finishReason == "upstream_error" {
			d.sawError = true
			return []canonicalStreamEvent{{Type: canonicalStreamEventError, ErrorMessage: "provider reported an upstream connection failure"}}, nil
		}

		usageData := raw["totalUsage"]
		if len(usageData) == 0 {
			usageData = raw["usage"]
		}
		if len(usageData) > 0 {
			var u struct {
				InputTokens       int `json:"inputTokens"`
				OutputTokens      int `json:"outputTokens"`
				CachedInputTokens int `json:"cachedInputTokens"`
			}
			if err := json.Unmarshal(usageData, &u); err == nil {
				usage := gatewayUsage{
					PromptTokens:         u.InputTokens,
					CompletionTokens:     u.OutputTokens,
					TotalTokens:          u.InputTokens + u.OutputTokens,
					CachedPromptTokens:   u.CachedInputTokens,
					PromptCacheHitTokens: u.CachedInputTokens,
					PromptTokensDetail: &promptTokensDetails{
						CachedTokens: u.CachedInputTokens,
					},
				}
				events = append(events, canonicalStreamEvent{
					Type:  canonicalStreamEventUsage,
					Usage: usage,
				})
			}
		}

		eventKind := canonicalStreamEventCompleted
		if finishReason == "length" || finishReason == "pause_turn" {
			eventKind = canonicalStreamEventIncomplete
		}
		d.pendingFinish = &canonicalStreamEvent{
			Type:         eventKind,
			FinishReason: finishReason,
		}

	case "error":
		d.sawError = true
		errMsg := "unknown error"
		if errData, ok := raw["error"]; ok {
			var e struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			}
			if err := json.Unmarshal(errData, &e); err == nil && e.Message != "" {
				errMsg = e.Message
			}
		}
		events = append(events, canonicalStreamEvent{
			Type:         canonicalStreamEventError,
			ErrorMessage: errMsg,
		})
	}

	return events, nil
}

func (d *commandCodeStreamDecoder) Flush() []canonicalStreamEvent {
	if d.sawError {
		return nil
	}
	if d.pendingFinish != nil {
		event := *d.pendingFinish
		d.pendingFinish = nil
		return []canonicalStreamEvent{event}
	}
	if !d.sawFinish && !d.sawError {
		return []canonicalStreamEvent{{Type: canonicalStreamEventError, ErrorMessage: "stream ended without a finish event"}}
	}
	return nil
}

func commandCodeFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "tool-calls", "tool_calls", "tool_use":
		return "tool_calls"
	case "length", "max_tokens", "max_output_tokens", "model_context_window_exceeded":
		return "length"
	case "network-error", "network_error", "network error", "networkerror",
		"connection-error", "connection_error", "connection error", "connectionerror",
		"upstream-error", "upstream_error", "upstream error", "upstreamerror":
		return "upstream_error"
	case "":
		return "stop"
	default:
		return strings.ToLower(strings.TrimSpace(reason))
	}
}
