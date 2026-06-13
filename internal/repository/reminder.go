package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ReminderDraft is a Gemini-generated follow-up email draft awaiting user approval.
type ReminderDraft struct {
	ID              int64      `json:"id"`
	OutreachEmailID int64      `json:"outreach_email_id"`
	UserEmail       string     `json:"user_email"`
	ReminderNumber  int        `json:"reminder_number"` // 1 or 2
	RecruiterEmail  string     `json:"recruiter_email"`
	RecruiterName   string     `json:"recruiter_name"`
	CompanyName     string     `json:"company_name"`
	GmailThreadID   string     `json:"gmail_thread_id"`
	GmailMessageID  string     `json:"gmail_message_id"`
	Subject         string     `json:"subject"`
	Body            string     `json:"body"`
	Status          string     `json:"status"` // pending, sent, rejected
	GeneratedAt     time.Time  `json:"generated_at"`
	SentAt          *time.Time `json:"sent_at"`
}

// CreateReminderDraft saves a newly generated Gemini draft.
func CreateReminderDraft(ctx context.Context, db *sql.DB, d *ReminderDraft) (int64, error) {
	// Don't create a duplicate if a pending draft already exists for this email+number
	var existing int64
	_ = db.QueryRowContext(ctx,
		"SELECT id FROM reminder_drafts WHERE outreach_email_id=? AND reminder_number=? AND status='pending'",
		d.OutreachEmailID, d.ReminderNumber).Scan(&existing)
	if existing > 0 {
		return existing, nil
	}

	const q = `
		INSERT INTO reminder_drafts
			(outreach_email_id, user_email, reminder_number, recruiter_email, recruiter_name,
			 company_name, gmail_thread_id, gmail_message_id, subject, body, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')
	`
	res, err := db.ExecContext(ctx, q,
		d.OutreachEmailID, d.UserEmail, d.ReminderNumber,
		d.RecruiterEmail, d.RecruiterName, d.CompanyName,
		d.GmailThreadID, d.GmailMessageID, d.Subject, d.Body)
	if err != nil {
		return 0, fmt.Errorf("CreateReminderDraft: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// GetPendingDraftsByUser retrieves all pending reminder drafts for a user.
func GetPendingDraftsByUser(ctx context.Context, db *sql.DB, userEmail string) ([]ReminderDraft, error) {
	const q = `
		SELECT id, outreach_email_id, user_email, reminder_number,
			   recruiter_email, recruiter_name, company_name,
			   COALESCE(gmail_thread_id,''), COALESCE(gmail_message_id,''),
			   subject, body, status, generated_at, sent_at
		FROM reminder_drafts
		WHERE user_email=? AND status='pending'
		ORDER BY generated_at DESC
	`
	return scanDrafts(ctx, db, q, userEmail)
}

// GetDraftByID retrieves a single draft (for edit/send).
func GetDraftByID(ctx context.Context, db *sql.DB, id int64) (*ReminderDraft, error) {
	const q = `
		SELECT id, outreach_email_id, user_email, reminder_number,
			   recruiter_email, recruiter_name, company_name,
			   COALESCE(gmail_thread_id,''), COALESCE(gmail_message_id,''),
			   subject, body, status, generated_at, sent_at
		FROM reminder_drafts WHERE id=?
	`
	rows, err := db.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := scanDrafts(ctx, db, q, id)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// UpdateDraftContent lets the user edit a draft's subject and body.
func UpdateDraftContent(ctx context.Context, db *sql.DB, id int64, subject, body string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE reminder_drafts SET subject=?, body=? WHERE id=? AND status='pending'",
		subject, body, id)
	return err
}

// MarkDraftSent marks a draft as sent and records the send time.
func MarkDraftSent(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx,
		"UPDATE reminder_drafts SET status='sent', sent_at=NOW() WHERE id=?", id)
	return err
}

// RejectDraft marks a draft as rejected (won't be sent).
func RejectDraft(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx,
		"UPDATE reminder_drafts SET status='rejected' WHERE id=?", id)
	return err
}

// CountPendingDrafts returns the number of pending reminder drafts for a user (for badge).
func CountPendingDrafts(ctx context.Context, db *sql.DB, userEmail string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM reminder_drafts WHERE user_email=? AND status='pending'",
		userEmail).Scan(&n)
	return n, err
}

func scanDrafts(ctx context.Context, db *sql.DB, q string, args ...interface{}) ([]ReminderDraft, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("scanDrafts: %w", err)
	}
	defer rows.Close()

	var drafts []ReminderDraft
	for rows.Next() {
		var d ReminderDraft
		var genAtStr string
		var sentAtStr sql.NullString
		if err := rows.Scan(
			&d.ID, &d.OutreachEmailID, &d.UserEmail, &d.ReminderNumber,
			&d.RecruiterEmail, &d.RecruiterName, &d.CompanyName,
			&d.GmailThreadID, &d.GmailMessageID,
			&d.Subject, &d.Body, &d.Status, &genAtStr, &sentAtStr,
		); err != nil {
			return nil, fmt.Errorf("scanDrafts scan: %w", err)
		}
		d.GeneratedAt = parseTS(genAtStr)
		if sentAtStr.Valid {
			t := parseTS(sentAtStr.String)
			d.SentAt = &t
		}
		drafts = append(drafts, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanDrafts rows: %w", err)
	}
	if drafts == nil {
		drafts = []ReminderDraft{}
	}
	return drafts, nil
}
