# RecruitIngest: Setup & Operations Guide

This document outlines all the non-coding steps required to configure, run, and test the Asynchronous PDF Ingestion Engine.

## 1. Prerequisites
Ensure you have the following installed and available:
*   **Go**: Version 1.20 or higher.
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

You can test the ingestion engine by sending a PDF file to the `POST /api/v1/upload` endpoint using `curl`.

```bash
curl -X POST http://localhost:8080/api/v1/upload \
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