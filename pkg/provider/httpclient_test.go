package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestLogger returns a JSON slog logger writing into buf at the given level.
func newTestLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level}))
}

func TestHTTPClientLogsRequestAndResponse(t *testing.T) {
	var buf bytes.Buffer
	SetHTTPLogger(newTestLogger(&buf, slog.LevelDebug))
	t.Cleanup(func() { SetHTTPLogger(nil) })

	const responseBody = `{"responseCode":"2000000","client_secret":"s3cr3t","amount":{"value":"100.00"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The request body must still be intact despite the transport reading it.
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"accessToken":"tok-123"`) {
			t.Errorf("server saw mangled request body: %s", body)
		}
		w.Header().Set("X-Signature", "deadbeef")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBody))
	}))
	defer srv.Close()

	client := NewHTTPClient("probe", 5*time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL,
		strings.NewReader(`{"accessToken":"tok-123","amount":"100.00"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer super-secret-token")
	req.Header.Set("X-SIGNATURE", "cafebabe")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != responseBody {
		t.Fatalf("caller lost the response body: got %q", got)
	}

	logged := buf.String()
	for _, want := range []string{
		`"msg":"provider api call"`,
		`"provider":"probe"`,
		`"method":"POST"`,
		`"status":200`,
		`"request_body"`,
		`"response_body"`,
		`"***"`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log missing %s\nlog: %s", want, logged)
		}
	}
	for _, secret := range []string{"tok-123", "s3cr3t", "super-secret-token", "cafebabe", "deadbeef"} {
		if strings.Contains(logged, secret) {
			t.Errorf("log leaked secret %q\nlog: %s", secret, logged)
		}
	}
	// Non-sensitive JSON survives redaction (the body is embedded as an
	// escaped JSON string, so match on the raw value).
	if !strings.Contains(logged, "100.00") {
		t.Errorf("expected non-sensitive response field to be logged\nlog: %s", logged)
	}
}

func TestHTTPClientInfoLevelOmitsBodies(t *testing.T) {
	var buf bytes.Buffer
	SetHTTPLogger(newTestLogger(&buf, slog.LevelInfo))
	t.Cleanup(func() { SetHTTPLogger(nil) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"responseCode":"2000000"}`))
	}))
	defer srv.Close()

	client := NewHTTPClient("probe", 5*time.Second)
	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"amount":"100.00"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	logged := buf.String()
	for _, want := range []string{`"msg":"provider api call"`, `"method":"POST"`, `"status":200`} {
		if !strings.Contains(logged, want) {
			t.Errorf("info log missing %s\nlog: %s", want, logged)
		}
	}
	for _, unwanted := range []string{`"request_body"`, `"response_body"`, `"request_headers"`} {
		if strings.Contains(logged, unwanted) {
			t.Errorf("info log should omit %s\nlog: %s", unwanted, logged)
		}
	}
}

func TestHTTPClientLogsFailureAtWarn(t *testing.T) {
	var buf bytes.Buffer
	SetHTTPLogger(newTestLogger(&buf, slog.LevelInfo))
	t.Cleanup(func() { SetHTTPLogger(nil) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"responseCode":"5000000"}`))
	}))
	defer srv.Close()

	client := NewHTTPClient("probe", 5*time.Second)
	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	logged := buf.String()
	for _, want := range []string{`"level":"WARN"`, `"msg":"provider api call rejected"`, `"status":500`} {
		if !strings.Contains(logged, want) {
			t.Errorf("failure log missing %s\nlog: %s", want, logged)
		}
	}
}

func TestHTTPClientWithoutLogger(t *testing.T) {
	SetHTTPLogger(nil)
	t.Cleanup(func() { SetHTTPLogger(nil) })
	if HTTPLogger() != nil {
		t.Fatal("expected no logger")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	client := NewHTTPClient("probe", 5*time.Second)
	resp, err := client.Post(srv.URL, "text/plain", strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != "ok" {
		t.Fatalf("expected body to pass through, got %q", got)
	}
}

func TestHTTPClientRecordsActivityWithoutLogger(t *testing.T) {
	SetHTTPLogger(nil)
	var traces chan HTTPTrace = make(chan HTTPTrace, 1)
	SetHTTPActivityRecorder(func(_ context.Context, trace HTTPTrace) { traces <- trace })
	t.Cleanup(func() { SetHTTPActivityRecorder(nil) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	client := NewHTTPClient("probe", 5*time.Second)
	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"token":"secret","ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	select {
	case trace := <-traces:
		if trace.StatusCode != http.StatusOK || strings.Contains(trace.RequestBody, "secret") {
			t.Fatalf("unexpected trace: %+v", trace)
		}
	case <-time.After(time.Second):
		t.Fatal("activity recorder was not called")
	}
}

func TestRedactJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		leak string
	}{
		{
			name: "flat sensitive fields",
			in:   `{"accessToken":"a","client_secret":"b","tokenType":"Bearer","amount":"100.00"}`,
			want: `{"accessToken":"***","amount":"100.00","client_secret":"***","tokenType":"Bearer"}`,
			leak: "a",
		},
		{
			name: "nested and arrays",
			in:   `{"additionalInfo":{"password":"p"},"items":[{"pin":"1234","name":"x"}]}`,
			want: `{"additionalInfo":{"password":"***"},"items":[{"name":"x","pin":"***"}]}`,
			leak: "1234",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := redactJSON([]byte(tc.in))
			var a, b map[string]any
			if err := json.Unmarshal([]byte(out), &a); err != nil {
				t.Fatalf("output is not JSON: %v (%s)", err, out)
			}
			if err := json.Unmarshal([]byte(tc.want), &b); err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(a, b) {
				t.Errorf("redactJSON mismatch:\n got %s\nwant %s", out, tc.want)
			}
			if strings.Contains(out, `"`+tc.leak+`"`) {
				t.Errorf("redactJSON leaked %q: %s", tc.leak, out)
			}
		})
	}
}

func TestRedactJSONNonJSON(t *testing.T) {
	if got := redactJSON([]byte("not json")); got != "not json" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestRedactHeader(t *testing.T) {
	h := http.Header{
		"Authorization": {"Bearer x"},
		"X-Signature":   {"sig"},
		"Content-Type":  {"application/json"},
	}
	out := redactHeader(h)
	if out.Get("Authorization") != "***" || out.Get("X-Signature") != "***" {
		t.Errorf("sensitive headers not redacted: %v", out)
	}
	if out.Get("Content-Type") != "application/json" {
		t.Errorf("non-sensitive header altered: %v", out)
	}
	// The original must not be mutated.
	if h.Get("Authorization") != "Bearer x" {
		t.Errorf("redactHeader mutated the input: %v", h)
	}
}

// jsonEqual compares two decoded JSON values for deep equality via their
// canonical encodings.
func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}
