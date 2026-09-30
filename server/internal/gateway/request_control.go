package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

var errGatewayFirstByteTimeout = errors.New("gateway first byte timeout")

func isGatewayCause(ctx context.Context, cause error) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), cause)
}

func attemptErrorType(ctx context.Context, err error) string {
	switch {
	case isGatewayCause(ctx, errGatewayFirstByteTimeout):
		return "upstream_first_byte_timeout"
	case ctx != nil && errors.Is(context.Cause(ctx), context.Canceled):
		return "downstream_client_cancelled"
	default:
		return transportErrorType(err)
	}
}

func applyGatewayAttemptContextFailure(ctx context.Context, result gatewayAttemptResult) gatewayAttemptResult {
	if ctx == nil || result.responseStarted {
		return result
	}

	switch {
	case isGatewayCause(ctx, errGatewayFirstByteTimeout):
		result.success = false
		result.statusCode = http.StatusGatewayTimeout
		result.errorType = "upstream_first_byte_timeout"
		result.errorMessage = "upstream first byte timeout"
	case ctx != nil && errors.Is(context.Cause(ctx), context.Canceled) && !result.success:
		result.success = false
		result.statusCode = transportFailureStatusCode(context.Canceled)
		result.errorType = "downstream_client_cancelled"
		result.errorMessage = context.Canceled.Error()
	}
	return result
}

func monitorGatewayFirstByteTimeout(ctx context.Context, timeout time.Duration, cancel context.CancelCauseFunc, outputStarted func() bool) {
	if timeout <= 0 || cancel == nil {
		return
	}
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			if ctx.Err() == nil && (outputStarted == nil || !outputStarted()) {
				cancel(errGatewayFirstByteTimeout)
			}
		case <-ctx.Done():
		}
	}()
}

type gatewayRequestControlContextKey struct{}

// gatewayRequestControl couples the request context cancellation with the
// currently active upstream response body. The context is the primary
// cancellation mechanism, while closing the body makes cancellation prompt
// even when a custom transport or proxy does not unblock a body read quickly.
type gatewayRequestControl struct {
	mu            sync.Mutex
	cancelContext context.CancelCauseFunc
	cancelled     bool
	response      *http.Response
}

func newGatewayRequestControl(cancel context.CancelCauseFunc) *gatewayRequestControl {
	return &gatewayRequestControl{cancelContext: cancel}
}

func withGatewayRequestControl(ctx context.Context, control *gatewayRequestControl) context.Context {
	if ctx == nil || control == nil {
		return ctx
	}
	return context.WithValue(ctx, gatewayRequestControlContextKey{}, control)
}

func gatewayRequestControlFromContext(ctx context.Context) *gatewayRequestControl {
	if ctx == nil {
		return nil
	}
	control, _ := ctx.Value(gatewayRequestControlContextKey{}).(*gatewayRequestControl)
	return control
}

func (c *gatewayRequestControl) Cancel() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		return false
	}
	c.cancelled = true
	cancelContext := c.cancelContext
	response := c.response
	c.mu.Unlock()

	if cancelContext != nil {
		cancelContext(context.Canceled)
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return true
}

func (c *gatewayRequestControl) SetResponse(response *http.Response) {
	if c == nil || response == nil || response.Body == nil {
		return
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		_ = response.Body.Close()
		return
	}
	c.response = response
	c.mu.Unlock()
}

func (c *gatewayRequestControl) ClearResponse() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.response = nil
	c.mu.Unlock()
}

type firstByteTrackingWriter struct {
	httpResponseWriter http.ResponseWriter
	onCommit           func()
	onWrite            func(int)
}

func (w firstByteTrackingWriter) Header() http.Header {
	return w.httpResponseWriter.Header()
}

func (w firstByteTrackingWriter) WriteHeader(statusCode int) {
	w.httpResponseWriter.WriteHeader(statusCode)
	if w.onCommit != nil {
		w.onCommit()
	}
}

func (w firstByteTrackingWriter) Write(body []byte) (int, error) {
	written, err := w.httpResponseWriter.Write(body)
	if written > 0 && w.onWrite != nil {
		w.onWrite(written)
	}
	return written, err
}

func (w firstByteTrackingWriter) Flush() {
	if w.onCommit != nil {
		w.onCommit()
	}
	if flusher, ok := w.httpResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w firstByteTrackingWriter) Unwrap() http.ResponseWriter {
	return w.httpResponseWriter
}

func (w firstByteTrackingWriter) WriteSSEFailure(failure downstreamSSEFailure) bool {
	writer, ok := w.httpResponseWriter.(downstreamSSEFailureWriter)
	if !ok {
		return false
	}
	written := writer.WriteSSEFailure(failure)
	if written && w.onWrite != nil {
		w.onWrite(1)
	}
	return written
}

func (w firstByteTrackingWriter) FinishSSE() {
	if writer, ok := w.httpResponseWriter.(downstreamSSELifecycleWriter); ok {
		writer.FinishSSE()
	}
}
