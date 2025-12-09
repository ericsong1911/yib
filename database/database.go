// yib/database/database.go
package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"yib/models"
	"yib/utils"

	_ "github.com/mattn/go-sqlite3"
)

// DatabaseService is the central struct for all database operations.
type DatabaseService struct {
	DB         *sql.DB
	logger     *slog.Logger
	dsn        string
	boardCache map[string]*models.BoardConfig
	cacheMu    sync.RWMutex
}

// InitDB connects to the database, runs migrations, and seeds default data.
func InitDB(dataSourceName string, logger *slog.Logger) (*DatabaseService, error) {
	db, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, err
	}

	// Run the base schema to ensure all tables exist.
	if _, err = db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to execute base schema: %w", err)
	}

	// Run versioned migrations
	if err := runMigrations(db, logger); err != nil {
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	// Seed database if empty
	var categoryCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM categories").Scan(&categoryCount); err == nil && categoryCount == 0 {
		if _, err := db.Exec("INSERT INTO categories (id, name) VALUES (1, 'General')"); err != nil {
			return nil, fmt.Errorf("failed to seed categories: %w", err)
		}
	}
	var boardCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM boards").Scan(&boardCount); err == nil && boardCount == 0 {
		_, err = db.Exec("INSERT INTO boards (id, name, description, color_scheme, created) VALUES ('b', 'Random', 'The anything-goes board.', 'yalie-blue', ?)", time.Now())
		if err != nil {
			return nil, fmt.Errorf("failed to seed boards: %w", err)
		}
	}

	// One-time migration for FTS data if the table is empty but posts exist
	var ftsCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM posts_fts").Scan(&ftsCount); err == nil && ftsCount == 0 {
		var postCount int
		if err := db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&postCount); err == nil && postCount > 0 {
			logger.Info("FTS table is empty, performing one-time data migration for existing posts...")
			_, err := db.Exec(`INSERT INTO posts_fts(rowid, subject, content) SELECT p.id, t.subject, p.content FROM posts p JOIN threads t ON p.thread_id = t.id;`)
			if err != nil {
				logger.Error("CRITICAL: Failed to migrate existing posts to FTS table", "error", err)
			} else {
				logger.Info("FTS data migration complete.")
			}
		}
	}

	logger.Info("Database initialized and cache ready.")

	return &DatabaseService{
		DB:         db,
		logger:     logger,
		dsn:        dataSourceName, // Store the DSN
		boardCache: make(map[string]*models.BoardConfig),
	}, nil
}

// BackupDatabase performs an online backup of the live SQLite database using VACUUM INTO.
func (ds *DatabaseService) BackupDatabase() (string, error) {
	if utils.BackupDir == "" {
		return "", fmt.Errorf("backup directory is not configured")
	}
	if err := os.MkdirAll(utils.BackupDir, 0755); err != nil {
		return "", fmt.Errorf("could not create backup directory %s: %w", utils.BackupDir, err)
	}

	timestamp := time.Now().UTC().Format("2006-01-02_15-04-05")
	backupFilename := fmt.Sprintf("yib_backup_%s.db", timestamp)
	backupPath := filepath.Join(utils.BackupDir, backupFilename)

	ds.logger.Info("Starting database backup", "destination", backupPath)

	_, err := ds.DB.Exec("VACUUM INTO ?", backupPath)
	if err != nil {
		// If backup fails, attempt to remove the potentially incomplete file
		if removeErr := os.Remove(backupPath); removeErr != nil && !os.IsNotExist(removeErr) {
			ds.logger.Error("Failed to remove incomplete backup file", "path", backupPath, "error", removeErr)
		}
		return "", fmt.Errorf("VACUUM INTO command failed: %w", err)
	}

	return backupPath, nil
}

