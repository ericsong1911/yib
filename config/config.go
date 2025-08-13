package config

const (
	AppVersion   = "0.42-beta-hotfix2"
	DefaultTheme = "yalie-blue"

	// Form & Post Limits
	MaxNameLen    = 75
	MaxSubjectLen = 100
	MaxCommentLen = 8000

	// File Upload Limits
	MaxFileSize = 15 * 1024 * 1024 // 15MB
	MaxWidth    = 8000
	MaxHeight   = 8000
)