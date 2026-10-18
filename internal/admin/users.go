package admin

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

// listUsers serves GET /api/users. Readable by every signed-in role (tech and
// up per the role ranking), mutable only by owners — enforced at the router.
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list users")
		return
	}
	out := make([]*userPayload, 0, len(users))
	for _, u := range users {
		out = append(out, toUserPayloadPtr(u))
	}
	writeData(w, http.StatusOK, out, map[string]any{"count": len(out)})
}

// createUser serves POST /api/users. No email infrastructure exists in this
// build: the console generates a one-time password and shows it once.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !strings.Contains(email, "@") {
		invalidField(w, "email", "email is not valid")
		return
	}
	role := domain.AdminRole(strings.TrimSpace(body.Role))
	if !role.Valid() {
		invalidField(w, "role", "role must be owner, operator or tech")
		return
	}

	temporary, err := generatePassword()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot generate password")
		return
	}
	hash, err := hashPassword(temporary)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot hash password")
		return
	}

	now := s.now()
	user := &domain.AdminUser{
		ID:           s.newID("usr"),
		Email:        email,
		Name:         strings.TrimSpace(body.Name),
		PasswordHash: hash,
		Role:         role,
		Status:       domain.AdminUserActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.users.Create(r.Context(), user); err != nil {
		s.writeEngineError(w, err, "cannot create user")
		return
	}

	if actor, _ := userFromContext(r.Context()); actor != nil {
		s.auditWrite(r, actor, "user.create", user.Email+" "+string(user.Role))
	}
	writeData(w, http.StatusCreated, toUserPayloadPtr(user), map[string]any{
		// One-time password, shown once in the invite dialog.
		"password": temporary,
	})
}

// updateUser serves PATCH /api/users/{id} {role?, status?}. Both changes kill
// the user's live sessions so a demoted or suspended account cannot keep a
// browser session running.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	actor, _ := userFromContext(r.Context())
	target, err := s.users.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "user")
		return
	}

	changedRole, changedStatus := "", ""
	if body.Role != nil {
		if actor != nil && actor.ID == target.ID && domain.AdminRole(*body.Role) != target.Role {
			invalidField(w, "role", "you cannot change your own role")
			return
		}
		role := domain.AdminRole(strings.TrimSpace(*body.Role))
		if !role.Valid() {
			invalidField(w, "role", "role must be owner, operator or tech")
			return
		}
		if role != target.Role {
			target.Role = role
			changedRole = string(role)
		}
	}
	if body.Status != nil {
		if actor != nil && actor.ID == target.ID && *body.Status != domain.AdminUserActive {
			invalidField(w, "status", "you cannot suspend yourself")
			return
		}
		switch *body.Status {
		case domain.AdminUserActive, domain.AdminUserSuspended:
			if target.Status != *body.Status {
				target.Status = *body.Status
				changedStatus = *body.Status
			}
		default:
			invalidField(w, "status", "status must be active or suspended")
			return
		}
	}

	if changedRole == "" && changedStatus == "" {
		writeData(w, http.StatusOK, toUserPayloadPtr(target), map[string]any{"changed": false})
		return
	}
	target.UpdatedAt = s.now()
	if err := s.users.Update(r.Context(), target); err != nil {
		s.writeEngineError(w, err, "cannot update user")
		return
	}
	if changedRole != "" || changedStatus == domain.AdminUserSuspended {
		if err := s.sessions.DeleteForUser(r.Context(), target.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "cannot clear sessions")
			return
		}
	}

	labels := labelsFor(changedRole, changedStatus)
	s.auditWrite(r, actor, "user."+labels[0], target.Email+" "+labels[1])
	writeData(w, http.StatusOK, toUserPayloadPtr(target), map[string]any{"changed": true})
}

// labelsFor turns the two change dimensions into one audit action name and
// one target note, keeping the audit log readable ("user.suspend",
// "user.set_role" instead of a combinatorial zoo).
func labelsFor(role, status string) [2]string {
	switch {
	case status == domain.AdminUserSuspended:
		return [2]string{"suspend", ""}
	case status == domain.AdminUserActive:
		return [2]string{"restore", ""}
	case role != "":
		return [2]string{"set_role", role}
	default:
		return [2]string{"update", ""}
	}
}

// listAudit serves GET /api/audit?q=&limit=&offset=.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	f := repository.AuditFilter{
		Search: strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:  parseInt(r.URL.Query().Get("limit"), 50),
		Offset: parseInt(r.URL.Query().Get("offset"), 0),
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	entries, err := s.audit.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot read audit log")
		return
	}
	writeData(w, http.StatusOK, entries, paginationMeta(f.Limit, f.Offset, len(entries)))
}

func toUserPayloadPtr(u *domain.AdminUser) *userPayload {
	p := toUserPayload(u)
	return &p
}

// generatePassword returns a readable one-time password: 16 bytes of entropy
// base64url-encoded, stripped of characters that hurt dictation over the phone.
func generatePassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generating password: %w", err)
	}
	pw := base64.RawURLEncoding.EncodeToString(buf)
	replacer := strings.NewReplacer("-", "x", "_", "y")
	return replacer.Replace(pw), nil
}
