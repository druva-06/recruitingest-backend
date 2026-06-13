package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// OutreachEmail represents a sent outreach email record.
type OutreachEmail struct {
	ID                 int64      `json:"id"`
	UserEmail          string     `json:"user_email"`
	RecruiterEmail     string     `json:"recruiter_email"`
	RecruiterName      string     `json:"recruiter_name"`
	CompanyName        string     `json:"company_name"`
	Subject            string     `json:"subject"`
	Body               string     `json:"body"`
	Status             string     `json:"status"` // awaiting_reply, replied, reminder_1_sent, reminder_2_sent, ghosted, closed
	GmailThreadID      string     `json:"gmail_thread_id"`
	GmailMessageID     string     `json:"gmail_message_id"`
	Reminder1DelayDays int        `json:"reminder1_delay_days"`
	Reminder2DelayDays int        `json:"reminder2_delay_days"`
	SentAt             time.Time  `json:"sent_at"`
	Reminder1SentAt    *time.Time `json:"reminder1_sent_at"`
	Reminder2SentAt    *time.Time `json:"reminder2_sent_at"`
	RepliedAt          *time.Time `json:"replied_at"`
	GhostedAt          *time.Time `json:"ghosted_at"`
}

// SaveOutreachEmail persists a sent email record for the logged-in user.
func SaveOutreachEmail(ctx context.Context, db *sql.DB, e *OutreachEmail) (int64, error) {
	const q = `
		INSERT INTO outreach_emails
			(user_email, recruiter_email, recruiter_name, company_name, subject, body,
			 status, gmail_thread_id, gmail_message_id, reminder1_delay_days, reminder2_delay_days)
		VALUES (?, ?, ?, ?, ?, ?, 'awaiting_reply', ?, ?, ?, ?)
	`
	res, err := db.ExecContext(ctx, q,
		e.UserEmail, e.RecruiterEmail, e.RecruiterName, e.CompanyName, e.Subject, e.Body,
		e.GmailThreadID, e.GmailMessageID, e.Reminder1DelayDays, e.Reminder2DelayDays)
	if err != nil {
		return 0, fmt.Errorf("SaveOutreachEmail: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// GetOutreachEmailsByUser retrieves all sent emails for a specific user, newest first.
func GetOutreachEmailsByUser(ctx context.Context, db *sql.DB, userEmail string) ([]OutreachEmail, error) {
	const q = `
		SELECT id, user_email, recruiter_email, recruiter_name, company_name, subject, body,
			   status, COALESCE(gmail_thread_id,''), COALESCE(gmail_message_id,''),
			   reminder1_delay_days, reminder2_delay_days,
			   sent_at, reminder1_sent_at, reminder2_sent_at, replied_at, ghosted_at
		FROM outreach_emails
		WHERE user_email = ?
		ORDER BY sent_at DESC
		LIMIT 200
	`
	return scanOutreachEmails(ctx, db, q, userEmail)
}

// GetPendingForPolling returns emails still needing reply-checking (not replied/closed/ghosted).
func GetPendingForPolling(ctx context.Context, db *sql.DB) ([]OutreachEmail, error) {
	const q = `
		SELECT id, user_email, recruiter_email, recruiter_name, company_name, subject, body,
			   status, COALESCE(gmail_thread_id,''), COALESCE(gmail_message_id,''),
			   reminder1_delay_days, reminder2_delay_days,
			   sent_at, reminder1_sent_at, reminder2_sent_at, replied_at, ghosted_at
		FROM outreach_emails
		WHERE status IN ('awaiting_reply','reminder_1_sent','reminder_2_sent')
		  AND gmail_thread_id != ''
		ORDER BY sent_at ASC
	`
	return scanOutreachEmails(ctx, db, q)
}

// UpdateOutreachStatus updates only the status + nullable timestamp fields for an email.
func UpdateOutreachStatus(ctx context.Context, db *sql.DB, id int64, status string, tsField string) error {
	var q string
	switch tsField {
	case "replied_at":
		q = "UPDATE outreach_emails SET status=?, replied_at=NOW() WHERE id=?"
	case "reminder1_sent_at":
		q = "UPDATE outreach_emails SET status=?, reminder1_sent_at=NOW() WHERE id=?"
	case "reminder2_sent_at":
		q = "UPDATE outreach_emails SET status=?, reminder2_sent_at=NOW() WHERE id=?"
	case "ghosted_at":
		q = "UPDATE outreach_emails SET status=?, ghosted_at=NOW() WHERE id=?"
	default:
		q = "UPDATE outreach_emails SET status=? WHERE id=?"
	}
	_, err := db.ExecContext(ctx, q, status, id)
	return err
}

// UpdateEmailDelays allows per-email reminder delay override from Sent Emails view.
func UpdateEmailDelays(ctx context.Context, db *sql.DB, id int64, r1Days, r2Days int) error {
	_, err := db.ExecContext(ctx,
		"UPDATE outreach_emails SET reminder1_delay_days=?, reminder2_delay_days=? WHERE id=?",
		r1Days, r2Days, id)
	return err
}

func scanOutreachEmails(ctx context.Context, db *sql.DB, q string, args ...interface{}) ([]OutreachEmail, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("scanOutreachEmails query: %w", err)
	}
	defer rows.Close()

	var emails []OutreachEmail
	for rows.Next() {
		var e OutreachEmail
		var sentAtStr string
		var r1Str, r2Str, repliedStr, ghostedStr sql.NullString
		if err := rows.Scan(
			&e.ID, &e.UserEmail, &e.RecruiterEmail, &e.RecruiterName, &e.CompanyName,
			&e.Subject, &e.Body, &e.Status, &e.GmailThreadID, &e.GmailMessageID,
			&e.Reminder1DelayDays, &e.Reminder2DelayDays,
			&sentAtStr, &r1Str, &r2Str, &repliedStr, &ghostedStr,
		); err != nil {
			return nil, fmt.Errorf("scanOutreachEmails scan: %w", err)
		}
		e.SentAt = parseTS(sentAtStr)
		if r1Str.Valid {
			t := parseTS(r1Str.String)
			e.Reminder1SentAt = &t
		}
		if r2Str.Valid {
			t := parseTS(r2Str.String)
			e.Reminder2SentAt = &t
		}
		if repliedStr.Valid {
			t := parseTS(repliedStr.String)
			e.RepliedAt = &t
		}
		if ghostedStr.Valid {
			t := parseTS(ghostedStr.String)
			e.GhostedAt = &t
		}
		emails = append(emails, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanOutreachEmails rows: %w", err)
	}
	if emails == nil {
		emails = []OutreachEmail{}
	}
	return emails, nil
}

var tsFormats = []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05Z"}

func parseTS(s string) time.Time {
	for _, f := range tsFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
