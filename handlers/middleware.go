package handlers

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"yib/utils"

	"github.com/google/uuid"
)

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

const (
	UserCookieKey ContextKey = "userCookieID"
	CSRFTokenKey  ContextKey = "csrfToken"
	AppKey        ContextKey = "app"
)

// AppContextMiddleware injects the App dependency into the request context.
// This is useful for handlers that are not wrapped by MakeHandler, like renderError.
func AppContextMiddleware(app App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), AppKey, app)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CSRFMiddleware protects against Cross-Site Request Forgery attacks.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrfCookie, err := r.Cookie("csrf_token")
		var csrfToken string

		if err != nil || csrfCookie.Value == "" {
			csrfToken = uuid.New().String()
			http.SetCookie(w, &http.Cookie{
				Name:     "csrf_token",
				Value:    csrfToken,
				Path:     "/",
				HttpOnly: true,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
			})
		} else {
			csrfToken = csrfCookie.Value
		}

		if r.Method == "POST" {
			// This check handles both multipart/form-data and application/x-www-form-urlencoded
			tokenFromForm := r.FormValue("csrf_token")
			if tokenFromForm == "" {
				// For AJAX requests that might not use form values directly
				tokenFromForm = r.Header.Get("X-CSRF-Token")
			}

			if subtle.ConstantTimeCompare([]byte(tokenFromForm), []byte(csrfToken)) != 1 {
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}
		}

		ctx := context.WithValue(r.Context(), CSRFTokenKey, csrfToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CookieMiddleware ensures every user has a persistent unique identifier cookie.
func CookieMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("yib_id")
		var userID string
		if err != nil {
			userID = uuid.New().String()
			http.SetCookie(w, &http.Cookie{
				Name:     "yib_id",
				Value:    userID,
				Path:     "/",
				Expires:  utils.GetTime().Add(365 * 24 * 3600 * 1000000000), // 1 year
				HttpOnly: true,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
			})
		} else {
			userID = cookie.Value
		}

		ctx := context.WithValue(r.Context(), UserCookieKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireLAN restricts access to a handler to private or loopback IP addresses.
// The function signature is updated to work with chi's router.
func RequireLAN(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ipStr := utils.GetIPAddress(r)
		ip := net.ParseIP(ipStr)
		if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
			http.Error(w, "Forbidden: Moderation access restricted to LAN", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}