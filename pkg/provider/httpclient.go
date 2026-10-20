package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// This file provides the HTTP client every provider adapter uses to talk to its
// gateway. It traces each outbound call and its response through the process
// logger, which is why providers do not construct a bare *http.Client anymore.
//
// The trace is emitted in two layers:
//
//   - One line per call at info level with the provider, method, URL, HTTP
//     status and duration. Failures are logged at warn level instead.
//   - Redacted request/response headers and bodies when the logger is at debug
//     level, so payload-level detail is opt-in and does not flood production
//     logs.
//
// Credentials never reach the log: sensitive headers (Authorization,
// X-SIGNATURE, client keys, cookies) and sensitive JSON fields (access tokens,
// charge tokens, secrets, card data) are replaced with "***" before printing.

// maxLogBodyBytes caps how much of a request or response body is written to the
// log. Payment payloads are small; the cap only guards against pathological
// bodies.
const maxLogBodyBytes = 8 << 10

// httpLogger holds the logger used to trace outbound provider API calls. It is
// installed once at startup by the host application via [SetHTTPLogger]; when
// nil, tracing is disabled and the transport behaves like a plain one.
var (
	httpLoggerMu sync.RWMutex
	httpLogger   *slog.Logger
)

// SetHTTPLogger installs the logger used to trace outbound requests to provider
// APIs and their responses. Passing nil disables tracing. It is safe to call
// concurrently, though it is normally called once during startup.
func SetHTTPLogger(l *slog.Logger) {
	httpLoggerMu.Lock()
	defer httpLoggerMu.Unlock()
	httpLogger = l
}

// HTTPLogger returns the logger currently used for provider API tracing, or nil
// when tracing is disabled.
func HTTPLogger() *slog.Logger {
	httpLoggerMu.RLock()
	defer httpLoggerMu.RUnlock()
	return httpLogger
}

// NewHTTPClient returns an *http.Client for a provider adapter. The timeout is
// applied as the client timeout, and every call is traced through the logger
// installed by [SetHTTPLogger]. providerName is recorded on each trace line.
func NewHTTPClient(providerName string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &loggingTransport{
			provider: strings.TrimSpace(providerName),
			base:     http.DefaultTransport,
		},
	}
}

// loggingTransport is an http.RoundTripper that logs each request and response.
type loggingTransport struct {
	provider string
	base     http.RoundTripper
}

// RoundTrip implements [http.RoundTripper].
func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	logger := HTTPLogger()
	recorder := HTTPActivityRecorderFrom(req.Context())
	if (logger == nil && recorder == nil) || t.base == nil {
		base := t.base
		if base == nil {
			base = http.DefaultTransport
		}
		return base.RoundTrip(req)
	}

	ctx := req.Context()
	verbose := logger != nil && logger.Enabled(ctx, slog.LevelDebug)

	attrs := []any{
		"provider", t.provider,
		"method", req.Method,
		"url", req.URL.String(),
	}
	if verbose || recorder != nil {
		attrs = append(attrs, "request_headers", redactHeader(req.Header))
		if body, ok := peekRequestBody(req); ok {
			attrs = append(attrs, "request_body", redactJSON(body))
		}
	}

	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		if recorder != nil {
			trace := HTTPTrace{Provider: t.provider, Method: req.Method, URL: req.URL.String(), RequestHeaders: redactHeader(req.Header), RequestBody: redactBodyForTrace(req), DurationMs: elapsed, Error: err.Error()}
			recorder(context.WithoutCancel(ctx), trace)
		}
		if logger != nil {
			logger.WarnContext(ctx, "provider api call failed",
				append(attrs, "duration_ms", elapsed, "error", err.Error())...)
		}
		return resp, err
	}

	attrs = append(attrs, "status", resp.StatusCode, "duration_ms", elapsed)
	if verbose || recorder != nil {
		attrs = append(attrs, "response_headers", redactHeader(resp.Header))
		if body, ok := peekResponseBody(resp); ok {
			attrs = append(attrs, "response_body", redactJSON(body))
		}
	}
	if recorder != nil {
		trace := HTTPTrace{Provider: t.provider, Method: req.Method, URL: req.URL.String(), RequestHeaders: redactHeader(req.Header), ResponseHeaders: redactHeader(resp.Header), StatusCode: resp.StatusCode, DurationMs: elapsed}
		if body, ok := peekRequestBody(req); ok {
			trace.RequestBody = redactJSON(body)
		}
		if body, ok := peekResponseBody(resp); ok {
			trace.ResponseBody = redactJSON(body)
		}
		recorder(context.WithoutCancel(ctx), trace)
	}

	if logger != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			logger.WarnContext(ctx, "provider api call rejected", attrs...)
		} else {
			logger.InfoContext(ctx, "provider api call", attrs...)
		}
	}
	return resp, nil
}

