package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	routeengine "xlyra/server/internal/router"
)

func TestCommandCodeBuildUpstreamPayloadEnvelopeAndEmptySystem(t *testing.T) {
	adapter := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
		RequestedModel: "claude-sonnet-4-6",
		Payload: map[string]any{
			"model": "claude-sonnet-4-6",
		},
		Canonical: &canonicalRequest{
			DownstreamPath: "/v1/chat/completions",
			RequestedModel: "claude-sonnet-4-6",
			Messages: []canonicalMessage{
				{
					Role: "user",
					Content: []canonicalContentPart{
						{Type: "text", Text: "Hello Command Code"},
					},
				},
			},
		},
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "claude-sonnet-4-6",
		},
	})

	payload, err := adapter.BuildUpstreamPayload(adapter.request, adapter.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}

	if payload["mode"] != "agent" {
		t.Fatalf("expected mode agent, got %v", payload["mode"])
	}
	if payload["permissionMode"] != "standard" {
		t.Fatalf("expected permissionMode standard, got %v", payload["permissionMode"])
	}

	cfg, ok := payload["config"].(map[string]any)
	if !ok || cfg["environment"] != "win32" {
		t.Fatalf("expected config.environment win32, got %v", cfg)
	}

	params, ok := payload["params"].(map[string]any)
	if !ok {
		t.Fatalf("expected params map, got %v", payload["params"])
	}

	if params["model"] != "claude-sonnet-4-6" {
		t.Fatalf("expected model claude-sonnet-4-6, got %v", params["model"])
	}

	// Verify empty system placeholder was injected
	systemBlocks, ok := params["system"].([]map[string]any)
	if !ok || len(systemBlocks) != 1 || systemBlocks[0]["text"] != " " {
		t.Fatalf("expected empty system placeholder [{'type':'text','text':' '}], got %v", params["system"])
	}
}

func TestCommandCodeToolMappingAndSystemSuppression(t *testing.T) {
	adapter := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
		RequestedModel: "claude-sonnet-4-6",
		Payload:        map[string]any{},
		Canonical: &canonicalRequest{
			Instructions: "Custom system instructions",
			Tools: []canonicalTool{
				{
					Name:        "bash_output",
					Description: "Run bash command",
					Parameters:  map[string]any{"type": "object"},
				},
				{
					Name:        "read_multiple_files",
					Description: "Read files",
					Parameters:  map[string]any{"type": "object"},
				},
			},
			Messages: []canonicalMessage{
				{
					Role: "assistant",
					ToolCalls: []canonicalToolCall{
						{
							ID:        "call_123",
							Name:      "bash_output",
							Arguments: `{"cmd":"ls"}`,
						},
					},
				},
				{
					Role:       "tool",
					Name:       "bash_output",
					ToolCallID: "call_123",
					Output:     "file1.txt\nfile2.txt",
				},
			},
		},
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "claude-sonnet-4-6",
		},
	})

	payload, err := adapter.BuildUpstreamPayload(adapter.request, adapter.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}

	params := payload["params"].(map[string]any)

	// Verify instructions preserved
	systemBlocks := params["system"].([]map[string]any)
	if len(systemBlocks) != 1 || systemBlocks[0]["text"] != "Custom system instructions" {
		t.Fatalf("expected custom system instruction, got %v", systemBlocks)
	}

	// Verify tool names mapped
	tools := params["tools"].([]map[string]any)
	if len(tools) != 2 || tools[0]["name"] != "shell_output" || tools[1]["name"] != "read_file" {
		t.Fatalf("expected tools mapped to shell_output and read_file, got %v", tools)
	}

	// Verify messages tool call and tool result names mapped
	msgs := params["messages"].([]map[string]any)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	asstContent := msgs[0]["content"].([]map[string]any)
	if asstContent[0]["toolName"] != "shell_output" {
		t.Fatalf("expected assistant tool-call name shell_output, got %v", asstContent[0])
	}
	toolContent := msgs[1]["content"].([]map[string]any)
	if toolContent[0]["toolName"] != "shell_output" {
		t.Fatalf("expected tool tool-result name shell_output, got %v", toolContent[0])
	}
}

