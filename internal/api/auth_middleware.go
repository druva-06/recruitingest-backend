package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/druva-06/recruitingest-backend/internal/repository"
)

type contextKey string

const contextKeySession contextKey = "session"

// RequireAuth is middleware that validates the session cookie and injects the
// session into the request context. Returns 401 JSON on missing/expired sessions.
func RequireAuth(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "Not authenticated. Please sign in.")
				return
			}

			session, err := repository.GetSession(r.Context(), db, cookie.Value)
			if err != nil || session == nil {
				// Clear stale cookie from browser.
				http.SetCookie(w, &http.Cookie{Name: sessionCookieName, MaxAge: -1, Path: "/"})
				writeJSONError(w, http.StatusUnauthorized, "Session expired. Please sign in again.")
				return
			}

			ctx := context.WithValue(r.Context(), contextKeySession, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionFromContext retrieves the session stored by RequireAuth.
func SessionFromContext(ctx context.Context) *repository.Session {
	s, _ := ctx.Value(contextKeySession).(*repository.Session)
	return s
}
