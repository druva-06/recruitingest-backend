package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// StartReplyPoller starts a background goroutine that polls Gmail every 3 hours
// to detect recruiter replies and update outreach_emails statuses.
func StartReplyPoller(db *sql.DB, cfg *config.Config) {
	go func() {
		// Initial delay of 5 minutes to let the server fully start
		time.Sleep(5 * time.Minute)

		ticker := time.NewTicker(3 * time.Hour)
		defer ticker.Stop()

		for {
			runReplyPoll(db, cfg)
			<-ticker.C
		}
	}()
}

func runReplyPoll(db *sql.DB, cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	slog.Info("Starting poll cycle")

	// Get all emails that are still pending a reply
	emails, err := repository.GetPendingForPolling(ctx, db)
	if err != nil {
		slog.Error("Failed to fetch pending emails", "error", err)
		return
	}
	if len(emails) == 0 {
		slog.Info("No emails pending reply check")
		return
	}

	slog.Info("Checking email threads for replies", "threadCount", len(emails))

	// Group by user to minimize token refresh calls
	userEmails := groupByUser(emails)

	for userEmail, userOutreachEmails := range userEmails {
		// Get the user's session (refresh token) from DB
		sessions, err := getUserSessions(ctx, db, userEmail)
		if err != nil || len(sessions) == 0 {
			slog.Warn("No active session found, skipping", "userEmail", userEmail)
			continue
		}
		session := sessions[0]

		// Build OAuth client for this user
		ocCfg := buildOAuthConfig(cfg)
		token := &oauth2.Token{
			AccessToken:  session.AccessToken,
			RefreshToken: session.RefreshToken,
			Expiry:       time.Now().Add(-1 * time.Hour),
		}
		tokenSource := ocCfg.TokenSource(ctx, token)
		newToken, err := tokenSource.Token()
		if err != nil {
			slog.Error("Token refresh failed", "userEmail", userEmail, "error", err)
			continue
		}
		if newToken.AccessToken != session.AccessToken {
			_, _ = db.ExecContext(ctx,
				"UPDATE sessions SET access_token=? WHERE email=? ORDER BY expires_at DESC LIMIT 1",
				newToken.AccessToken, userEmail)
		}
		client := ocCfg.Client(ctx, newToken)

		for _, e := range userOutreachEmails {
			checkThread(ctx, db, client, e)
		}
	}

	slog.Info("Poll cycle complete")
}

func checkThread(ctx context.Context, db *sql.DB, client *http.Client, e repository.OutreachEmail) {
	url := fmt.Sprintf(
		"https://gmail.googleapis.com/gmail/v1/users/me/threads/%s?format=minimal",
		e.GmailThreadID,
	)
	resp, err := client.Get(url)
	if err != nil {
		slog.Error("Failed to fetch thread", "threadID", e.GmailThreadID, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		slog.Warn("Thread not found (email may have been deleted)", "threadID", e.GmailThreadID)
		return
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("Thread fetch returned error status", "statusCode", resp.StatusCode, "threadID", e.GmailThreadID)
		return
	}

	var threadResp struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&threadResp); err != nil {
		slog.Error("Failed to decode thread response", "error", err)
		return
	}

	// If more than 1 message in thread → recruiter replied
	if len(threadResp.Messages) > 1 {
		slog.Info("Reply detected for email", "emailID", e.ID, "threadID", e.GmailThreadID)
		_ = repository.UpdateOutreachStatus(ctx, db, e.ID, "replied", "replied_at")
	}
}

func groupByUser(emails []repository.OutreachEmail) map[string][]repository.OutreachEmail {
	result := make(map[string][]repository.OutreachEmail)
	for _, e := range emails {
		result[e.UserEmail] = append(result[e.UserEmail], e)
	}
	return result
}

type sessionRow struct {
	AccessToken  string
	RefreshToken string
}

func getUserSessions(ctx context.Context, db *sql.DB, email string) ([]sessionRow, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT access_token, COALESCE(refresh_token,'') FROM sessions WHERE email=? AND expires_at > NOW() ORDER BY expires_at DESC LIMIT 1",
		email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []sessionRow
	for rows.Next() {
		var s sessionRow
		if err := rows.Scan(&s.AccessToken, &s.RefreshToken); err == nil {
			sessions = append(sessions, s)
		}
	}
	return sessions, rows.Err()
}

func buildOAuthConfig(cfg *config.Config) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		Scopes: []string{
			"https://www.googleapis.com/auth/gmail.send",
			"https://www.googleapis.com/auth/gmail.readonly",
		},
		Endpoint: google.Endpoint,
	}
}