func TestCommandCodeStreamDecoderAndBufferedTransform(t *testing.T) {
	decoder := newCommandCodeStreamDecoder()

	lines := [][]byte{
		[]byte(`{"type":"text-start"}`),
		[]byte(`{"type":"reasoning-delta","text":"thinking about life"}`),
		[]byte(`{"type":"text-delta","text":"Hello, "}`),
		[]byte(`{"type":"text-delta","text":"world!"}`),
		[]byte(`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":15,"outputTokens":8,"cachedInputTokens":10}}`),
		[]byte(`[DONE]`),
	}

	var events []canonicalStreamEvent
	for _, l := range lines {
		evs, err := decoder.DecodeLine(l)
		if err != nil {
			t.Fatalf("DecodeLine error: %v", err)
		}
		events = append(events, evs...)
	}

	hasReasoning := false
	hasText := false
	hasUsage := false
	for _, ev := range events {
		if ev.Type == canonicalStreamEventReasoningDelta && ev.Delta == "thinking about life" {
			hasReasoning = true
		}
		if ev.Type == canonicalStreamEventTextDelta && ev.Delta == "Hello, " {
			hasText = true
		}
		if ev.Type == canonicalStreamEventUsage && ev.Usage.PromptTokens == 15 && ev.Usage.CachedPromptTokens == 10 {
			hasUsage = true
		}
	}
	if !hasReasoning {
		t.Fatal("expected reasoning delta")
	}
	if !hasText {
		t.Fatal("expected text delta")
	}
	if !hasUsage {
		t.Fatal("expected usage event with cached tokens")
	}

	// Test TransformBufferedResponse for OpenAI Chat
	adapterChat := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
	}, routeengine.Candidate{})

	streamBody := []byte("data: {\"type\":\"reasoning-delta\",\"text\":\"deep reasoning\"}\n\ndata: {\"type\":\"text-delta\",\"text\":\"The answer is 42.\"}\n\ndata: {\"type\":\"finish\",\"finishReason\":\"stop\",\"totalUsage\":{\"inputTokens\":20,\"outputTokens\":10,\"cachedInputTokens\":15}}\n\ndata: [DONE]\n\n")

	resp, err := adapterChat.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBody)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}

	var chatResp map[string]any
	if err := json.Unmarshal(resp.Body, &chatResp); err != nil {
		t.Fatalf("json.Unmarshal chat error: %v", err)
	}
	choices := chatResp["choices"].([]any)
	firstChoice := choices[0].(map[string]any)
	msg := firstChoice["message"].(map[string]any)
	if msg["content"] != "The answer is 42." {
		t.Fatalf("expected content 'The answer is 42.', got %v", msg["content"])
	}
	if msg["reasoning_content"] != "deep reasoning" {
		t.Fatalf("expected reasoning_content 'deep reasoning', got %v", msg["reasoning_content"])
	}

	// Test TransformBufferedResponse for Anthropic Messages
	adapterMsg := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/messages",
	}, routeengine.Candidate{})

	respAnthropic, err := adapterMsg.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBody)
	if err != nil {
		t.Fatalf("TransformBufferedResponse Anthropic error: %v", err)
	}

	var anthropicResp map[string]any
	if err := json.Unmarshal(respAnthropic.Body, &anthropicResp); err != nil {
		t.Fatalf("json.Unmarshal anthropic error: %v", err)
	}
	contentList := anthropicResp["content"].([]any)
	if len(contentList) != 2 {
		t.Fatalf("expected 2 content blocks (thinking and text), got %v", contentList)
	}
	thinkingBlock := contentList[0].(map[string]any)
	if thinkingBlock["type"] != "thinking" || thinkingBlock["thinking"] != "deep reasoning" {
		t.Fatalf("expected thinking block with deep reasoning, got %v", thinkingBlock)
	}
	if thinkingBlock["signature"] == "" {
		t.Fatal("expected non-empty fake thinking signature")
	}
}

