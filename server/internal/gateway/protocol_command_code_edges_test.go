package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	routeengine "xlyra/server/internal/router"
)

func TestCommandCodeRequestRoundTripFields(t *testing.T) {
	request := gatewayRequest{
		DownstreamPath: "/v1/responses",
		Payload:        map[string]any{"max_output_tokens": float64(128)},
		Canonical: &canonicalRequest{Messages: []canonicalMessage{
			{Role: "user", Content: []canonicalContentPart{{Type: "image", ImageURL: "https://example.invalid/image.png"}}},
			{Role: "assistant", Thinking: []canonicalThinkingBlock{{Thinking: "reasoning history"}}, ToolCalls: []canonicalToolCall{{ID: "call-1", Name: "read", Arguments: `{}`}}},
			{Role: "tool", ToolCallID: "call-1", Output: "file content"},
		}},
	}
	a := newCommandCodeProtocolAdapter(request, routeengine.Candidate{})
	payload, err := a.BuildUpstreamPayload(request, a.candidate)
	if err != nil {
		t.Fatal(err)
	}
	params := payload["params"].(map[string]any)
	if params["max_tokens"] != 128 {
		t.Fatalf("token limit: %v", params)
	}
	messages := params["messages"].([]map[string]any)
	image := messages[0]["content"].([]map[string]any)[0]
	if image["image"] != "https://example.invalid/image.png" {
		t.Fatalf("URL lost: %v", image)
	}
	thinking := messages[1]["content"].([]map[string]any)[0]
	if thinking["type"] != "reasoning" || thinking["text"] != "reasoning history" {
		t.Fatalf("thinking lost: %v", thinking)
	}
	result := messages[2]["content"].([]map[string]any)[0]
	if result["toolName"] != "read" {
		t.Fatalf("tool name lost: %v", result)
	}
	// Empty key avoids initialization HTTP calls; headers still use the payload's session.
	req := httptest.NewRequest(http.MethodPost, "https://example.invalid/alpha/generate", nil)
	a.ApplyUpstreamHeaders(req, "", "", true)
	if req.Header.Get("x-session-id") != payload["threadId"] {
		t.Fatalf("session mismatch: %v / %v", req.Header.Get("x-session-id"), payload["threadId"])
	}
}

func TestCommandCodeBufferedToolsAndReasoning(t *testing.T) {
	a := newCommandCodeProtocolAdapter(gatewayRequest{DownstreamPath: "/v1/responses"}, routeengine.Candidate{})
	body := `{"type":"reasoning-delta","text":"think"}
{"type":"text-delta","text":"checking"}
{"type":"tool-call","toolCallId":"call-1","toolName":"read","input":{"path":"a"}}
{"type":"finish","finishReason":"tool-calls"}`
	response, err := a.TransformBufferedResponse(200, http.Header{}, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	output := result["output"].([]any)
	if len(output) != 3 {
		t.Fatalf("missing output: %s", response.Body)
	}
	if output[0].(map[string]any)["type"] != "reasoning" {
		t.Fatal("reasoning missing")
	}
	text := output[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if text["type"] != "output_text" {
		t.Fatalf("invalid text: %v", text)
	}
	call := output[2].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call-1" || call["arguments"] != `{"path":"a"}` {
		t.Fatalf("invalid call: %v", call)
	}
}

func TestCommandCodeBufferedFailures(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
		for _, body := range []string{
			`{"type":"error","error":{"message":"unavailable"}}`,
			`{"type":"text-delta","text":"partial"}`,
			`[DONE]`,
			`{"type":"finish","finishReason":"connection-error"}`,
			`{"type":"text-delta","text":"` + strings.Repeat("a", 2*1024*1024) + `"}`,
		} {
			a := newCommandCodeProtocolAdapter(gatewayRequest{DownstreamPath: path}, routeengine.Candidate{})
			if _, err := a.TransformBufferedResponse(200, http.Header{}, []byte(body)); err == nil {
				t.Fatalf("%s accepted failed/truncated response", path)
			}
		}
	}
}

func TestCommandCodeLargeToolEvent(t *testing.T) {
	args := `{"content":"` + strings.Repeat("x", 100*1024) + `"}`
	body := `{"type":"tool-call","toolName":"write","input":` + args + "}\n" + `{"type":"finish","finishReason":"tool-calls"}`
	a := newCommandCodeProtocolAdapter(gatewayRequest{DownstreamPath: "/v1/chat/completions"}, routeengine.Candidate{})
	response, err := a.TransformBufferedResponse(200, http.Header{}, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct{ Arguments string } `json:"function"`
				} `json:"tool_calls"`
			}
		}
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || len(result.Choices[0].Message.ToolCalls) != 1 || result.Choices[0].Message.ToolCalls[0].Function.Arguments != args {
		t.Fatal("large arguments truncated")
	}
}

func TestCommandCodeFinishSemantics(t *testing.T) {
	for _, reason := range []string{"max_tokens", "max_output_tokens", "model_context_window_exceeded", "pause_turn"} {
		for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
			t.Run(path+"/"+reason, func(t *testing.T) {
				body := "{\"type\":\"text-delta\",\"text\":\"partial\"}\n{\"type\":\"finish\",\"finishReason\":\"" + reason + "\"}\n[DONE]\n"
				a := newCommandCodeProtocolAdapter(gatewayRequest{DownstreamPath: path}, routeengine.Candidate{})
				response, err := a.TransformBufferedResponse(200, http.Header{}, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
				want := `"finish_reason":"length"`
				if path == "/v1/messages" {
					want = `"stop_reason":"max_tokens"`
					if reason == "pause_turn" {
						want = `"stop_reason":"pause_turn"`
					}
				}
				if path == "/v1/responses" {
					want = `"status":"incomplete"`
				}
				if !strings.Contains(string(response.Body), want) {
					t.Fatalf("want %s: %s", want, response.Body)
				}
				w := httptest.NewRecorder()
				upstream := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
				capture, _, err := a.ProxyStream(context.Background(), w, upstream, time.Now(), a.candidate)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(w.Body.String(), want) || capture.endReason != "response_incomplete" {
					t.Fatalf("stream terminal: %s / %s", capture.endReason, w.Body.String())
				}
			})
		}
	}
}

func TestCommandCodeStreamWithoutFinish(t *testing.T) {
	a := newCommandCodeProtocolAdapter(gatewayRequest{DownstreamPath: "/v1/chat/completions"}, routeengine.Candidate{})
	w := httptest.NewRecorder()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("{\"type\":\"text-delta\",\"text\":\"partial\"}\n"))}
	_, _, err := a.ProxyStream(context.Background(), w, resp, time.Now(), a.candidate)
	if err == nil || strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("truncation hidden: %v / %s", err, w.Body.String())
	}
}
