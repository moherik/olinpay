package provider

import (
	"context"
	"net/http"
	"sync"
)

type TraceContext struct {
	MerchantID    string
	Environment   string
	ReferenceID   string
	TransactionID string
}

type HTTPTrace struct {
	Provider        string
	Method          string
	URL             string
	RequestHeaders  http.Header
	RequestBody     string
	ResponseHeaders http.Header
	ResponseBody    string
	StatusCode      int
	DurationMs      int64
	Error           string
}

type HTTPActivityRecorder func(context.Context, HTTPTrace)

type traceContextKey struct{}

var (
	traceRecorderMu sync.RWMutex
	traceRecorder   HTTPActivityRecorder
)

func WithTraceContext(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

func TraceContextFrom(ctx context.Context) (TraceContext, bool) {
	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return trace, ok
}

func SetHTTPActivityRecorder(recorder HTTPActivityRecorder) {
	traceRecorderMu.Lock()
	defer traceRecorderMu.Unlock()
	traceRecorder = recorder
}

func CurrentHTTPActivityRecorder() HTTPActivityRecorder {
	traceRecorderMu.RLock()
	defer traceRecorderMu.RUnlock()
	return traceRecorder
}