func TestCommandCodeReverseToolMapping(t *testing.T) {
	decoder := newCommandCodeStreamDecoder()
	line := []byte(`{"type":"tool-call","toolCallId":"call_123","toolName":"shell_output","input":{"command":"ls"}}`)
	events, err := decoder.DecodeLine(line)
	if err != nil {
		t.Fatalf("DecodeLine error: %v", err)
	}
	if len(events) < 1 {
		t.Fatalf("expected events, got %d", len(events))
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Type != canonicalStreamEventToolCallDelta || lastEvent.ToolCallName != "bash_output" {
		t.Fatalf("expected reversed tool name 'bash_output', got %#v", lastEvent)
	}

	adapterChat := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
	}, routeengine.Candidate{})
	streamBody := []byte("data: {\"type\":\"tool-call\",\"toolCallId\":\"call_abc\",\"toolName\":\"search_tools\",\"input\":{\"query\":\"git\"}}\n\ndata: {\"type\":\"finish\",\"finishReason\":\"tool_calls\"}\n\ndata: [DONE]\n\n")
	resp, err := adapterChat.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBody)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}
	var chatResp map[string]any
	if err := json.Unmarshal(resp.Body, &chatResp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	choices := chatResp["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	toolCalls := msg["tool_calls"].([]any)
	firstCall := toolCalls[0].(map[string]any)["function"].(map[string]any)
	if firstCall["name"] != "tool_search" {
		t.Fatalf("expected reverse mapped tool name 'tool_search', got %q", firstCall["name"])
	}
}

func TestCommandCodeBuildUpstreamPayloadImages(t *testing.T) {
	adapter := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
		RequestedModel: "claude-sonnet-4-6",
		Payload:        map[string]any{},
		Canonical: &canonicalRequest{
			Messages: []canonicalMessage{
				{
					Role: "user",
					Content: []canonicalContentPart{
						{
							Type:     "image",
							ImageURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==",
						},
						{
							Type:     "image",
							MimeType: "image/webp",
							FileData: "UklGRkAAAABXRUJQVlA4WAoAAAAQAAAAAQAA",
						},
						{
							Type:     "image",
							FileData: "data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==",
						},
						{
							Type:     "image",
							ImageURL: "aGVsbG8gd29ybGQ=",
						},
					},
				},
			},
		},
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "claude-sonnet-4-6",
		},
	})

	payload, err := adapter.BuildUpstreamPayload(adapter.request, adapter.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}

	params := payload["params"].(map[string]any)
	msgs := params["messages"].([]map[string]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	parts := msgs[0]["content"].([]map[string]any)
	if len(parts) != 4 {
		t.Fatalf("expected 4 image parts, got %d", len(parts))
	}

	// 1. Data URL in ImageURL
	if parts[0]["mimeType"] != "image/png" || parts[0]["image"] != "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==" {
		t.Fatalf("unexpected image 1: %v", parts[0])
	}

	// 2. Base64 in FileData with MimeType
	if parts[1]["mimeType"] != "image/webp" || parts[1]["image"] != "data:image/webp;base64,UklGRkAAAABXRUJQVlA4WAoAAAAQAAAAAQAA" {
		t.Fatalf("unexpected image 2: %v", parts[1])
	}

	// 3. Data URL in FileData
	if parts[2]["mimeType"] != "image/gif" || parts[2]["image"] != "data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==" {
		t.Fatalf("unexpected image 3: %v", parts[2])
	}

	// 4. Raw base64 in ImageURL
	if parts[3]["mimeType"] != "image/jpeg" || parts[3]["image"] != "data:image/jpeg;base64,aGVsbG8gd29ybGQ=" {
		t.Fatalf("unexpected image 4: %v", parts[3])
	}
}

func TestCommandCodeBuildUpstreamPayloadThreadID(t *testing.T) {
	// Case 1: valid UUID
	validUUID := "12345678-1234-1234-1234-123456789abc"
	reqValid := gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
		DownstreamHeaders: http.Header{
			"X-Session-Id": []string{validUUID},
		},
		Canonical: &canonicalRequest{},
	}
	adapter1 := newCommandCodeProtocolAdapter(reqValid, routeengine.Candidate{})
	payload1, err := adapter1.BuildUpstreamPayload(reqValid, adapter1.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}
	if payload1["threadId"] != validUUID {
		t.Fatalf("expected threadId %s, got %v", validUUID, payload1["threadId"])
	}

	// Case 2: non-UUID string
	nonUUID := "custom-session-string"
	reqInvalid := gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
		DownstreamHeaders: http.Header{
			"X-Session-Id": []string{nonUUID},
		},
		Canonical: &canonicalRequest{},
	}
	adapter2 := newCommandCodeProtocolAdapter(reqInvalid, routeengine.Candidate{})
	payload2, err := adapter2.BuildUpstreamPayload(reqInvalid, adapter2.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}
	if _, ok := payload2["threadId"]; ok {
		t.Fatalf("expected threadId NOT to be set for non-UUID, got %v", payload2["threadId"])
	}
}

