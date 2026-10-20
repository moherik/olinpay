package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/moherik/openpay.id/internal/domain"
)

// sessionTTL bounds how long a console session lives. Suspend, role change or
// explicit logout ends it immediately because sessions live server-side.
const sessionTTL = 12 * time.Hour

// sessionCookie is the HttpOnly cookie holding the raw session token.
const sessionCookie = "openpay_admin_session"

// userCtxKey is the private context key holding the authenticated console user.
type userCtxKey struct{}

// userFromContext returns the authenticated console user.
func userFromContext(ctx context.Context) (*domain.AdminUser, bool) {
	u, ok := ctx.Value(userCtxKey{}).(*domain.AdminUser)
	return u, ok
}

// hashToken is the one-way form of the session token that is stored. The raw
// token lives only in the browser cookie.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// newSessionToken generates a 256-bit random session token.
func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generating session token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// requireSession resolves the session cookie to a live console user, rejecting
// suspended users and stale sessions.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			unauthorized(w)
			return
		}
		sess, err := s.sessions.Get(r.Context(), hashToken(c.Value), s.now())
		if err != nil {
			unauthorized(w)
			return
		}
		user, err := s.users.GetByID(r.Context(), sess.UserID)
		if err != nil {
			unauthorized(w)
			return
		}
		if user.Status != domain.AdminUserActive {
			unauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRole guards a route by minimum role. Roles rank owner > operator >
// tech.
func requireRole(minimum domain.AdminRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				unauthorized(w)
				return
			}
			if !user.Role.AtLeast(minimum) {
				forbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- handlers ---------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login authenticates a console user and issues a session cookie. The session
// token is generated fresh per login; only its hash is persisted.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "email and password are required")
		return
	}

	user, err := s.users.GetByEmail(r.Context(), email)
	if err != nil {
		// Equal-cost bcrypt check so unknown accounts and wrong passwords are
		// indistinguishable by timing.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B0S8sXGUmX1nC1E3ZRjrQeJcFZbO"), []byte(body.Password))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	if user.Status != domain.AdminUserActive {
		writeError(w, http.StatusForbidden, "account_suspended", "this account is suspended")
		return
	}

	token, err := newSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot create session")
		return
	}
	if err := s.startSession(w, r, user, token); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot create session")
		return
	}

	// Update last login; a failure here must not block the session.
	now := s.now()
	user.LastLoginAt = &now
	user.UpdatedAt = now
	_ = s.users.Update(r.Context(), user)

	s.auditWrite(r, user, "auth.login", user.Email)

	writeData(w, http.StatusOK, s.mePayload(user), nil)
}

// startSession persists a freshly generated token and sets the session cookie.
// Shared by login and the first-install setup endpoint.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *domain.AdminUser, token string) error {
	now := s.now()
	sess := &domain.AdminSession{
		TokenHash: hashToken(token),
		UserID:    user.ID,
		IP:        clientIP(r),
		ExpiresAt: now.Add(sessionTTL),
		CreatedAt: now,
	}
	if err := s.sessions.Create(r.Context(), sess); err != nil {
		return fmt.Errorf("admin: creating session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

// logout deletes the session server-side and clears the cookie. Sessions end
// regardless of whether the client honours the cookie afterwards.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.sessions.Delete(r.Context(), hashToken(c.Value))
	}
	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "auth.logout", user.Email)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeData(w, http.StatusOK, map[string]bool{"ok": true}, nil)
}

// me returns the signed-in user and the console feature flags in one call, so
// a page load can resolve auth and gating together.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	writeData(w, http.StatusOK, s.mePayload(user), nil)
}

type consoleStatusResponse struct {
	// NeedsSetup is true while no console user exists.
	NeedsSetup bool `json:"needs_setup"`
}

// consoleStatus tells the login page whether the console still needs its
// bootstrap user, without leaking account data.
func (s *Server) consoleStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.users.Count(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot inspect console accounts")
		return
	}
	writeData(w, http.StatusOK, consoleStatusResponse{NeedsSetup: n == 0}, nil)
}

type userPayload struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Server) mePayload(u *domain.AdminUser) map[string]any {
	flags := map[string]bool{}
	if s.flags != nil {
		flags = s.flags()
	}
	return map[string]any{
		"user":  toUserPayload(u),
		"flags": flags,
	}
}

// toUserPayload strips the credential hash fields.
func toUserPayload(u *domain.AdminUser) userPayload {
	return userPayload{
		ID:          u.ID,
		Email:       u.Email,
		Name:        u.Name,
		Role:        string(u.Role),
		Status:      u.Status,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}

// password hashing ------------------------------------------------------------

// hashPassword produces the bcrypt hash stored on admin_users.
func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("admin: hashing password: %w", err)
	}
	return string(b), nil
}
