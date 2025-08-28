// yib/utils/env.go
package utils

import "os"

// GetEnv reads an environment variable or returns a default value.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