func TestCommandCodeBufferedResponseModelFallback(t *testing.T) {
	streamBodyWithoutModel := []byte("data: {\"type\":\"text-delta\",\"text\":\"Hi\"}\n\ndata: {\"type\":\"finish\",\"finishReason\":\"stop\"}\n\ndata: [DONE]\n\n")

	candidate := routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "claude-sonnet-4-6",
		},
	}

	// 1. Anthropic fallback
	adapterAnthropic := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/messages",
		RequestedModel: "claude-requested",
	}, candidate)
	respA, err := adapterAnthropic.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBodyWithoutModel)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}
	var mapA map[string]any
	if err := json.Unmarshal(respA.Body, &mapA); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if mapA["model"] != "claude-sonnet-4-6" {
		t.Fatalf("expected model claude-sonnet-4-6, got %v", mapA["model"])
	}

	// 2. OpenAI Chat fallback
	adapterChat := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/chat/completions",
		RequestedModel: "claude-requested",
	}, candidate)
	respC, err := adapterChat.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBodyWithoutModel)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}
	var mapC map[string]any
	if err := json.Unmarshal(respC.Body, &mapC); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if mapC["model"] != "claude-sonnet-4-6" {
		t.Fatalf("expected model claude-sonnet-4-6, got %v", mapC["model"])
	}

	// 3. OpenAI Responses fallback
	adapterResp := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/responses",
		RequestedModel: "claude-requested",
	}, candidate)
	respR, err := adapterResp.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBodyWithoutModel)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}
	var mapR map[string]any
	if err := json.Unmarshal(respR.Body, &mapR); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if mapR["model"] != "claude-sonnet-4-6" {
		t.Fatalf("expected model claude-sonnet-4-6, got %v", mapR["model"])
	}

	// 4. Upstream explicit model overrides candidate
	streamBodyWithModel := []byte("data: {\"type\":\"text-delta\",\"model\":\"custom-upstream-model\",\"text\":\"Hi\"}\n\ndata: {\"type\":\"finish\",\"finishReason\":\"stop\"}\n\ndata: [DONE]\n\n")
	respWithModel, err := adapterChat.TransformBufferedResponse(http.StatusOK, http.Header{}, streamBodyWithModel)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}
	var mapWithModel map[string]any
	if err := json.Unmarshal(respWithModel.Body, &mapWithModel); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if mapWithModel["model"] != "custom-upstream-model" {
		t.Fatalf("expected model custom-upstream-model, got %v", mapWithModel["model"])
	}
}

func TestCommandCodeBufferedResponseAnthropicEmptyContentFallback(t *testing.T) {
	streamEmpty := []byte("data: {\"type\":\"finish\",\"finishReason\":\"stop\"}\n\ndata: [DONE]\n\n")

	adapter := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/messages",
		RequestedModel: "claude-sonnet-4-6",
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "claude-sonnet-4-6",
		},
	})

	resp, err := adapter.TransformBufferedResponse(http.StatusOK, http.Header{}, streamEmpty)
	if err != nil {
		t.Fatalf("TransformBufferedResponse error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	content, ok := parsed["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected content length 1, got %v", parsed["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" || block["text"] != "" {
		t.Fatalf("expected text block with empty text, got %v", block)
	}
}

