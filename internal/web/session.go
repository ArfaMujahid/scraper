package web

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ArfaMujahid/scraper/internal/job"
)

// ctxKey is an unexported context-key type so our value can't collide with keys
// from other packages (the correct use of context.WithValue, L6).
type ctxKey int

const ownerKey ctxKey = 0

// sessionCookie is the cookie carrying the anonymous session id.
const sessionCookie = "session_id"

// withSession reads the session_id cookie or mints a new UUID once, sets it as
// an HttpOnly SameSite=Lax cookie, and stashes the resulting OwnerID in the
// request context. This is the v2 auth seam: only how OwnerID is derived would
// change for real authentication.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := ""
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			owner = c.Value
		} else {
			owner = uuid.NewString()
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    owner,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(s.cookieTTL / time.Second),
			})
		}
		ctx := context.WithValue(r.Context(), ownerKey, job.OwnerID(owner))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ownerFrom returns the OwnerID stashed by withSession (empty if absent).
func ownerFrom(ctx context.Context) job.OwnerID {
	o, _ := ctx.Value(ownerKey).(job.OwnerID)
	return o
}
