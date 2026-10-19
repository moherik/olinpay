package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/httpx"
)

// callFail runs fail() against a stub request and decodes the standard error
// envelope.
func callFail(t *testing.T, err error) (int, httpx.ErrorDetail) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/payments", nil)
	fail(w, r, err, "")
	body, derr := io.ReadAll(w.Result().Body)
	if derr != nil {
		t.Fatalf("reading body: %v", derr)
	}
	var got struct {
		Error httpx.ErrorDetail `json:"error"`
	}
	if derr := json.Unmarshal(body, &got); derr != nil {
		t.Fatalf("decoding body %q: %v", body, derr)
	}
	return w.Result().StatusCode, got.Error
}

func TestFailSurfacesRoutingRejectIdentity(t *testing.T) {
	err := &domain.RejectedError{Status: 402, Code: "amount_not_supported", Message: "too large"}
	status, got := callFail(t, err)
	if status != 402 {
		t.Fatalf("status = %d, want 402", status)
	}
	if got.Code != "amount_not_supported" || got.Message != "too large" || got.Field != "" {
		t.Errorf("body = %+v, want the configured code and message", got)
	}
}

func TestFailFillsMissingRejectDefaults(t *testing.T) {
	status, got := callFail(t, &domain.RejectedError{Status: 500})
	if status != 500 {
		t.Fatalf("status = %d, want 500", status)
	}
	if got.Code != "routing_rejected" {
		t.Errorf("code = %q, want routing_rejected", got.Code)
	}
	if got.Message != domain.ErrRejected.Error() {
		t.Errorf("message = %q, want %q", got.Message, domain.ErrRejected.Error())
	}
}

func TestFailNeverShipsRejectAs2xxOr3xx(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"accepted", 200},
		{"redirect", 301},
		{"below range", 399},
		{"above range", 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, got := callFail(t, &domain.RejectedError{
				Status: tc.status, Code: "custom", Message: "custom message",
			})
			if status < 400 || status > 599 {
				t.Fatalf("status = %d, want a clamped 4xx/5xx", status)
			}
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", status)
			}
			if got.Code != "custom" || got.Message != "custom message" {
				t.Errorf("body = %+v, want the configured identity preserved", got)
			}
		})
	}
}