func TestCommandCodeHandleBufferedResponseUsesRawBytes(t *testing.T) {
	adapter := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/responses",
		RequestedModel: "meta/muse-spark-1.3-contributor",
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "meta/muse-spark-1.3-contributor",
		},
	})

	sseBody := strings.Join([]string{
		`{"type":"start"}`,
		`{"type":"text-start","id":"txt-0"}`,
		`{"type":"text-delta","id":"txt-0","text":"Pong! 👋"}`,
		`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":10,"outputTokens":5}}`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
		},
		Body: io.NopCloser(strings.NewReader(sseBody)),
	}

	handler := Handler{logger: gatewayDiscardLogger()}
	result := handler.handleBufferedResponse(
		context.Background(),
		"test-req-id",
		uuid.New(),
		uuid.New(),
		adapter.candidate,
		adapter,
		resp,
		gatewayAttemptResult{attempt: 1, downstreamPath: "/v1/responses", currency: "USD"},
		time.Now(),
	)

	if !result.success {
		t.Fatalf("expected success true, got false (errorType=%s, errMsg=%s)", result.errorType, result.errorMessage)
	}
	if result.statusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", result.statusCode)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result.body, &parsed); err != nil {
		t.Fatalf("failed to unmarshal transformed response body: %v", err)
	}
	if parsed["status"] != "completed" {
		t.Fatalf("expected status completed, got %v", parsed["status"])
	}
	if parsed["model"] != "meta/muse-spark-1.3-contributor" {
		t.Fatalf("expected model meta/muse-spark-1.3-contributor, got %v", parsed["model"])
	}
	outputList, ok := parsed["output"].([]any)
	if !ok || len(outputList) == 0 {
		t.Fatalf("expected output list, got %v", parsed["output"])
	}
}

func TestCommandCodeCredentialSupportsProtocol(t *testing.T) {
	if !credentialSupportsProtocol([]string{"openai"}, "command_code_to_chat_completions") {
		t.Fatal("expected command_code_to_chat_completions to match openai")
	}
	if !credentialSupportsProtocol([]string{"openai-response"}, "command_code_to_responses") {
		t.Fatal("expected command_code_to_responses to match openai-response")
	}
	if !credentialSupportsProtocol([]string{"openai-responses"}, "command_code_to_responses") {
		t.Fatal("expected command_code_to_responses to match legacy openai-responses")
	}
	if !credentialSupportsProtocol([]string{"anthropic-messages"}, "command_code_to_anthropic_messages") {
		t.Fatal("expected command_code_to_anthropic_messages to match anthropic-messages")
	}
	if credentialSupportsProtocol([]string{"openai-image"}, "command_code_to_chat_completions") {
		t.Fatal("expected openai-image not to match command_code_to_chat_completions")
	}
}