// runMigrations applies all un-applied migrations.
func runMigrations(db *sql.DB, logger *slog.Logger) error {
	var latestVersion uint
	err := db.QueryRow("SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&latestVersion)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("could not get db version: %w", err)
	}

	logger.Info("Current database schema version", "version", latestVersion)

	for _, m := range allMigrations {
		if m.Version > latestVersion {
			logger.Info("Applying migration", "version", m.Version)
			tx, err := db.Begin()
			if err != nil {
				return err
			}

			if _, err := tx.Exec(m.Query); err != nil {
				if rerr := tx.Rollback(); rerr != nil {
					logger.Error("Failed to rollback migration", "version", m.Version, "error", rerr)
				}
				return fmt.Errorf("failed to apply migration v%d: %w", m.Version, err)
			}
			if _, err := tx.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.Version, utils.GetSQLTime()); err != nil {
				if rerr := tx.Rollback(); rerr != nil {
					logger.Error("Failed to rollback migration record", "version", m.Version, "error", rerr)
				}
				return fmt.Errorf("failed to record migration v%d: %w", m.Version, err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit migration v%d: %w", m.Version, err)
			}
			logger.Info("Successfully applied migration", "version", m.Version)
		}
	}
	return nil
}

// GetBoard fetches board configuration, using the instance's cache.
func (ds *DatabaseService) GetBoard(boardID string) (*models.BoardConfig, error) {
	ds.cacheMu.RLock()
	config, ok := ds.boardCache[boardID]
	ds.cacheMu.RUnlock()
	if ok {
		return config, nil
	}

	var board models.BoardConfig
	err := ds.DB.QueryRow("SELECT id, name, description, max_threads, bump_limit, image_required, password, require_pass, color_scheme, created, archived, category_id, sort_order FROM boards WHERE id = ?", boardID).Scan(
		&board.ID, &board.Name, &board.Description, &board.MaxThreads, &board.BumpLimit,
		&board.ImageRequired, &board.Password, &board.RequirePass, &board.ColorScheme,
		&board.Created, &board.Archived, &board.CategoryID, &board.SortOrder,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("board '%s' not found", boardID)
		}
		return nil, fmt.Errorf("db error getting board '%s': %w", boardID, err)
	}

	ds.cacheMu.Lock()
	ds.boardCache[boardID] = &board
	ds.cacheMu.Unlock()
	return &board, nil
}

