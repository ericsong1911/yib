// yib/database/migrations.go
package database

// migration represents a single database schema migration.
type migration struct {
	Version uint
	Query   string
}

// allMigrations holds all schema changes in order.
var allMigrations = []migration{
	{
		Version: 1,
		Query: `
-- Add thumbnail path and moderator status to posts table
ALTER TABLE posts ADD COLUMN thumbnail_path TEXT;
ALTER TABLE posts ADD COLUMN is_moderator BOOLEAN DEFAULT 0;

-- Add indexes for the new column and a commonly queried one
CREATE INDEX IF NOT EXISTS idx_posts_is_moderator ON posts(is_moderator);
CREATE INDEX IF NOT EXISTS idx_threads_board_id ON threads(board_id);
		`,
	},
	// Future migrations will be added here, e.g.:
	// {
	// 	Version: 2,
	// 	Query: `ALTER TABLE boards ADD COLUMN new_feature_flag BOOLEAN DEFAULT 0;`,
	// },
}
