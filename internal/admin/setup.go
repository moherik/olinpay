package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/internal/domain"
)

// bootstrapPasswordMin is the smallest acceptable first-console password.
const bootstrapPasswordMin = 10

// setupRequest is the first-install wizard's payload: the console's first
// account. Later wizard steps (provider credentials, first merchant and API
// key) reuse the ordinary session-guarded endpoints.
type setupRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// setup serves POST /api/setup. It works only while no console account exists:
// the first account marks the installation as configured and every later call
// answers 409. The created account is an active owner who is signed in
// immediately, so the wizard continues over the normal authenticated API.
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
	if err := s.users.CreateFirst(r.Context(), user); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeError(w, http.StatusConflict, "setup_completed", "this console already has an account; sign in instead")
			return
		}
		s.log.Error("creating first console account", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot create account")
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
