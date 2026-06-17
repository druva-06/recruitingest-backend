# RecruitIngest Backend: Setup & Operations Guide

This document outlines all the non-coding steps required to configure, run,
and test the Asynchronous PDF Ingestion Engine.

## Repositories

The application is maintained as two separate Git repositories:

* **Backend:** [`druva-06/recruitingest-backend`](https://github.com/druva-06/recruitingest-backend)
* **React frontend:** [`druva-06/recruitingest-web`](https://github.com/druva-06/recruitingest-web)

Clone and enter the backend repository before following this guide:

```bash
git clone https://github.com/druva-06/recruitingest-backend.git
cd recruitingest-backend
```

## 1. Prerequisites
Ensure you have the following installed and available:
*   **Go**: Version 1.25.8 or higher, matching `go.mod`.
*   **MySQL**: Version 8.0+ (running locally or accessible via network).
*   **Google Gemini API Key**: Generate this from Google AI Studio.

---

## 2. Database Configuration

Before starting the application, you must create the database and the table schema. 

1. Access your MySQL instance:
   ```bash
   mysql -u root -p
   ```
2. Create the database (if it doesn't exist) and switch to it:
   ```sql
   CREATE DATABASE recruitingest;
   USE recruitingest;
   ```
3. Run the schema creation queries (crucial for deduplication and job tracking):
   ```sql
   -- Core Data Table
   CREATE TABLE IF NOT EXISTS recruiters (
       id INT AUTO_INCREMENT PRIMARY KEY,
       recruiter_name VARCHAR(255) NOT NULL,
       recruiter_title VARCHAR(255),
       recruiter_email VARCHAR(255) NOT NULL,
       company_name VARCHAR(255),
       source_file VARCHAR(255),
       created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       UNIQUE KEY unique_recruiter_email (recruiter_email)
   );

   -- Job Tracking Table
   CREATE TABLE IF NOT EXISTS jobs (
       id VARCHAR(36) PRIMARY KEY,
       status VARCHAR(50) NOT NULL DEFAULT 'pending',
       total_chunks INT DEFAULT 0,
       processed_chunks INT DEFAULT 0,
       created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
   );

   -- Outreach Email History (scoped per user login)
   CREATE TABLE IF NOT EXISTS outreach_emails (
       id INT AUTO_INCREMENT PRIMARY KEY,
       user_email VARCHAR(255) NOT NULL,
       recruiter_email VARCHAR(255) NOT NULL,
       recruiter_name VARCHAR(255) DEFAULT '',
       company_name VARCHAR(255) DEFAULT '',
       subject TEXT,
       body LONGTEXT,
       sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       INDEX idx_user_email (user_email)
   );

   -- Alter outreach_emails to add tracking columns (run once)
   ALTER TABLE outreach_emails
     ADD COLUMN status VARCHAR(50) NOT NULL DEFAULT 'awaiting_reply' AFTER sent_at,
     ADD COLUMN gmail_thread_id VARCHAR(255) DEFAULT NULL AFTER status,
     ADD COLUMN gmail_message_id VARCHAR(255) DEFAULT NULL AFTER gmail_thread_id,
     ADD COLUMN reminder1_delay_days INT NOT NULL DEFAULT 5,
     ADD COLUMN reminder2_delay_days INT NOT NULL DEFAULT 10,
     ADD COLUMN reminder1_sent_at TIMESTAMP NULL DEFAULT NULL,
     ADD COLUMN reminder2_sent_at TIMESTAMP NULL DEFAULT NULL,
     ADD COLUMN replied_at TIMESTAMP NULL DEFAULT NULL,
     ADD COLUMN ghosted_at TIMESTAMP NULL DEFAULT NULL;

   -- Reminder drafts (Gemini-generated, awaiting user approval)
   CREATE TABLE IF NOT EXISTS reminder_drafts (
       id INT AUTO_INCREMENT PRIMARY KEY,
       outreach_email_id INT NOT NULL,
       user_email VARCHAR(255) NOT NULL,
       reminder_number TINYINT NOT NULL,
       recruiter_email VARCHAR(255) NOT NULL,
       recruiter_name VARCHAR(255) DEFAULT '',
       company_name VARCHAR(255) DEFAULT '',
       gmail_thread_id VARCHAR(255) DEFAULT '',
       gmail_message_id VARCHAR(255) DEFAULT '',
       subject TEXT,
       body LONGTEXT,
       status VARCHAR(30) NOT NULL DEFAULT 'pending',
       generated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       sent_at TIMESTAMP NULL DEFAULT NULL,
       INDEX idx_user_pending (user_email, status)
   );

   -- User reminder delay preferences
   CREATE TABLE IF NOT EXISTS user_reminder_settings (
       email VARCHAR(255) PRIMARY KEY,
       reminder1_delay_days INT NOT NULL DEFAULT 5,
       reminder2_delay_days INT NOT NULL DEFAULT 10,
       updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
   );

   -- Target Jobs (The actual roles you are applying for)
   CREATE TABLE IF NOT EXISTS job_postings (
       id INT AUTO_INCREMENT PRIMARY KEY,
       user_email VARCHAR(255) NOT NULL,
       company_name VARCHAR(255) NOT NULL,
       role_title VARCHAR(255) NOT NULL,
       created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
   );

   -- LinkedIn Profiles (The people - exists only once per user)
   CREATE TABLE IF NOT EXISTS linkedin_profiles (
       id INT AUTO_INCREMENT PRIMARY KEY,
       user_email VARCHAR(255) NOT NULL,
       linkedin_url VARCHAR(255) NOT NULL,
       profile_name VARCHAR(255),
       created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       UNIQUE KEY unique_user_profile (user_email, linkedin_url)
   );

   -- Referral Requests (The interaction linking a Person to a Job)
   CREATE TABLE IF NOT EXISTS referral_requests (
       id INT AUTO_INCREMENT PRIMARY KEY,
       user_email VARCHAR(255) NOT NULL,
       linkedin_profile_id INT NOT NULL,
       job_posting_id INT NOT NULL,
       status VARCHAR(50) NOT NULL DEFAULT 'Pending',
       created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
       FOREIGN KEY (linkedin_profile_id) REFERENCES linkedin_profiles(id) ON DELETE CASCADE,
       FOREIGN KEY (job_posting_id) REFERENCES job_postings(id) ON DELETE CASCADE
   );
   ```

---

## 3. Environment Variables

The application relies on environment variables for secure configuration. 
Create a file named `.env` in the root of the project directory:

```env
# The port the HTTP server will listen on (e.g., 8080 or :8080)
SERVER_PORT=8080

# MySQL Data Source Name (Format: user:password@tcp(host:port)/dbname)
DATABASE_DSN=root:password@tcp(127.0.0.1:3306)/recruitingest

# Your Google Gemini API Key
GEMINI_API_KEY=your_gemini_api_key_here

# The Gemini Model to use (e.g., gemini-1.5-flash or gemini-1.5-pro)
GEMINI_MODEL=gemini-1.5-flash

# The deployed React frontend origin
CORS_ALLOWED_ORIGIN=http://localhost:5173
```
*(Note: Replace `root`, `password`, and `your_gemini_api_key_here` with your actual credentials).*

---

## 4. Running the Application

1. Ensure all Go dependencies are downloaded and up-to-date:
   ```bash
   go mod tidy
   ```
2. Start the server:
   ```bash
   go run main.go
   ```
   *Expected Output:*
   ```text
   Initializing configuration engine...
   Configuration loaded successfully.
   Initializing MySQL connection...
   Successfully connected to MySQL.
   Server successfully started. Listening on port :8080...
   ```

---

## 5. Testing the API

You can test the ingestion engine by sending a PDF file to the `POST /api/v1/upload` endpoint using `curl`. You can optionally supply custom credentials, model specifications, and rate-limiting headers:

```bash
curl -X POST http://localhost:8080/api/v1/upload \
  -H "X-Gemini-API-Key: your_gemini_api_key_here" \
  -H "X-Gemini-Model: gemini-3.5-flash" \
  -H "X-Rate-Limit-Requests: 10" \
  -H "X-Rate-Limit-Interval: 60" \
  -F "file=@/path/to/your/sample_resumes.pdf"
```

*Expected JSON Response:*
```json
{
  "status": "processing",
  "message": "File accepted successfully. Extraction is executing in the background.",
  "job_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
}
```

Check the terminal where the Go server is running. You will see the background worker logging the chunking, LLM extraction, and final database insertion steps.

### Polling Job Status

Use the `job_id` returned from the upload endpoint to poll for status and progress:

```bash
curl http://localhost:8080/api/v1/jobs/a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

*Expected JSON Response:*
```json
{
  "job_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "status": "processing",
  "total_chunks": 5,
  "processed_chunks": 2,
  "created_at": "2026-06-07 10:00:00",
  "updated_at": "2026-06-07 10:01:30"
}
```
*(Status will transition from `pending` -> `processing` -> `completed` or `failed`)*

### Search Recruiters & Pagination

Search across names, titles, companies, and email addresses with built-in pagination support:

```bash
curl "http://localhost:8080/api/v1/recruiters?q=gmail.com&company=Acme&page=1&limit=20"
```

The endpoint accepts optional `page` (default `1`) and `limit` (default `50`, max `100`) query parameters, returning results alongside pagination metadata:

```json
{
  "recruiters": [...],
  "total": 45,
  "page": 1,
  "limit": 20
}
```

### Add a Recruiter Manually

```bash
curl -X POST http://localhost:8080/api/v1/recruiters \
  -H "Content-Type: application/json" \
  -d '{
    "recruiter_name": "Maya Patel",
    "recruiter_title": "Senior Technical Recruiter",
    "recruiter_email": "maya@example.com",
    "company_name": "Example Labs"
  }'
```

Recruiter names and valid email addresses are required. Duplicate email
addresses return a conflict response instead of creating another record.
