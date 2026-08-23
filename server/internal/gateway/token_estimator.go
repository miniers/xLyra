package gateway

import (
	"context"
	"math"
	"unicode"
	"unicode/utf8"
)

type gatewayOutputTokenObserver func(string)
type gatewayOutputTokenToolObserver func(string)
type gatewayOutputTokenUsageObserver func(completionUsage)

type gatewayOutputTokenObserverKey struct{}
type gatewayOutputTokenToolObserverKey struct{}
type gatewayOutputTokenUsageObserverKey struct{}

func withGatewayOutputTokenObserver(ctx context.Context, observer gatewayOutputTokenObserver) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, gatewayOutputTokenObserverKey{}, observer)
}

func gatewayOutputTokenObserverFromContext(ctx context.Context) gatewayOutputTokenObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(gatewayOutputTokenObserverKey{}).(gatewayOutputTokenObserver)
	return observer
}

func withGatewayOutputTokenToolObserver(ctx context.Context, observer gatewayOutputTokenToolObserver) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, gatewayOutputTokenToolObserverKey{}, observer)
}

func gatewayOutputTokenToolObserverFromContext(ctx context.Context) gatewayOutputTokenToolObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(gatewayOutputTokenToolObserverKey{}).(gatewayOutputTokenToolObserver)
	return observer
}

func withGatewayOutputTokenUsageObserver(ctx context.Context, observer gatewayOutputTokenUsageObserver) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, gatewayOutputTokenUsageObserverKey{}, observer)
}

func gatewayOutputTokenUsageObserverFromContext(ctx context.Context) gatewayOutputTokenUsageObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(gatewayOutputTokenUsageObserverKey{}).(gatewayOutputTokenUsageObserver)
	return observer
}

// gatewayOutputTokenEstimator counts response content rather than serialized
// SSE frames. The default profile is deliberately conservative and is shared
// by providers without an upstream tokenizer. OpenAI gets a slightly more
// granular profile because its tool arguments and non-ASCII text are commonly
// undercounted by a flat four-characters-per-token rule.
type gatewayOutputTokenEstimator struct {
	asciiBytes      int64
	nonASCIIUnits   int64
	openAIUnits     float64
	openAI          bool
	lastTokenCount  int64
	exactTokenCount int64
	onEstimate      func(int64)
}

func newGatewayOutputTokenEstimator(onEstimate func(int64)) *gatewayOutputTokenEstimator {
	return &gatewayOutputTokenEstimator{onEstimate: onEstimate}
}

func newOpenAIOutputTokenEstimator(onEstimate func(int64)) *gatewayOutputTokenEstimator {
	return &gatewayOutputTokenEstimator{openAI: true, onEstimate: onEstimate}
}

func (e *gatewayOutputTokenEstimator) add(text string) {
	e.addPart(text, false)
}

func (e *gatewayOutputTokenEstimator) addToolArguments(text string) {
	e.addPart(text, true)
}

func (e *gatewayOutputTokenEstimator) addPart(text string, structured bool) {
	if e == nil || text == "" || e.exactTokenCount > 0 {
		return
	}
	if e.openAI {
		e.addOpenAIPart(text, structured)
		return
	}
	for _, r := range text {
		if r < utf8.RuneSelf {
			e.asciiBytes++
			continue
		}
		e.nonASCIIUnits++
	}
	e.publish((e.asciiBytes+3)/4 + e.nonASCIIUnits)
}

func (e *gatewayOutputTokenEstimator) addOpenAIPart(text string, structured bool) {
	for _, r := range text {
		e.openAIUnits += openAIOutputTokenWeight(r)
		if structured && isOpenAIStructuralOutputRune(r) {
			e.openAIUnits += 0.2
		}
	}
	e.publish(int64(math.Ceil(e.openAIUnits)))
}

func (e *gatewayOutputTokenEstimator) setUsage(usage completionUsage) {
	if e == nil {
		return
	}
	tokens := int64(usage.CompletionTokens + usage.AudioOutputTokens)
	if tokens <= 0 {
		return
	}
	e.exactTokenCount = tokens
	e.lastTokenCount = tokens
	if e.onEstimate != nil {
		e.onEstimate(tokens)
	}
}

func (e *gatewayOutputTokenEstimator) publish(tokens int64) {
	if tokens <= e.lastTokenCount {
		return
	}
	e.lastTokenCount = tokens
	if e.onEstimate != nil {
		e.onEstimate(tokens)
	}
}

func openAIOutputTokenWeight(r rune) float64 {
	if r < utf8.RuneSelf {
		switch {
		case unicode.IsSpace(r):
			return 0.25
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return 0.28
		case isOpenAIStructuralOutputRune(r):
			return 0.45
		default:
			return 0.4
		}
	}
	if unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana) {
		return 1.65
	}
	if unicode.Is(unicode.So, r) {
		return 1.9
	}
	return 1.4
}

func isOpenAIStructuralOutputRune(r rune) bool {
	switch r {
	case '{', '}', '[', ']', ':', ',', '"', '\\':
		return true
	default:
		return false
	}
}