// GetThreadCount returns the total number of active or archived threads on a board.
func (ds *DatabaseService) GetThreadCount(boardID string, archived bool) (int, error) {
	var count int
	err := ds.DB.QueryRow("SELECT COUNT(*) FROM threads WHERE board_id = ? AND archived = ?", boardID, archived).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetThreadsForBoard retrieves a paginated list of threads for a board page.
func (ds *DatabaseService) GetThreadsForBoard(boardID string, archived bool, page, pageSize int, fetchReplies bool, dailySalt string) ([]models.Thread, error) {
	offset := (page - 1) * pageSize
	// Select new thumbnail and moderator columns
	rows, err := ds.DB.Query(`
        SELECT t.id, t.subject, t.bump, t.reply_count, t.image_count, t.locked, t.sticky,
               p.id, p.name, p.tripcode, p.content, p.image_path, p.thumbnail_path, p.timestamp, p.ip_hash, p.cookie_hash, p.is_moderator
        FROM threads t
        JOIN posts p ON t.id = p.thread_id AND p.id = (SELECT MIN(id) FROM posts WHERE thread_id = t.id)
        WHERE t.board_id = ? AND t.archived = ?
        ORDER BY t.sticky DESC, t.bump DESC
        LIMIT ? OFFSET ?`, boardID, archived, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ds.logger.Error("Failed to close rows in GetThreadsForBoard", "error", err)
		}
	}()

	var threads []models.Thread
	for rows.Next() {
		var t models.Thread
		t.BoardID = boardID
		var op models.Post
		// Scan new columns
		if err := rows.Scan(&t.ID, &t.Subject, &t.Bump, &t.ReplyCount, &t.ImageCount, &t.Locked, &t.Sticky,
			&op.ID, &op.Name, &op.Tripcode, &op.Content, &op.ImagePath, &op.ThumbnailPath, &op.Timestamp, &op.IPHash, &op.CookieHash, &op.IsModerator); err != nil {
			ds.logger.Error("Failed to scan thread row", "error", err)
			continue
		}
		op.IsOp, op.BoardID, op.ThreadID, op.Subject = true, boardID, t.ID, t.Subject
		op.ThreadUserID = utils.GenerateThreadUserID(op.IPHash, op.ThreadID, dailySalt)
		t.Op = op
		threads = append(threads, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(threads) == 0 {
		return threads, nil
	}

	threadMap := make(map[int64]*models.Thread, len(threads))
	postMap := make(map[int64]*models.Post, len(threads))
	for i := range threads {
		threadMap[threads[i].ID] = &threads[i]
		postMap[threads[i].Op.ID] = &threads[i].Op
	}

	if fetchReplies {
		ds.fetchAndAssignReplies(threadMap, postMap, dailySalt)
	}

	allPostIDs := make([]interface{}, 0, len(postMap))
	for id := range postMap {
		allPostIDs = append(allPostIDs, id)
	}

	ds.fetchAndAssignBacklinks(allPostIDs, func(targetID, backlinkID int64) {
		if post, ok := postMap[targetID]; ok {
			post.Backlinks = append(post.Backlinks, backlinkID)
		}
	})

	return threads, nil
}

// GetPostsForThread fetches a single thread and all its posts.
func (ds *DatabaseService) GetPostsForThread(threadID int64, dailySalt string) ([]models.Post, error) {
	// Select new columns
	rows, err := ds.DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, image_path, thumbnail_path, timestamp, ip_hash, cookie_hash, is_moderator FROM posts WHERE thread_id = ? ORDER BY id ASC", threadID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ds.logger.Error("Failed to close rows in GetPostsForThread", "error", err)
		}
	}()

	var posts []models.Post
	for rows.Next() {
		var p models.Post
		// Scan new columns
		if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.ImagePath, &p.ThumbnailPath, &p.Timestamp, &p.IPHash, &p.CookieHash, &p.IsModerator); err != nil {
			ds.logger.Error("Failed to scan post row", "error", err)
			continue
		}
		p.ThreadUserID = utils.GenerateThreadUserID(p.IPHash, p.ThreadID, dailySalt)
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return posts, nil
	}

	postMap := make(map[int64]*models.Post, len(posts))
	postIDs := make([]interface{}, 0, len(posts))
	for i := range posts {
		postMap[posts[i].ID] = &posts[i]
		postIDs = append(postIDs, posts[i].ID)
	}

	posts[0].IsOp = true

	ds.fetchAndAssignBacklinks(postIDs, func(targetID, backlinkID int64) {
		if post, ok := postMap[targetID]; ok {
			post.Backlinks = append(post.Backlinks, backlinkID)
		}
	})

	return posts, nil
}

// GetPostByID fetches a single post by its ID. Used for previews.
func (ds *DatabaseService) GetPostByID(postID int64, dailySalt string) (*models.Post, error) {
	var p models.Post
	var subject sql.NullString
	// Select new columns
	err := ds.DB.QueryRow(`
		SELECT p.id, p.board_id, p.thread_id, p.name, p.tripcode, p.content, p.image_path, p.thumbnail_path, p.timestamp, p.ip_hash, p.cookie_hash, p.is_moderator,
		       t.subject,
		       (SELECT MIN(id) FROM posts WHERE thread_id = p.thread_id) = p.id as is_op
		FROM posts p JOIN threads t ON p.thread_id = t.id
		WHERE p.id = ?`, postID).Scan(
		&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content,
		&p.ImagePath, &p.ThumbnailPath, &p.Timestamp, &p.IPHash, &p.CookieHash, &p.IsModerator, &subject, &p.IsOp,
	)
	if err != nil {
		return nil, err
	}
	if p.IsOp && subject.Valid {
		p.Subject = subject.String
	}
	p.ThreadUserID = utils.GenerateThreadUserID(p.IPHash, p.ThreadID, dailySalt)

	postIDs := []interface{}{p.ID}
	postMap := map[int64]*models.Post{p.ID: &p}
	ds.fetchAndAssignBacklinks(postIDs, func(targetID, backlinkID int64) {
		if post, ok := postMap[targetID]; ok {
			post.Backlinks = append(post.Backlinks, backlinkID)
		}
	})

	return &p, nil
}

