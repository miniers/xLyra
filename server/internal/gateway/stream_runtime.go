package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type streamCaptureState struct {
	usage                    completionUsage
	outputTokenObserver      gatewayOutputTokenObserver
	outputToolTokenObserver  gatewayOutputTokenToolObserver
	outputUsageObserver      gatewayOutputTokenUsageObserver
	sawDone                  bool
	streamCompleted          bool
	endReason                string
	errorDetail              string
	semanticFailure          *upstreamSemanticFailure
	firstByteLatency         int64
	malformedLines           int
	annotations              []any
	preOutputEventsBuffered  int
	preOutputFailureDeferred bool
}

func newStreamCaptureState(ctx context.Context) streamCaptureState {
	return streamCaptureState{
		outputTokenObserver:     gatewayOutputTokenObserverFromContext(ctx),
		outputToolTokenObserver: gatewayOutputTokenToolObserverFromContext(ctx),
		outputUsageObserver:     gatewayOutputTokenUsageObserverFromContext(ctx),
	}
}

func (capture *streamCaptureState) observeOutputText(text string) {
	if capture == nil || capture.outputTokenObserver == nil || text == "" {
		return
	}
	capture.outputTokenObserver(text)
}

func (capture *streamCaptureState) observeOutputToolArguments(text string) {
	if capture == nil || text == "" {
		return
	}
	if capture.outputToolTokenObserver != nil {
		capture.outputToolTokenObserver(text)
		return
	}
	capture.observeOutputText(text)
}

func (capture *streamCaptureState) observeOutputUsage(usage completionUsage) {
	if capture == nil || capture.outputUsageObserver == nil {
		return
	}
	capture.outputUsageObserver(usage)
}

func proxyUpstreamStream(
	ctx context.Context,
	w http.ResponseWriter,
	resp *http.Response,
	startedAt time.Time,
) (streamCaptureState, bool, error) {
	return proxyUpstreamStreamWithInspector(ctx, w, resp, startedAt, inspectOpenAIChatStreamLine)
}

func proxyUpstreamStreamWithInspector(
	ctx context.Context,
	w http.ResponseWriter,
	resp *http.Response,
	startedAt time.Time,
	inspectLine func([]byte, *streamCaptureState),
) (streamCaptureState, bool, error) {
	capture := newStreamCaptureState(ctx)
	if resp == nil || resp.Body == nil {
		capture.endReason = "upstream_stream_missing_body"
		return capture, false, fmt.Errorf("upstream stream body is not available")
	}

	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(resp.Body)
	headersWritten := false

	writeHeaders := func() {
		if headersWritten {
			return
		}
		copyStreamingHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		headersWritten = true
	}

	for {
		if err := ctx.Err(); err != nil {
			capture.endReason = "downstream_client_cancelled"
			if headersWritten {
				return capture, true, err
			}
			return capture, false, err
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if !headersWritten {
				capture.firstByteLatency = time.Since(startedAt).Milliseconds()
				writeHeaders()
			}
			if _, writeErr := w.Write(line); writeErr != nil {
				capture.endReason = "downstream_stream_write_failed"
				return capture, headersWritten, writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			if inspectLine != nil {
				inspectLine(line, &capture)
			}
		}

		if err == nil {
			continue
		}
		if err == io.EOF {
			if capture.streamCompleted || capture.sawDone {
				capture.streamCompleted = true
				if capture.endReason == "" {
					capture.endReason = "done"
				}
			} else if capture.endReason != "" {
				// Preserve semantic terminal states detected from stream events.
			} else if headersWritten {
				capture.endReason = "upstream_stream_eof"
			} else {
				capture.endReason = "upstream_stream_empty"
			}
			return capture, headersWritten, nil
		}
		if capture.endReason == "" && (capture.streamCompleted || capture.sawDone) {
			capture.streamCompleted = true
			capture.endReason = "done"
			return capture, headersWritten, nil
		}
		if capture.endReason == "" {
			capture.endReason = "upstream_stream_read_failed"
		}
		if headersWritten {
			return capture, true, err
		}
		return capture, false, err
	}
}

func inspectOpenAIChatStreamLine(line []byte, capture *streamCaptureState) {
	if capture == nil {
		return
	}
	text := strings.TrimSpace(string(line))
	if !strings.HasPrefix(text, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(text, "data:"))
	if data == "" {
		return
	}
	if strings.HasPrefix(data, "[DONE]") {
		capture.sawDone = true
		return
	}

	var envelope struct {
		Choices []chatChoice    `json:"choices"`
		Usage   completionUsage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		return
	}
	for _, choice := range envelope.Choices {
		observeChatMessageContent(capture, choice.Delta.Content)
		reasoning := choice.Delta.ReasoningContent
		if strings.TrimSpace(reasoning) == "" {
			reasoning = choice.Delta.Thinking
		}
		capture.observeOutputText(reasoning)
		for _, toolCall := range choice.Delta.ToolCalls {
			capture.observeOutputToolArguments(toolCall.Function.Arguments)
		}
	}
	if envelope.Usage.TotalTokens > 0 || envelope.Usage.PromptTokens > 0 || envelope.Usage.CompletionTokens > 0 {
		capture.usage = envelope.Usage.normalized()
		capture.observeOutputUsage(capture.usage)
	}
}

func observeChatMessageContent(capture *streamCaptureState, content any) {
	if capture == nil {
		return
	}
	switch value := content.(type) {
	case string:
		capture.observeOutputText(value)
	case []any:
		for _, rawPart := range value {
			part, ok := rawPart.(map[string]any)
			if !ok || strings.TrimSpace(anyString(part["type"])) != "text" {
				continue
			}
			capture.observeOutputText(anyString(part["text"]))
		}
	}
}

func copyStreamingHeaders(dst http.Header, src http.Header) {
	contentType := strings.TrimSpace(src.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream; charset=utf-8"
	}
	dst.Set("Content-Type", contentType)
	dst.Set("Cache-Control", "no-cache, no-transform")
	dst.Set("Connection", "keep-alive")
	dst.Set("X-Accel-Buffering", "no")
	for _, key := range []string{
		"X-Request-Id",
		"Openai-Processing-Ms",
		"Anthropic-Request-Id",
	} {
		if value := strings.TrimSpace(src.Get(key)); value != "" {
			dst.Set(key, value)
		}
	}
}
