// yib/utils/system.go
package utils

import (
	"time"
)

// GetTime returns the current time. Useful for mocking in tests.
func GetTime() time.Time {
	return time.Now()
}

// GetSQLTime returns the current time in UTC for database storage.
func GetSQLTime() time.Time {
	return time.Now().UTC()
}

// GetDailySalt returns a consistent salt for the current day.
func GetDailySalt() string {
	return GetTime().Format("2006-01-02") + "-yib-daily-salt"
}