// GetBanDetails checks if a user is banned.
func (ds *DatabaseService) GetBanDetails(ip, ipHash, cookieHash string) (models.Ban, bool) {
	var ban models.Ban
	err := ds.DB.QueryRow(`
		SELECT reason, expires_at, ban_type FROM bans
		WHERE (expires_at IS NULL OR expires_at > ?)
		AND ((hash = ? AND ban_type = 'ip') OR (hash = ? AND ban_type = 'cookie'))
		ORDER BY created_at DESC LIMIT 1`,
		utils.GetSQLTime(), ipHash, cookieHash).Scan(&ban.Reason, &ban.ExpiresAt, &ban.BanType)

	if err == nil {
		return ban, true
	}
	if err != sql.ErrNoRows {
		ds.logger.Error("Failed to query for ban details", "error", err)
	}

	// Check CIDR bans
	cidrRows, err := ds.DB.Query("SELECT hash, reason, expires_at FROM bans WHERE ban_type = 'cidr' AND (expires_at IS NULL OR expires_at > ?)", utils.GetSQLTime())
	if err != nil {
		ds.logger.Error("Failed to query CIDR bans", "error", err)
		return ban, false
	}
	defer cidrRows.Close()

	userIP := net.ParseIP(ip)
	if userIP == nil {
		return ban, false
	}

	for cidrRows.Next() {
		var cidrStr string
		if err := cidrRows.Scan(&cidrStr, &ban.Reason, &ban.ExpiresAt); err == nil {
			_, subnet, err := net.ParseCIDR(cidrStr)
			if err == nil && subnet.Contains(userIP) {
				ban.BanType = "cidr" // Explicitly set type for CIDR match
				return ban, true
			}
		}
	}

	return ban, false
}

