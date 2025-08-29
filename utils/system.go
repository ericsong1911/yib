package utils

import (
	"time"
)

// --- Time Helpers ---

// GetTime returns the current time. Useful for mocking in tests.
func GetTime() time.Time {
	return time.Now()
}

// GetSQLTime returns the current time in UTC for database storage.
func GetSQLTime() time.Time {
	return time.Now().UTC()
}
