// yib/utils/security_test.go
package utils

import (
	"net/http/httptest"
	"testing"
)

// TestGenerateTripcode validates that tripcode generation is correct and consistent.
func TestGenerateTripcode(t *testing.T) {
	testCases := []struct {
		name         string
		input        string
		expectedName string
		expectedTrip string
	}{
		{
			name:         "No Tripcode",
			input:        "Anonymous",
			expectedName: "Anonymous",
			expectedTrip: "",
		},
		{
			name:         "Simple Tripcode",
			input:        "user#password",
			expectedName: "user",
			expectedTrip: "!t3e7Tz8pDP",
		},
		{
			name:  "Empty Name with Tripcode",
			input: "#password",
			// CORRECTED HASH
			expectedTrip: "!t3e7Tz8pDP",
		},
		{
			name:         "Empty Tripcode",
			input:        "user#",
			expectedName: "user",
			expectedTrip: "",
		},
		{
			name:         "Name with spaces",
			input:        " new user # trip pass ",
			expectedName: "new user",
			expectedTrip: "!eKEqaz31Fb",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			displayName, tripcode := GenerateTripcode(tc.input)
			if displayName != tc.expectedName {
				t.Errorf("Expected name to be '%s', but got '%s'", tc.expectedName, displayName)
			}
			if tripcode != tc.expectedTrip {
				t.Errorf("Expected tripcode to be '%s', but got '%s'", tc.expectedTrip, tripcode)
			}
		})
	}
}

// TestHashIP ensures that hashing is consistent and produces the expected format.
func TestHashIP(t *testing.T) {
	// Set a static global salt for the duration of this test.
	IPSalt = "test-salt-for-predictable-hashes"
	defer func() { IPSalt = "" }() // Clean up after the test

	input := "192.168.1.1"
	expectedHash := "5121baeed07f193c00a644a53de2ccdb"

	hash := HashIP(input)

	if len(hash) != 32 {
		t.Errorf("Expected hash length to be 32, but got %d", len(hash))
	}
	if hash != expectedHash {
		t.Errorf("Expected hash to be '%s', but got '%s'", expectedHash, hash)
	}

	// Test that the same input produces the same hash
	hash2 := HashIP(input)
	if hash != hash2 {
		t.Error("Hashing the same input twice produced different results")
	}

	// Test that different input produces a different hash
	hash3 := HashIP("127.0.0.1")
	if hash == hash3 {
		t.Error("Hashing different inputs produced the same result")
	}
}

// TestIsModerator verifies the logic for identifying moderator IPs.
func TestIsModerator(t *testing.T) {
	testCases := []struct {
		name       string
		remoteAddr string // Use RemoteAddr directly to handle IPv6 formatting
		expected   bool
	}{
		{"Loopback IPv4", "127.0.0.1:12345", true},
		{"Loopback IPv6", "[::1]:12345", true},
		{"Private Class A", "10.0.0.5:12345", true},
		{"Private Class B", "172.16.10.20:12345", true},
		{"Private Class C", "192.168.1.100:12345", true},
		{"Public Google DNS", "8.8.8.8:12345", false},
		{"Public Cloudflare DNS", "1.1.1.1:12345", false},
		{"Invalid IP", "not-an-ip", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr

			isMod := IsModerator(req)
			if isMod != tc.expected {
				t.Errorf("Expected IsModerator for IP %s to be %v, but got %v", tc.remoteAddr, tc.expected, isMod)
			}
		})
	}

	// Test with X-Real-IP header
	t.Run("Public IP with X-Real-IP Header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "8.8.8.8:12345"
		req.Header.Set("X-Real-IP", "192.168.1.50") // A private IP
		isMod := IsModerator(req)
		if !isMod {
			t.Error("Expected IsModerator to be true when a private IP is in X-Real-IP header")
		}
	})
}
