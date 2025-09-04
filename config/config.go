// yib/config/config.go
package config

const (
	AppVersion   = "0.53-beta"
	DefaultTheme = "yalie-blue"

	// Form & Post Limits
	MaxNameLen    = 75
	MaxSubjectLen = 100
	MaxCommentLen = 8000

	// File Upload Limits
	MaxFileSize     = 15 * 1024 * 1024 // 15MB
	MaxWidth        = 8000
	MaxHeight       = 8000
	ThumbnailWidth  = 250
	ThumbnailHeight = 250

	// Rate Limiting Defaults
	DefaultRateLimitEvery  = "30s"
	DefaultRateLimitBurst  = 3
	DefaultRateLimitPrune  = "1h"
	DefaultRateLimitExpire = "24h"
)
