package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/moherik/openpay.id/internal/domain"
)

func (s *Server) getMerchantSettings(w http.ResponseWriter, r *http.Request) {
	merchantID := chiURLParam(r, "id")
	if _, err := s.merchants.GetByID(r.Context(), merchantID); err != nil {
		notFound(w, "merchant")
		return
	}
	settings, err := s.merchantSettings.Get(r.Context(), merchantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot load merchant settings")
		return
	}
	writeData(w, http.StatusOK, settings, nil)
}

func (s *Server) updateMerchantSettings(w http.ResponseWriter, r *http.Request) {
	merchantID := chiURLParam(r, "id")
	if _, err := s.merchants.GetByID(r.Context(), merchantID); err != nil {
		notFound(w, "merchant")
		return
	}
	var body struct {
		ReturnURL        string `json:"return_url"`
		SuccessReturnURL string `json:"success_return_url"`
		FailureReturnURL string `json:"failure_return_url"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	body.ReturnURL = strings.TrimSpace(body.ReturnURL)
	body.SuccessReturnURL = strings.TrimSpace(body.SuccessReturnURL)
	body.FailureReturnURL = strings.TrimSpace(body.FailureReturnURL)
	for _, item := range []struct{ field, value string }{
		{"return_url", body.ReturnURL},
		{"success_return_url", body.SuccessReturnURL},
		{"failure_return_url", body.FailureReturnURL},
	} {
		if err := validateReturnURL(item.value); err != nil {
			invalidField(w, item.field, err.Error())
			return
		}
	}
	settings := &domain.MerchantSettings{
		MerchantID: merchantID, ReturnURL: body.ReturnURL,
		SuccessReturnURL: body.SuccessReturnURL, FailureReturnURL: body.FailureReturnURL,
		UpdatedAt: s.now(),
	}
	if err := s.merchantSettings.Upsert(r.Context(), settings); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot save merchant settings")
		return
	}
	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "merchant.settings", merchantID)
	}
	writeData(w, http.StatusOK, settings, nil)
}

func validateReturnURL(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 2048 {
		return errors.New("must be at most 2048 characters")
	}
	u, err := url.ParseRequestURI(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errors.New("must be an absolute http or https URL without credentials")
	}
	return nil
}
