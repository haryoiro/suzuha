package middleware

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuth returns a middleware that requires HTTP Basic authentication.
// If username or password is empty, the middleware is a no-op (pass-through).
func BasicAuth(username, password string, next http.Handler) http.Handler {
	if username == "" || password == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for CORS preflight.
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="suzuha admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
