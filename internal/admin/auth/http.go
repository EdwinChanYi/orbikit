package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// CookieName is the HttpOnly session cookie.
	CookieName = "orbikit_session"
	// SessionTTL is the lifetime of a login session and its cookie.
	SessionTTL = 12 * time.Hour
)

const (
	failBody = `{"error":"登录失败"}`
	meBody   = `{"username":"admin"}`
)

// handler serves the login API. password disables login when empty.
// Credential checks use the hash stored by EnsureOperator.
type handler struct {
	store       Store
	hasPassword bool
	now         func() time.Time
}

// NewHandler serves POST /api/login, POST /api/logout, and GET /api/me.
// password is the configured operator password. now supplies the session clock.
func NewHandler(store Store, password []byte, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	h := &handler{store: store, hasPassword: len(password) != 0, now: now}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.logout)
	mux.HandleFunc("GET /api/me", h.me)
	return mux
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.hasPassword {
		writeUnauthorized(w)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		writeUnauthorized(w)
		return
	}
	hash, err := h.store.PasswordHash(r.Context())
	if err != nil || bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) != nil {
		writeUnauthorized(w)
		return
	}

	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		writeUnauthorized(w)
		return
	}
	sum := sha256.Sum256(token)
	expires := h.now().Add(SessionTTL)
	if err := h.store.CreateSession(r.Context(), sum[:], expires); err != nil {
		writeUnauthorized(w)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    hex.EncodeToString(token),
		Path:     "/",
		MaxAge:   int(SessionTTL / time.Second),
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if hash, ok := sessionHash(c.Value); ok {
			if err := h.store.DeleteSession(r.Context(), hash); err != nil {
				clearSessionCookie(w)
				writeUnauthorized(w)
				return
			}
		}
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		writeUnauthorized(w)
		return
	}
	hash, ok := sessionHash(c.Value)
	if !ok {
		writeUnauthorized(w)
		return
	}
	valid, err := h.store.SessionValid(r.Context(), hash, h.now())
	if err != nil || !valid {
		writeUnauthorized(w)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, meBody)
}

// sessionHash hashes the raw token carried in the cookie as lowercase hex.
func sessionHash(value string) ([]byte, bool) {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, failBody)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
}