func TestCommandCodeLiveUpstreamWithKeyFile(t *testing.T) {
	if os.Getenv("XLYRA_COMMAND_CODE_LIVE_TEST") != "1" {
		t.Skip("set XLYRA_COMMAND_CODE_LIVE_TEST=1 to enable real upstream requests")
	}
	keyBytes, err := os.ReadFile("../../../.cmd_key")
	if err != nil {
		keyBytes, err = os.ReadFile("../../.cmd_key")
	}
	if err != nil {
		keyBytes, err = os.ReadFile(".cmd_key")
	}
	if err != nil {
		t.Skip("skipping live upstream test, .cmd_key not found")
	}
	key := strings.TrimSpace(string(keyBytes))
	if key == "" {
		t.Skip("skipping live upstream test, .cmd_key is empty")
	}

	// 1. Test /v1/responses non-streaming
	adapterResponses := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/responses",
		RequestedModel: "meta/muse-spark-1.3-contributor",
		Payload: map[string]any{
			"model": "meta/muse-spark-1.3-contributor",
		},
		Canonical: &canonicalRequest{
			Messages: []canonicalMessage{
				{Role: "user", Content: []canonicalContentPart{{Type: "text", Text: "Say hello in exactly two words"}}},
			},
		},
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "meta/muse-spark-1.3-contributor",
		},
	})

	payload, err := adapterResponses.BuildUpstreamPayload(adapterResponses.request, adapterResponses.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}

	reqBody, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, adapterResponses.UpstreamPath(""), bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	adapterResponses.ApplyUpstreamHeaders(req, key, "", false)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Live request to Command Code failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Live upstream status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	handler := Handler{logger: gatewayDiscardLogger()}
	result := handler.handleBufferedResponse(
		context.Background(),
		"live-test-req",
		uuid.New(),
		uuid.New(),
		adapterResponses.candidate,
		adapterResponses,
		resp,
		gatewayAttemptResult{attempt: 1, downstreamPath: "/v1/responses", currency: "USD"},
		time.Now(),
	)

	if !result.success {
		t.Fatalf("handleBufferedResponse failed: type=%s msg=%s", result.errorType, result.errorMessage)
	}
	if result.statusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", result.statusCode)
	}

	var parsedResp map[string]any
	if err := json.Unmarshal(result.body, &parsedResp); err != nil {
		t.Fatalf("failed to unmarshal result body: %v\nBody: %s", err, string(result.body))
	}
	if parsedResp["status"] != "completed" {
		t.Fatalf("expected status completed, got %v", parsedResp["status"])
	}
	if parsedResp["model"] != "meta/muse-spark-1.3-contributor" {
		t.Fatalf("expected model meta/muse-spark-1.3-contributor, got %v", parsedResp["model"])
	}
	t.Logf("Live Command Code /v1/responses response: %s", string(result.body))

	// 2. Test /v1/messages non-streaming
	adapterMessages := newCommandCodeProtocolAdapter(gatewayRequest{
		DownstreamPath: "/v1/messages",
		RequestedModel: "meta/muse-spark-1.3-contributor",
		Payload: map[string]any{
			"model": "meta/muse-spark-1.3-contributor",
		},
		Canonical: &canonicalRequest{
			Messages: []canonicalMessage{
				{Role: "user", Content: []canonicalContentPart{{Type: "text", Text: "Say ping in exactly one word"}}},
			},
		},
	}, routeengine.Candidate{
		Model: routeengine.CandidateModel{
			UpstreamName: "meta/muse-spark-1.3-contributor",
		},
	})

	payloadMsg, err := adapterMessages.BuildUpstreamPayload(adapterMessages.request, adapterMessages.candidate)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload failed: %v", err)
	}

	reqBodyMsg, _ := json.Marshal(payloadMsg)
	reqMsg, err := http.NewRequestWithContext(context.Background(), http.MethodPost, adapterMessages.UpstreamPath(""), bytes.NewReader(reqBodyMsg))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	adapterMessages.ApplyUpstreamHeaders(reqMsg, key, "", false)

	respMsg, err := (&http.Client{Timeout: 30 * time.Second}).Do(reqMsg)
	if err != nil {
		t.Fatalf("Live request to Command Code /v1/messages failed: %v", err)
	}
	defer respMsg.Body.Close()

	if respMsg.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(respMsg.Body)
		t.Fatalf("Live upstream status %d: %s", respMsg.StatusCode, string(bodyBytes))
	}

	resultMsg := handler.handleBufferedResponse(
		context.Background(),
		"live-test-req-msg",
		uuid.New(),
		uuid.New(),
		adapterMessages.candidate,
		adapterMessages,
		respMsg,
		gatewayAttemptResult{attempt: 1, downstreamPath: "/v1/messages", currency: "USD"},
		time.Now(),
	)

	if !resultMsg.success {
		t.Fatalf("handleBufferedResponse for messages failed: type=%s msg=%s", resultMsg.errorType, resultMsg.errorMessage)
	}
	if resultMsg.statusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resultMsg.statusCode)
	}

	var parsedMsg map[string]any
	if err := json.Unmarshal(resultMsg.body, &parsedMsg); err != nil {
		t.Fatalf("failed to unmarshal result body: %v\nBody: %s", err, string(resultMsg.body))
	}
	if parsedMsg["type"] != "message" {
		t.Fatalf("expected type message, got %v", parsedMsg["type"])
	}
	if parsedMsg["model"] != "meta/muse-spark-1.3-contributor" {
		t.Fatalf("expected model meta/muse-spark-1.3-contributor, got %v", parsedMsg["model"])
	}
	contentList, ok := parsedMsg["content"].([]any)
	if !ok || len(contentList) == 0 {
		t.Fatalf("expected content blocks in Anthropic response, got %v", parsedMsg["content"])
	}
	t.Logf("Live Command Code /v1/messages response: %s", string(resultMsg.body))
}