// DeletePost handles the logic of deleting a post or an entire thread.
func (ds *DatabaseService) DeletePost(postID int64, uploadDir string, modHash, details string) (boardID string, isOp bool, err error) {
	tx, err := ds.DB.Begin()
	if err != nil {
		return "", false, err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			ds.logger.Error("Failed to rollback transaction in DeletePost", "error", rerr)
		}
	}()

	var imagePath, thumbnailPath, imageHash sql.NullString
	var threadID int64
	// Select thumbnail path as well
	err = tx.QueryRow(`SELECT p.board_id, p.thread_id, p.image_path, p.thumbnail_path, p.image_hash, (SELECT id FROM posts WHERE thread_id = p.thread_id ORDER BY id ASC LIMIT 1) = p.id as is_op FROM posts p WHERE id = ?`, postID).Scan(&boardID, &threadID, &imagePath, &thumbnailPath, &imageHash, &isOp)
	if err != nil {
		return "", false, fmt.Errorf("post not found: %w", err)
	}

	type fileToDelete struct{ Path, Hash string }
	filesToCheck := make(map[string]fileToDelete)

	if isOp {
		// Get both image and thumbnail paths for all posts in the thread
		rows, err := tx.Query("SELECT image_path, thumbnail_path, image_hash FROM posts WHERE thread_id = ? AND image_path IS NOT NULL AND image_path != ''", threadID)
		if err != nil {
			return "", false, fmt.Errorf("failed to query images for thread deletion: %w", err)
		}
		for rows.Next() {
			var p, t, h sql.NullString
			if err := rows.Scan(&p, &t, &h); err == nil {
				if p.Valid {
					filesToCheck[p.String] = fileToDelete{Path: p.String, Hash: h.String}
				}
				if t.Valid {
					filesToCheck[t.String] = fileToDelete{Path: t.String, Hash: h.String}
				}
			}
		}
		if err := rows.Close(); err != nil {
			ds.logger.Warn("Failed to close rows for thread images", "error", err)
		}
		if _, err := tx.Exec("DELETE FROM threads WHERE id = ?", threadID); err != nil {
			return "", false, fmt.Errorf("failed to delete thread: %w", err)
		}
	} else {
		if imagePath.Valid && imageHash.Valid {
			filesToCheck[imagePath.String] = fileToDelete{Path: imagePath.String, Hash: imageHash.String}
			if thumbnailPath.Valid {
				filesToCheck[thumbnailPath.String] = fileToDelete{Path: thumbnailPath.String, Hash: imageHash.String}
			}
		}
		if _, err := tx.Exec("DELETE FROM posts WHERE id = ?", postID); err != nil {
			return "", false, fmt.Errorf("failed to delete reply post: %w", err)
		}
		if _, err := tx.Exec("UPDATE threads SET reply_count = reply_count - 1 WHERE id = ?", threadID); err != nil {
			return "", false, fmt.Errorf("failed to update reply count: %w", err)
		}
		if imagePath.Valid {
			if _, err := tx.Exec("UPDATE threads SET image_count = image_count - 1 WHERE id = ?", threadID); err != nil {
				return "", false, fmt.Errorf("failed to update image count: %w", err)
			}
		}
	}
	if _, err := tx.Exec("DELETE FROM reports WHERE post_id = ?", postID); err != nil {
		return "", false, fmt.Errorf("failed to delete associated reports: %w", err)
	}

	if modHash != "" {
		action := "delete_reply"
		if isOp {
			action = "delete_thread"
		}
		if err := LogModAction(tx, modHash, action, postID, details); err != nil {
			return "", false, err // Rollback
		}
	}

	for _, file := range filesToCheck {
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM posts WHERE image_hash = ?", file.Hash).Scan(&count); err != nil {
			ds.logger.Warn("Failed to check for duplicate images", "hash", file.Hash, "error", err)
			continue
		}
		if count == 0 {
			filePath := filepath.Join(uploadDir, filepath.Base(file.Path))
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				ds.logger.Warn("Failed to remove image file", "path", filePath, "error", err)
			}
		}
	}
	return boardID, isOp, tx.Commit()
}