func redactBodyForTrace(req *http.Request) string {
	if body, ok := peekRequestBody(req); ok {
		return redactJSON(body)
	}
	return ""
}

// peekRequestBody returns a copy of the request body for logging, restoring the
// body so the real call still sends it intact. ok is false when there is no
// body or it could not be read.
func peekRequestBody(req *http.Request) ([]byte, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, false
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, false
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxLogBodyBytes+1))
		if err != nil {
			return nil, false
		}
		return b, true
	}
	// The request was built without GetBody (e.g. from a stream), so the body
	// can only be inspected by reading it and putting it back.
	b, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	if err != nil {
		return nil, false
	}
	return b, true
}

// peekResponseBody returns a copy of the response body for logging, restoring
// the body so the caller can read it normally. ok is false when there is no
// body or it could not be read.
func peekResponseBody(resp *http.Response) ([]byte, bool) {
	if resp.Body == nil || resp.Body == http.NoBody {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxLogBodyBytes+1))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	if err != nil {
		return nil, false
	}
	return b, true
}

// sensitiveHeaders are redacted wherever they appear. Keys are lower-cased.
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"x-signature":         {},
	"x-client-key":        {},
	"x-api-key":           {},
	"x-callback-token":    {},
	"cookie":              {},
	"set-cookie":          {},
}

// redactHeader returns a copy of h with sensitive values replaced by "***".
func redactHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, v := range h {
		if _, sensitive := sensitiveHeaders[strings.ToLower(k)]; sensitive {
			out[k] = []string{"***"}
			continue
		}
		out[k] = append([]string(nil), v...)
	}
	return out
}

// sensitiveKeys are JSON field names whose values are redacted. Comparison
// ignores case and separators ("access_token" == "accessToken").
var sensitiveKeys = map[string]struct{}{
	"accesstoken":    {},
	"chargetoken":    {},
	"refreshtoken":   {},
	"idtoken":        {},
	"identitytoken":  {},
	"token":          {},
	"clientsecret":   {},
	"secret":         {},
	"privatekey":     {},
	"signature":      {},
	"password":       {},
	"apikey":         {},
	"authorization":  {},
	"cardnumber":     {},
	"cvv":            {},
	"cvn":            {},
	"pin":            {},
	"otp":            {},
	"accountnumber":  {},
	"securitycode":   {},
	"xsignature":     {},
	"xcallbacktoken": {},
}

// redactJSON returns body as a compact string with sensitive field values
// replaced by "***". Non-JSON bodies are returned as-is (truncated).
func redactJSON(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return bodyForLog(body)
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return bodyForLog(body)
	}
	redactValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return bodyForLog(body)
	}
	return bodyForLog(out)
}

// redactValue walks decoded JSON and blanks sensitive values in place.
func redactValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitiveKey(k) {
				t[k] = "***"
				continue
			}
			redactValue(val)
		}
	case []any:
		for _, item := range t {
			redactValue(item)
		}
	}
}

// isSensitiveKey reports whether a JSON field name should be redacted.
func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", " ", "").Replace(normalized)
	_, ok := sensitiveKeys[normalized]
	return ok
}

// bodyForLog renders a body as a string, truncating anything beyond
// maxLogBodyBytes.
func bodyForLog(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > maxLogBodyBytes {
		return string(b[:maxLogBodyBytes]) + "…(truncated)"
	}
	return string(b)
}
