package utils

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
)

var (
	// IPSalt is a global salt for hashing IPs, initialized at startup.
	IPSalt string
)

// GetIPAddress extracts the real IP address from a request, trusting X-Real-IP from a reverse proxy.
func GetIPAddress(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip != "" {
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
	return hex.EncodeToString(hash[:16]) // Return 32 characters
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
	// This salt is static and part of the tripcode algorithm.
	salt := password + "yalie-salt-shaker"
	h := sha256.Sum256([]byte(salt))
	trip := base64.StdEncoding.EncodeToString(h[:])
	return displayName, "!" + trip[:10]
}

// GenerateBoardSessionHash creates a secure hash for a board-specific session cookie.
func GenerateBoardSessionHash(boardPasswordHash string) string {
	// We hash the already-hashed password with the static IP salt.
	// This creates a new, non-password hash unique to this session's value.
	hash := sha256.Sum256([]byte(boardPasswordHash + IPSalt))
	return hex.EncodeToString(hash[:])
}
