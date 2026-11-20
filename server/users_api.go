package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Autumn-27/artex/db"
)

// GET /api/auth/me — current session identity.
func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	tok := extractToken(r)
	if !s.sessionOK(tok) {
		writeErr(w, 401, "Unauthorized")
		return
	}
	claims, ok := parseSessionClaims(tok, s.jwtKey)
	if !ok {
		writeErr(w, 401, "Unauthorized")
		return
	}
	username := claims.Subject
	if username == "" {
		username = "ARTEX"
	}
	role := claims.Role
	if role == "" {
		role = "admin"
	}
	writeJSON(w, 200, map[string]any{
		"username": username,
		"role":     role,
		"name":     username,
	})
}

func (s *Server) sessionRole(r *http.Request) (username, role string, ok bool) {
	tok := extractToken(r)
	if !s.sessionOK(tok) {
		return "", "", false
	}
	claims, parsed := parseSessionClaims(tok, s.jwtKey)
	if !parsed {
		return "", "", false
	}
	username = claims.Subject
	if username == "" {
		username = "ARTEX"
	}
	role = claims.Role
	if role == "" {
		role = "admin"
	}
	return username, role, true
}

// GET /api/users — list team accounts (admin only). Includes synthetic ARTEX admin.
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	_, role, ok := s.sessionRole(r)
	if !ok {
		writeErr(w, 401, "Unauthorized")
		return
	}
	if role != "admin" {
		writeErr(w, 403, "admin role required")
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	users, err := pg.ListAuthUsers()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []map[string]any{{
		"id":       "ARTEX",
		"username": "ARTEX",
		"role":     "admin",
		"disabled": false,
		"builtin":  true,
	}}
	for _, u := range users {
		out = append(out, map[string]any{
			"id":         strconv.FormatInt(u.ID, 10),
			"username":   u.Username,
			"role":       u.Role,
			"disabled":   u.Disabled,
			"builtin":    false,
			"created_at": u.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

// POST /api/users — create a team account (admin only).
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	_, role, ok := s.sessionRole(r)
	if !ok {
		writeErr(w, 401, "Unauthorized")
		return
	}
	if role != "admin" {
		writeErr(w, 403, "admin role required")
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(w, r, &req); err != nil {
		writeErr(w, 400, "Invalid request format")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if req.Role == "" {
		req.Role = "operator"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "Password encryption failed")
		return
	}
	u, err := pg.CreateAuthUser(req.Username, string(hash), req.Role)
	if err != nil {
		if err == db.ErrUserExists {
			writeErr(w, 409, "username already exists")
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"id":       strconv.FormatInt(u.ID, 10),
		"username": u.Username,
		"role":     u.Role,
		"disabled": u.Disabled,
	})
}

// POST /api/users/{id}/disable — disable a team account (admin only).
func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	actor, role, ok := s.sessionRole(r)
	if !ok {
		writeErr(w, 401, "Unauthorized")
		return
	}
	if role != "admin" {
		writeErr(w, 403, "admin role required")
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	idStr := r.PathValue("id")
	if idStr == "" || strings.EqualFold(idStr, "ARTEX") {
		writeErr(w, 400, "cannot disable bootstrap admin")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, 400, "bad user id")
		return
	}
	u, err := pg.GetAuthUserByID(id)
	if err != nil {
		writeErr(w, 404, "user not found")
		return
	}
	if u.Username == actor {
		writeErr(w, 400, "cannot disable your own account")
		return
	}
	if err := pg.SetAuthUserDisabled(id, true); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
