package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

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
