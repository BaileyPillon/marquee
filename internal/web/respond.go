package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxBody is the largest JSON request body accepted.
const maxBody = 1 << 20

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON writes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// writeError writes {"error":{"code":"...","message":"..."}}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// decodeJSON reads one JSON object from the body into dst. On failure it has
// already written the error response and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONLimit(w, r, dst, maxBody)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That request is too big.")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "invalid_json", "The request needs a JSON body.")
		default:
			writeError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		}
		return false
	}
	// Exactly one value: reject trailing garbage.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "The request body must be a single JSON value.")
		return false
	}
	return true
}