// DeletePostsByHash deletes all posts matching a specific IP or Cookie hash.
func (ds *DatabaseService) DeletePostsByHash(hash, hashType, uploadDir, modHash string) (int64, error) {
	tx, err := ds.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			ds.logger.Error("Failed to rollback transaction in DeletePostsByHash", "error", rerr)
		}
	}()

	var column string
	if hashType == "ip" {
		column = "ip_hash"
	} else if hashType == "cookie" {
		column = "cookie_hash"
	} else {
		return 0, fmt.Errorf("invalid hash type: %s", hashType)
	}

	rows, err := tx.Query(fmt.Sprintf("SELECT id FROM posts WHERE %s = ?", column), hash)
	if err != nil {
		return 0, err
	}

	var postIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			postIDs = append(postIDs, id)
		}
	}
	rows.Close()

	if len(postIDs) == 0 {
		return 0, nil
	}

	deletedCount := int64(0)
	for _, postID := range postIDs {
		// Reuse logic similar to DeletePost but within this transaction.

		var boardID string
		var threadID int64
		var imagePath, thumbnailPath, imageHash sql.NullString
		var isOp bool

		err := tx.QueryRow(`SELECT board_id, thread_id, image_path, thumbnail_path, image_hash, (SELECT id FROM posts WHERE thread_id = p.thread_id ORDER BY id ASC LIMIT 1) = p.id as is_op FROM posts p WHERE id = ?`, postID).Scan(&boardID, &threadID, &imagePath, &thumbnailPath, &imageHash, &isOp)
		if err != nil {
			if err == sql.ErrNoRows {
				continue // Already deleted
			}
			return 0, err
		}

		type fileToDelete struct{ Path, Hash string }
		filesToCheck := make(map[string]fileToDelete)

		if isOp {
			rows, err := tx.Query("SELECT image_path, thumbnail_path, image_hash FROM posts WHERE thread_id = ? AND image_path IS NOT NULL AND image_path != ''", threadID)
			if err != nil {
				return 0, err
			}
			for rows.Next() {
				var p, t, h sql.NullString
				if err := rows.Scan(&p, &t, &h); err == nil {
					if p.Valid {
						filesToCheck[p.String] = fileToDelete{Path: p.String, Hash: h.String}
					}
					if t.Valid {
						filesToCheck[t.String] = fileToDelete{Path: t.String, Hash: h.String}
					}
				}
			}
			rows.Close()
			if _, err := tx.Exec("DELETE FROM threads WHERE id = ?", threadID); err != nil {
				return 0, err
			}
		} else {
			if imagePath.Valid && imageHash.Valid {
				filesToCheck[imagePath.String] = fileToDelete{Path: imagePath.String, Hash: imageHash.String}
				if thumbnailPath.Valid {
					filesToCheck[thumbnailPath.String] = fileToDelete{Path: thumbnailPath.String, Hash: imageHash.String}
				}
			}
			if _, err := tx.Exec("DELETE FROM posts WHERE id = ?", postID); err != nil {
				return 0, err
			}
			// Ideally update thread counts, but for mass nuke it might be acceptable to be slightly off or fix later.
			// However, to be correct:
			if _, err := tx.Exec("UPDATE threads SET reply_count = reply_count - 1 WHERE id = ?", threadID); err != nil {
				// Ignore error if thread deleted? No, thread should exist if post existed as reply.
			}
			if imagePath.Valid {
				if _, err := tx.Exec("UPDATE threads SET image_count = image_count - 1 WHERE id = ?", threadID); err != nil {
					// same
				}
			}
		}

		// Delete files
		for _, file := range filesToCheck {
			var count int
			// Check if other posts use this hash (in case of duplicate images across threads/boards)
			// Note: We are in a transaction, so we should check against the DB state.
			// But since we just deleted the post, the count should reflect that.
			if err := tx.QueryRow("SELECT COUNT(*) FROM posts WHERE image_hash = ?", file.Hash).Scan(&count); err == nil && count == 0 {
				filePath := filepath.Join(uploadDir, filepath.Base(file.Path))
				os.Remove(filePath)
			}
		}

		deletedCount++
	}

	// Cleanup orphaned reports
	// In DeletePost we do: DELETE FROM reports WHERE post_id = ?
	// Since we deleted posts, reports referencing them should be removed.
	// We can do a cleanup query:
	if _, err := tx.Exec("DELETE FROM reports WHERE post_id NOT IN (SELECT id FROM posts)"); err != nil {
		ds.logger.Warn("Failed to cleanup reports during mass delete", "error", err)
	}

	if err := LogModAction(tx, modHash, "mass_delete", 0, fmt.Sprintf("Deleted %d posts for %s hash %s", deletedCount, hashType, hash)); err != nil {
		return 0, err
	}

	return deletedCount, tx.Commit()
}

