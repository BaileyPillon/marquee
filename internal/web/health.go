package web

import (
	"context"
	"net/http"
	"time"
)

type healthBody struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	DB            string `json:"db"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	body := healthBody{
		Status:        "ok",
		Version:       s.Version,
		UptimeSeconds: int64(time.Since(s.Started).Seconds()),
		DB:            "ok",
	}
	code := http.StatusOK
	if s.MainDB.PingContext(ctx) != nil || s.EventsDB.PingContext(ctx) != nil {
		body.Status, body.DB = "degraded", "error"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, body)
}
