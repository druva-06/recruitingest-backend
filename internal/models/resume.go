package models

import "time"

type UserResume struct {
	Email          string    `json:"email"`
	ResumeFilename string    `json:"resume_filename"`
	ResumeText     string    `json:"-"`
	DriveLink      string    `json:"drive_link"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