// SearchPosts performs a full-text search on posts using FTS5.
func (ds *DatabaseService) DeleteBoard(boardID, uploadDir, modHash string) error {
	tx, err := ds.DB.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			ds.logger.Error("Failed to rollback transaction in DeleteBoard", "error", rerr)
		}
	}()

	// Select both image and thumbnail paths for deletion
	rows, err := tx.Query("SELECT image_path, thumbnail_path FROM posts WHERE board_id = ?", boardID)
	if err != nil {
		return fmt.Errorf("failed to query image paths for board deletion: %w", err)
	}

	for rows.Next() {
		var imgPath, thumbPath sql.NullString
		if err := rows.Scan(&imgPath, &thumbPath); err == nil {
			if imgPath.Valid && imgPath.String != "" {
				filePath := filepath.Join(uploadDir, filepath.Base(imgPath.String))
				if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
					ds.logger.Warn("Failed to remove image file during board deletion", "path", filePath, "error", err)
				}
			}
			if thumbPath.Valid && thumbPath.String != "" {
				filePath := filepath.Join(uploadDir, filepath.Base(thumbPath.String))
				if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
					ds.logger.Warn("Failed to remove thumbnail file during board deletion", "path", filePath, "error", err)
				}
			}
		}
	}
	if err := rows.Close(); err != nil {
		ds.logger.Warn("Failed to close rows for board images", "error", err)
	}

	if _, err := tx.Exec("DELETE FROM boards WHERE id = ?", boardID); err != nil {
		return fmt.Errorf("failed to delete board record: %w", err)
	}

	if err := LogModAction(tx, modHash, "delete_board", 0, boardID); err != nil {
		return err
	}

	return tx.Commit()
}

// SearchPosts performs a full-text search on posts using FTS5.
func (ds *DatabaseService) SearchPosts(query, boardID string) ([]models.Post, error) {
	var posts []models.Post

	// Build query using FTS5
	sql := `
		SELECT p.id, p.board_id, p.thread_id, p.name, p.tripcode, p.content, p.timestamp, t.subject,
			   (SELECT MIN(id) FROM posts WHERE thread_id = p.thread_id) = p.id as is_op
		FROM posts p
		JOIN posts_fts fts ON p.id = fts.rowid
		JOIN threads t ON p.thread_id = t.id
		WHERE posts_fts MATCH ?`

	args := []interface{}{query}

	if boardID != "" {
		sql += " AND p.board_id = ?"
		args = append(args, boardID)
	}
	sql += " ORDER BY p.id DESC LIMIT 50"

	rows, err := ds.DB.Query(sql, args...)
	if err != nil {
		ds.logger.Error("FTS Search failed", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ds.logger.Error("Failed to close rows in SearchPosts", "error", err)
		}
	}()

	for rows.Next() {
		var p models.Post
		if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.Subject, &p.IsOp); err != nil {
			ds.logger.Error("Failed to scan post during search", "error", err)
			continue
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return posts, nil
}

// LogModAction records a moderator's action to the database.
func LogModAction(tx *sql.Tx, modHash, action string, targetID int64, details string) error {
	stmt, err := tx.Prepare("INSERT INTO mod_actions (timestamp, moderator_hash, action, target_id, details) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare mod action statement: %w", err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			slog.Default().Error("Failed to close statement in LogModAction", "error", err)
		}
	}()

	_, err = stmt.Exec(utils.GetSQLTime(), modHash, action, targetID, details)
	if err != nil {
		return fmt.Errorf("failed to execute mod action log: %w", err)
	}
	return nil
}

