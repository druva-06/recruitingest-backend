package api

import (
	"log/slog"
	"net/http"
	"time"
)

// responseRecorder wraps http.ResponseWriter to capture the status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(statusCode int) {
	rec.statusCode = statusCode
	rec.ResponseWriter.WriteHeader(statusCode)
}

// WithLogging is a middleware that adds production-grade structured logging to every HTTP request.
func WithLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rec := &responseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default if WriteHeader is never called
		}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)

		slog.Info("HTTP Request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_ip", r.RemoteAddr),
			slog.Int("status", rec.statusCode),
			slog.Duration("duration", duration),
			slog.String("user_agent", r.UserAgent()),
		)
	})
}
