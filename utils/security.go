// yib/utils/security.go
package utils

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
)

var (
	IPSalt string
)

// GetIPAddress extracts the real IP address from a request, trusting X-Real-IP from a reverse proxy.
func GetIPAddress(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return cf
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// HashIP creates a salted SHA256 hash of a string (IP or cookie) and returns a truncated hex string.
func HashIP(ip string) string {
	hash := sha256.Sum256([]byte(ip + IPSalt))
	return hex.EncodeToString(hash[:16])
}

// IsModerator checks if the request is coming from a private or loopback IP address.
func IsModerator(r *http.Request) bool {
	ipStr := GetIPAddress(r)
	ip := net.ParseIP(ipStr)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

// GenerateTripcode processes a name string to produce a display name and a secure tripcode.
func GenerateTripcode(name string) (string, string) {
	parts := strings.SplitN(name, "#", 2)
	displayName := strings.TrimSpace(parts[0])
	if len(parts) < 2 || parts[1] == "" {
		return displayName, ""
	}
	password := parts[1]
	salt := password + "yalie-salt-shaker"
	h := sha256.Sum256([]byte(salt))
	trip := base64.StdEncoding.EncodeToString(h[:])
	return displayName, "!" + trip[:10]
}

// GenerateBoardSessionHash creates a secure hash for a board-specific session cookie.
func GenerateBoardSessionHash(boardPasswordHash string) string {
	hash := sha256.Sum256([]byte(boardPasswordHash + IPSalt))
	return hex.EncodeToString(hash[:])
}

// GenerateThreadUserID creates a consistent, thread-specific anonymous ID for a user.
func GenerateThreadUserID(ipHash string, threadID int64, dailySalt string) string {
	input := fmt.Sprintf("%s-%d-%s", ipHash, threadID, dailySalt)
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:4])
}