// CreateBan inserts a new ban record.
func (ds *DatabaseService) CreateBan(hash, banType, reason, modHash string, expiresAt sql.NullTime) error {
	tx, err := ds.DB.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			ds.logger.Error("Failed to rollback transaction in CreateBan", "error", rerr)
		}
	}()

	_, err = tx.Exec(`INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(hash, ban_type) DO UPDATE SET reason=excluded.reason, expires_at=excluded.expires_at`,
		hash, banType, reason, utils.GetSQLTime(), expiresAt)
	if err != nil {
		return fmt.Errorf("failed to insert ban: %w", err)
	}

	if modHash != "" {
		if err := LogModAction(tx, modHash, "apply_ban", 0, fmt.Sprintf("%s Hash: %s, Reason: %s", strings.ToUpper(banType), hash, reason)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// IsHashBanned checks if a specific hash is explicitly banned.
func (ds *DatabaseService) IsHashBanned(hash, banType string) (bool, error) {
	var count int
	err := ds.DB.QueryRow("SELECT COUNT(*) FROM bans WHERE hash = ? AND ban_type = ? AND (expires_at IS NULL OR expires_at > ?)", hash, banType, utils.GetSQLTime()).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// --- Cache Management ---
func (ds *DatabaseService) ClearBoardCache(boardID string) {
	ds.cacheMu.Lock()
	delete(ds.boardCache, boardID)
	ds.cacheMu.Unlock()
}
func (ds *DatabaseService) ClearAllBoardCaches() {
	ds.cacheMu.Lock()
	ds.boardCache = make(map[string]*models.BoardConfig)
	ds.cacheMu.Unlock()
}

// --- Internal Helpers ---
func (ds *DatabaseService) fetchAndAssignReplies(threadMap map[int64]*models.Thread, postMap map[int64]*models.Post, dailySalt string) {
	if len(threadMap) == 0 {
		return
	}
	threadIDs := make([]interface{}, 0, len(threadMap))
	for id := range threadMap {
		threadIDs = append(threadIDs, id)
	}

	// Select new columns
	query := `
        WITH RankedReplies AS (
            SELECT p.*, ROW_NUMBER() OVER(PARTITION BY p.thread_id ORDER BY p.id DESC) as rn
            FROM posts p WHERE p.thread_id IN (?` + strings.Repeat(",?", len(threadIDs)-1) + `) AND p.id != (SELECT MIN(id) FROM posts WHERE thread_id=p.thread_id)
        )
        SELECT id, board_id, thread_id, name, tripcode, content, image_path, thumbnail_path, timestamp, ip_hash, cookie_hash, is_moderator
        FROM RankedReplies WHERE rn <= 3 ORDER BY thread_id, id ASC`
	replyRows, err := ds.DB.Query(query, threadIDs...)
	if err != nil {
		ds.logger.Error("Failed to fetch replies for board view", "error", err)
		return
	}
	defer func() {
		if err := replyRows.Close(); err != nil {
			ds.logger.Error("Failed to close rows in fetchAndAssignReplies", "error", err)
		}
	}()

	for replyRows.Next() {
		var p models.Post
		// Scan new columns
		if err := replyRows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.ImagePath, &p.ThumbnailPath, &p.Timestamp, &p.IPHash, &p.CookieHash, &p.IsModerator); err != nil {
			ds.logger.Error("Failed to scan reply post", "error", err)
			continue
		}
		p.ThreadUserID = utils.GenerateThreadUserID(p.IPHash, p.ThreadID, dailySalt)
		if thread, ok := threadMap[p.ThreadID]; ok {
			thread.Posts = append(thread.Posts, p)
			postMap[p.ID] = &thread.Posts[len(thread.Posts)-1]
		}
	}
	if err := replyRows.Err(); err != nil {
		ds.logger.Error("Row error during reply scan", "error", err)
	}
}
func (ds *DatabaseService) fetchAndAssignBacklinks(postIDs []interface{}, assignFunc func(targetID, backlinkID int64)) {
	if len(postIDs) == 0 {
		return
	}
	query := "SELECT target_post_id, source_post_id FROM backlinks WHERE target_post_id IN (?" + strings.Repeat(",?", len(postIDs)-1) + ")"
	rows, err := ds.DB.Query(query, postIDs...)
	if err != nil {
		ds.logger.Error("Failed to query backlinks", "error", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ds.logger.Error("Failed to close rows in fetchAndAssignBacklinks", "error", err)
		}
	}()
	for rows.Next() {
		var sourceID, targetID int64
		if err := rows.Scan(&targetID, &sourceID); err == nil {
			assignFunc(targetID, sourceID)
		} else {
			ds.logger.Error("Failed to scan backlink row", "error", err)
		}
	}
	if err := rows.Err(); err != nil {
		ds.logger.Error("Row error during backlink scan", "error", err)
	}
}
