package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// ContentSecurityPolicy is sent with every response.
const ContentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data: https://image.tmdb.org https://coverartarchive.org; " +
	"style-src 'self' 'unsafe-inline'; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'"

type ctxKey int

const (
	requestIDKey ctxKey = iota
	principalKey
)

// RequestID returns the id of the request a context belongs to.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// statusWriter records what a handler wrote. Unwrap lets
// http.ResponseController reach the real writer, which is how SSE flushes.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// requestID gives every request an id (kept if the caller sent a sane one) and
// echoes it in X-Request-ID.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// accessLog logs one line per request, without the query string (it can carry
// tokens). Static files and health checks log at debug level.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(sw, r)
		level := s.accessLevel(r.URL.Path)
		if !s.log.Enabled(r.Context(), level) {
			return
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.Log(r.Context(), level, "http",
			"id", RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", sw.bytes,
			"ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r))
	})
}

func (s *Server) accessLevel(path string) slog.Level {
	if strings.HasPrefix(path, "/assets/") || path == "/api/v1/health" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// recoverer turns a panic into a logged 500 instead of a dropped connection.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			s.log.Error("handler panicked",
				"id", RequestID(r.Context()), "method", r.Method, "path", r.URL.Path,
				"panic", rec, "stack", string(debug.Stack()))
			if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
				return // too late to change the response
			}
			writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets the headers every response carries.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
