// Package handler provides HTTP handlers for the admin dashboard API.
package handler

import "net/http"

// Health returns a simple health check response.
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
