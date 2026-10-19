package admin

import (
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/internal/domain"
)

// bootstrapPasswordMin is the smallest acceptable first-console password. The
// CLI (`openpay create-admin`) stays the expert path; the browser wizard is
// the one place a human types the very first credential, so it enforces a
// floor the CLI deliberately leaves open.
const bootstrapPasswordMin = 10

// setupRequest is the first-install wizard's payload: the console's first
// account. Later wizard steps (provider credentials, first merchant and API
// key) reuse the ordinary session-guarded endpoints.
type setupRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// setup serves POST /api/setup — the browser equivalent of
// `openpay create-admin`. It works only while no console account exists at
// all: the first account marks the installation as configured and every later
// call answers 409 (the WordPress-style one-shot install). The created account
// is an active owner who is signed in immediately, so the wizard continues
// over the normal authenticated API.
//
// The count-then-create check leaves a small window on a multi-instance
// deployment where two different emails could both win; a new installation has
// no traffic to speak of, and `openpay create-admin` carries the same contract.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body setupRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	email := strings.ToLower(strings.TrimSpace(body.Email))
	name := strings.TrimSpace(body.Name)
	password := body.Password
	switch {
	case email == "" || !strings.Contains(email, "@"):
		invalidField(w, "email", "email is not valid")
		return
	case name == "":
		invalidField(w, "name", "name is required")
		return
	case len(password) < bootstrapPasswordMin:
		invalidField(w, "password", "password must be at least 10 characters")
		return
	case len(password) > 72:
		// bcrypt truncates beyond this; refuse instead of silently cutting.
		invalidField(w, "password", "password must be at most 72 characters")
		return
	}

	n, err := s.users.Count(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot inspect console accounts")
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "setup_completed", "this console already has an account — sign in instead")
		return
	}

	hash, err := hashPassword(password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot hash password")
		return
	}
	now := s.now()
	user := &domain.AdminUser{
		ID:           s.newID("usr"),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Role:         domain.RoleOwner,
		Status:       domain.AdminUserActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.users.Create(r.Context(), user); err != nil {
		s.writeEngineError(w, err, "cannot create account")
		return
	}

	// Sign the new owner in so the setup flow can keep going.
	token, err := newSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot create session")
		return
	}
	if err := s.startSession(w, r, user, token); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot create session")
		return
	}
	user.LastLoginAt = &now
	user.UpdatedAt = now
	_ = s.users.Update(r.Context(), user)

	s.auditWrite(r, user, "setup.install", user.Email)
	writeData(w, http.StatusCreated, s.mePayload(user), nil)
}
