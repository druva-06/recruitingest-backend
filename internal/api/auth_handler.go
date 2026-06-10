package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"database/sql"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	sessionCookieName = "ri_session"
	stateCookieName   = "ri_oauth_state"
	sessionTTL        = 7 * 24 * time.Hour
)

// OAuthConfig builds the OAuth2 client config from app config.
func OAuthConfig(cfg *config.Config) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  cfg.OAuthCallbackURL,
		Scopes: []string{
			"openid",
			"email",
			"profile",
			"https://www.googleapis.com/auth/gmail.send",
		},
		Endpoint: google.Endpoint,
	}
}

// randomState generates a cryptographically-random base64 string for CSRF state.
func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// randomSessionID generates a 64-char hex session identifier.
func randomSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// isAllowed checks whether the given email is in the configured allowlist.
func isAllowed(email string, allowed []string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, a := range allowed {
		if a == email {
			return true
		}
	}
	return false
}

// googleUserInfo is the subset we care about from Google's userinfo endpoint.
type googleUserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func fetchGoogleUserInfo(ctx context.Context, token *oauth2.Token, oc *oauth2.Config) (*googleUserInfo, error) {
	client := oc.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return nil, fmt.Errorf("fetching userinfo: %w", err)
	}
	defer resp.Body.Close()
	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding userinfo: %w", err)
	}
	return &info, nil
}

// NewLoginHandler initiates the OAuth flow.
func NewLoginHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := randomState()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Could not generate auth state")
			return
		}

		// Store state in a short-lived HttpOnly cookie for CSRF protection.
		http.SetCookie(w, &http.Cookie{
			Name:     stateCookieName,
			Value:    state,
			Path:     "/",
			MaxAge:   300, // 5 minutes
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})

		oc := OAuthConfig(cfg)
		url := oc.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	}
}

// NewCallbackHandler handles the OAuth2 callback from Google.
func NewCallbackHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Validate CSRF state.
		stateCookie, err := r.Cookie(stateCookieName)
		queryState := r.URL.Query().Get("state")
		if err != nil {
			log.Printf("[Auth] CSRF state validation failed: cookie '%s' not found. Error: %v", stateCookieName, err)
			log.Printf("[Auth] Cookie Header: %s", r.Header.Get("Cookie"))
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid auth state (cookie missing). Please try signing in again. Query state: %s", queryState))
			return
		}
		if stateCookie.Value != queryState {
			log.Printf("[Auth] CSRF state validation failed: value mismatch. Cookie: %s, Query: %s", stateCookie.Value, queryState)
			writeJSONError(w, http.StatusBadRequest, "Invalid auth state (value mismatch). Please try signing in again.")
			return
		}

		// Clear state cookie.
		http.SetCookie(w, &http.Cookie{Name: stateCookieName, MaxAge: -1, Path: "/"})

		// 2. Exchange authorization code for tokens.
		oc := OAuthConfig(cfg)
		token, err := oc.Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			log.Printf("[Auth] Token exchange failed: %v", err)
			writeJSONError(w, http.StatusUnauthorized, "Authentication failed. Please try again.")
			return
		}

		// 3. Fetch user info from Google.
		userInfo, err := fetchGoogleUserInfo(r.Context(), token, oc)
		if err != nil {
			log.Printf("[Auth] Could not fetch user info: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Could not retrieve user profile from Google.")
			return
		}

		// 4. Enforce allowlist.
		if !isAllowed(userInfo.Email, cfg.OAuthAllowedEmails) {
			log.Printf("[Auth] Blocked login attempt from unlisted email: %s", userInfo.Email)
			http.Redirect(w, r, cfg.FrontendURL+"/unauthorized", http.StatusTemporaryRedirect)
			return
		}

		// 5. Mint a session.
		sessionID, err := randomSessionID()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Could not create session")
			return
		}

		refreshToken := ""
		if token.RefreshToken != "" {
			refreshToken = token.RefreshToken
		}

		session := &repository.Session{
			SessionID:    sessionID,
			Email:        strings.ToLower(userInfo.Email),
			Name:         userInfo.Name,
			Picture:      userInfo.Picture,
			AccessToken:  token.AccessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    time.Now().Add(sessionTTL),
		}
		if err := repository.CreateSession(r.Context(), db, session); err != nil {
			log.Printf("[Auth] Could not persist session: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Could not create session. Please try again.")
			return
		}

		// 6. Set the session cookie.
		isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    sessionID,
			Path:     "/",
			MaxAge:   int(sessionTTL.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   isSecure,
		})

		// 7. Redirect to the frontend app.
		http.Redirect(w, r, cfg.FrontendURL, http.StatusTemporaryRedirect)
	}
}

// meResponse is the JSON payload returned to the frontend.
type meResponse struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// NewMeHandler returns the authenticated user's profile from their session.
func NewMeHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		session, err := repository.GetSession(r.Context(), db, cookie.Value)
		if err != nil || session == nil {
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, MaxAge: -1, Path: "/"})
			writeJSONError(w, http.StatusUnauthorized, "Session expired. Please sign in again.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(meResponse{
			Email:   session.Email,
			Name:    session.Name,
			Picture: session.Picture,
		})
	}
}

// NewLogoutHandler deletes the server-side session and clears the cookie.
func NewLogoutHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Use POST to logout")
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			_ = repository.DeleteSession(r.Context(), db, cookie.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"})
	}
}
