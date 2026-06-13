package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
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
			"https://www.googleapis.com/auth/gmail.readonly",
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
		slog.Debug("Starting OAuth login flow")
		state, err := randomState()
		if err != nil {
			slog.Error("Failed to generate auth state", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Could not generate auth state")
			return
		}

		slog.Debug("Generated CSRF state, setting cookie", "cookie_name", stateCookieName)
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
		slog.Info("Redirecting user to Google OAuth", "url", url)
		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	}
}

// NewCallbackHandler handles the OAuth2 callback from Google.
func NewCallbackHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Handling OAuth callback")
		// 1. Validate CSRF state.
		stateCookie, err := r.Cookie(stateCookieName)
		queryState := r.URL.Query().Get("state")
		if err != nil {
			slog.Error("CSRF state validation failed: cookie not found", "cookieName", stateCookieName, "error", err)
			slog.Info("Cookie Header", "header", r.Header.Get("Cookie"))
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid auth state (cookie missing). Please try signing in again. Query state: %s", queryState))
			return
		}
		if stateCookie.Value != queryState {
			slog.Error("CSRF state validation failed: value mismatch", "cookie", stateCookie.Value, "query", queryState)
			writeJSONError(w, http.StatusBadRequest, "Invalid auth state (value mismatch). Please try signing in again.")
			return
		}

		slog.Debug("CSRF state validated successfully. Clearing state cookie.")
		// Clear state cookie.
		http.SetCookie(w, &http.Cookie{Name: stateCookieName, MaxAge: -1, Path: "/"})

		// 2. Exchange authorization code for tokens.
		slog.Debug("Exchanging auth code for tokens")
		oc := OAuthConfig(cfg)
		token, err := oc.Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			slog.Error("Token exchange failed", "error", err)
			writeJSONError(w, http.StatusUnauthorized, "Authentication failed. Please try again.")
			return
		}

		// 3. Fetch user info from Google.
		slog.Debug("Fetching Google User Profile")
		userInfo, err := fetchGoogleUserInfo(r.Context(), token, oc)
		if err != nil {
			slog.Error("Could not fetch user info", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Could not retrieve user profile from Google.")
			return
		}

		// 4. Enforce allowlist.
		slog.Debug("Enforcing user allowlist", "email", userInfo.Email)
		if !isAllowed(userInfo.Email, cfg.OAuthAllowedEmails) {
			slog.Warn("Blocked login attempt from unlisted email", "email", userInfo.Email)
			http.Redirect(w, r, cfg.FrontendURL+"/unauthorized", http.StatusTemporaryRedirect)
			return
		}

		// 5. Mint a session.
		slog.Debug("Minting new session ID")
		sessionID, err := randomSessionID()
		if err != nil {
			slog.Error("Failed to generate session ID", "error", err)
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
		slog.Debug("Persisting session to database", "email", session.Email)
		if err := repository.CreateSession(r.Context(), db, session); err != nil {
			slog.Error("Could not persist session", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Could not create session. Please try again.")
			return
		}

		// 6. Set the session cookie.
		slog.Debug("Setting session cookie and redirecting to frontend")
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
		slog.Info("Successfully logged in user", "email", session.Email)
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
		slog.Debug("Handling /me request to fetch user profile")
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			slog.Warn("No session cookie found")
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		
		slog.Debug("Fetching session from database")
		session, err := repository.GetSession(r.Context(), db, cookie.Value)
		if err != nil || session == nil {
			slog.Warn("Session expired or invalid", "error", err)
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, MaxAge: -1, Path: "/"})
			writeJSONError(w, http.StatusUnauthorized, "Session expired. Please sign in again.")
			return
		}
		
		slog.Info("Successfully retrieved user profile", "email", session.Email)
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
		slog.Debug("Handling logout request")
		if r.Method != http.MethodPost {
			slog.Warn("Invalid method for logout", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Use POST to logout")
			return
		}
		
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			slog.Debug("Deleting session from database")
			_ = repository.DeleteSession(r.Context(), db, cookie.Value)
		} else {
			slog.Debug("No session cookie found during logout")
		}
		
		slog.Debug("Clearing session cookie")
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		
		slog.Info("User logged out successfully")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"})
	}
}
