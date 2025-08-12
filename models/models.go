// yib/models/models.go
package models

import (
	"database/sql"
	"time"
	// The "yib/database" import is now removed
)

// The Application struct has been moved to main.go

// --- Core Data Models ---

type BoardConfig struct {
	ID            string
	Name          string
	Description   string
	MaxThreads    int
	BumpLimit     int
	ImageRequired bool
	Password      string
	RequirePass   bool
	ColorScheme   string
	Created       time.Time
	Archived      bool
	CategoryID    int
	SortOrder     int
}

type Page struct {
	Number     int
	IsCurrent  bool
	IsEllipsis bool
}

type FormInput struct {
	Name    string
	Subject string
	Content string
}

type Post struct {
	ID        int64
	BoardID   string
	ThreadID  int64
	IsOp      bool
	Name      string
	Tripcode  string
	Subject   string
	Content   string
	ImagePath string
	ImageHash string
	Timestamp time.Time
	IPHash    string
	CookieHash string
	Backlinks []int64
}

type Thread struct {
	ID         int64
	BoardID    string
	Subject    string
	Bump       time.Time
	ReplyCount int
	ImageCount int
	Locked     bool
	Sticky     bool
	Archived   bool
	Posts      []Post
	Op         Post
}

type Category struct {
	ID        int
	Name      string
	SortOrder int
	Boards    []BoardEntry
}

type BoardEntry struct {
	ID          string
	Name        string
	Description string
	IsLocked    bool
}

// --- Moderation & System Models ---

type Ban struct {
	ID        int64
	Hash      string
	BanType   string
	Reason    string
	CreatedAt time.Time
	ExpiresAt sql.NullTime
}

type Report struct {
	ID        int64
	PostID    int64
	Reason    string
	IPHash    string
	CreatedAt time.Time
	Post      Post // Include post info for the template
}

type ModAction struct {
	ID            int64
	Timestamp     time.Time
	ModeratorHash string
	Action        string
	TargetID      sql.NullInt64
	Details       sql.NullString
}
