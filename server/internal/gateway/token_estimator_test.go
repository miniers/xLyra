package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayOutputTokenEstimatorCountsSemanticText(t *testing.T) {
	t.Parallel()

	var estimates []int64
	estimator := newGatewayOutputTokenEstimator(func(tokens int64) {
		estimates = append(estimates, tokens)
	})
	estimator.add("Hello, world!")
	estimator.add("你好")

	if got := estimates[len(estimates)-1]; got != 6 {
		t.Fatalf("final estimate = %d, want 6 for 13 ASCII bytes and 2 CJK runes", got)
	}
	if len(estimates) != 2 || estimates[0] != 4 {
		t.Fatalf("estimates = %#v, want monotonic [4 6]", estimates)
	}
}

func TestOpenAIOutputTokenEstimatorWeightsStructuredAndNonASCIIOutput(t *testing.T) {
	t.Parallel()

	var openAIEstimate int64
	openAI := newOpenAIOutputTokenEstimator(func(tokens int64) {
		openAIEstimate = tokens
	})
	openAI.add(strings.Repeat("word ", 20))
	textEstimate := openAIEstimate
	openAI.addToolArguments(`{"query":"你好","limit":10}`)

	if textEstimate <= 0 || openAIEstimate <= textEstimate {
		t.Fatalf("OpenAI estimate did not account for structured/non-ASCII output: text=%d final=%d", textEstimate, openAIEstimate)
	}

	var genericEstimate int64
	generic := newGatewayOutputTokenEstimator(func(tokens int64) {
		genericEstimate = tokens
	})
	generic.add(strings.Repeat("word ", 20))
	if textEstimate <= genericEstimate {
		t.Fatalf("OpenAI text estimate = %d, generic estimate = %d; expected OpenAI calibration to be higher", textEstimate, genericEstimate)
	}
}

func TestGatewayOutputTokenEstimatorUsesExactUsageAndStopsEstimating(t *testing.T) {
	t.Parallel()

	var estimates []int64
	estimator := newOpenAIOutputTokenEstimator(func(tokens int64) {
		estimates = append(estimates, tokens)
	})
	estimator.add(strings.Repeat("output ", 20))
	estimator.setUsage(completionUsage{CompletionTokens: 7})
	estimator.add(strings.Repeat("more output ", 20))

	if got := estimates[len(estimates)-1]; got != 7 {
		t.Fatalf("exact usage estimate = %d, want 7; estimates=%#v", got, estimates)
	}
	if estimator.lastTokenCount != 7 || estimator.exactTokenCount != 7 {
		t.Fatalf("estimator state = %#v, want exact token count 7", estimator)
	}
}

func TestProxyUpstreamStreamObservesRawChatContentWithoutTrimming(t *testing.T) {
	t.Parallel()

	var observed []string
	ctx := withGatewayOutputTokenObserver(context.Background(), func(text string) {
		observed = append(observed, text)
	})
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello "}}]}`,
		`data: {"choices":[{"delta":{"content":"world"}}]}`,
		"data: [DONE]",
	}, "\n\n") + "\n\n"

	_, started, err := proxyUpstreamStream(ctx, httptest.NewRecorder(), gatewayStreamTestResponse(body), time.Now())
	if err != nil {
		t.Fatalf("proxyUpstreamStream returned error: %v", err)
	}
	if !started {
		t.Fatal("expected the stream to start")
	}
	if got := strings.Join(observed, ""); got != "hello world" {
		t.Fatalf("observed output = %q, want %q", got, "hello world")
	}
}

func TestProxyUpstreamStreamPublishesExactUsageToTokenObserver(t *testing.T) {
	t.Parallel()

	var observed completionUsage
	ctx := withGatewayOutputTokenUsageObserver(context.Background(), func(usage completionUsage) {
		observed = usage
	})
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"visible"}}]}`,
		`data: {"usage":{"prompt_tokens":3,"completion_tokens":17,"total_tokens":20}}`,
		"data: [DONE]",
	}, "\n\n") + "\n\n"

	_, started, err := proxyUpstreamStream(ctx, httptest.NewRecorder(), gatewayStreamTestResponse(body), time.Now())
	if err != nil {
		t.Fatalf("proxyUpstreamStream returned error: %v", err)
	}
	if !started || observed.CompletionTokens != 17 {
		t.Fatalf("started=%v observed usage=%+v, want completion_tokens=17", started, observed)
	}
}

func TestProxyResponsesStreamPassthroughObservesSemanticDeltas(t *testing.T) {
	t.Parallel()

	var observed []string
	ctx := withGatewayOutputTokenObserver(context.Background(), func(text string) {
		observed = append(observed, text)
	})
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"q\":1}"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
	}, "\n\n") + "\n\n"

	_, started, err := proxyResponsesStreamPassthrough(ctx, httptest.NewRecorder(), gatewayStreamTestResponse(body), time.Now())
	if err != nil {
		t.Fatalf("proxyResponsesStreamPassthrough returned error: %v", err)
	}
	if !started {
		t.Fatal("expected the stream to start")
	}
	if got := strings.Join(observed, ""); got != `hello{"q":1}` {
		t.Fatalf("observed output = %q, want semantic deltas only", got)
	}
}

func TestProxyCanonicalStreamAnthropicInspectorDoesNotDoubleCountOutput(t *testing.T) {
	t.Parallel()

	var observed []string
	ctx := withGatewayOutputTokenObserver(context.Background(), func(text string) {
		observed = append(observed, text)
	})
	inspector := newProviderAnthropicStreamInspector(false)
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"initial"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"reasoning"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"signature"}}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	rec := httptest.NewRecorder()
	_, started, err := proxyCanonicalStream(
		ctx,
		rec,
		gatewayStreamTestResponse(body),
		time.Now(),
		canonicalProtocolAnthropicMessages,
		canonicalProtocolOpenAIChat,
		canonicalStreamOptions{UpstreamLineInspect: inspector.inspect},
	)
	if err != nil {
		t.Fatalf("proxyCanonicalStream returned error: %v", err)
	}
	if !started {
		t.Fatal("expected the stream to start")
	}
	if got := strings.Join(observed, ""); got != "visibleinitialreasoning" {
		t.Fatalf("observed output = %q, want each semantic fragment once", got)
	}
}
