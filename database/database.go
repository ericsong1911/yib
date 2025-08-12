// yib/database/database.go

package database

import (
	"database/sql"
	"fmt"
	"log"
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
	boardCache map[string]*models.BoardConfig
	cacheMu    sync.RWMutex
}

// InitDB connects to the database, executes the schema, and seeds default data.
func InitDB(dataSourceName string) (*DatabaseService, error) {
	db, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to execute schema: %w", err)
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
			log.Println("FTS table is empty, performing one-time data migration for existing posts...")
			_, err := db.Exec(`INSERT INTO posts_fts(rowid, subject, content) SELECT p.id, t.subject, p.content FROM posts p JOIN threads t ON p.thread_id = t.id;`)
			if err != nil {
				log.Printf("CRITICAL: Failed to migrate existing posts to FTS table: %v", err)
			} else {
				log.Println("FTS data migration complete.")
			}
		}
	}

	log.Println("Database initialized and cache ready.")

	return &DatabaseService{
		DB:         db,
		boardCache: make(map[string]*models.BoardConfig),
	}, nil
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
func (ds *DatabaseService) GetThreadsForBoard(boardID string, archived bool, page, pageSize int, fetchReplies bool) ([]models.Thread, error) {
	offset := (page - 1) * pageSize
	rows, err := ds.DB.Query(`
        SELECT t.id, t.subject, t.bump, t.reply_count, t.image_count, t.locked, t.sticky,
               p.id, p.name, p.tripcode, p.content, p.image_path, p.timestamp, p.ip_hash, p.cookie_hash
        FROM threads t
        JOIN posts p ON t.id = p.thread_id AND p.id = (SELECT MIN(id) FROM posts WHERE thread_id = t.id)
        WHERE t.board_id = ? AND t.archived = ?
        ORDER BY t.sticky DESC, t.bump DESC
        LIMIT ? OFFSET ?`, boardID, archived, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []models.Thread
	for rows.Next() {
		var t models.Thread
		t.BoardID = boardID
		var op models.Post
		if err := rows.Scan(&t.ID, &t.Subject, &t.Bump, &t.ReplyCount, &t.ImageCount, &t.Locked, &t.Sticky,
			&op.ID, &op.Name, &op.Tripcode, &op.Content, &op.ImagePath, &op.Timestamp, &op.IPHash, &op.CookieHash); err != nil {
			log.Printf("ERROR: Failed to scan thread row: %v", err)
			continue
		}
		op.IsOp, op.BoardID, op.ThreadID, op.Subject = true, boardID, t.ID, t.Subject
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
		ds.fetchAndAssignReplies(threadMap, postMap)
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
func (ds *DatabaseService) GetPostsForThread(threadID int64) ([]models.Post, error) {
	rows, err := ds.DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, image_path, timestamp, ip_hash, cookie_hash FROM posts WHERE thread_id = ? ORDER BY id ASC", threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []models.Post
	for rows.Next() {
		var p models.Post
		if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.ImagePath, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
			log.Printf("ERROR: Failed to scan post row: %v", err)
			continue
		}
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
func (ds *DatabaseService) GetPostByID(postID int64) (*models.Post, error) {
	var p models.Post
	var subject sql.NullString
	err := ds.DB.QueryRow(`
		SELECT p.id, p.board_id, p.thread_id, p.name, p.tripcode, p.content, p.image_path, p.timestamp, p.ip_hash, p.cookie_hash,
		       t.subject,
		       (SELECT MIN(id) FROM posts WHERE thread_id = p.thread_id) = p.id as is_op
		FROM posts p JOIN threads t ON p.thread_id = t.id
		WHERE p.id = ?`, postID).Scan(
		&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content,
		&p.ImagePath, &p.Timestamp, &p.IPHash, &p.CookieHash, &subject, &p.IsOp,
	)
	if err != nil {
		return nil, err
	}
	if p.IsOp && subject.Valid {
		p.Subject = subject.String
	}

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
func (ds *DatabaseService) GetBanDetails(ipHash, cookieHash string) (models.Ban, bool) {
	var ban models.Ban
	err := ds.DB.QueryRow(`
		SELECT reason, expires_at FROM bans
		WHERE (expires_at IS NULL OR expires_at > ?)
		AND ((hash = ? AND ban_type = 'ip') OR (hash = ? AND ban_type = 'cookie'))
		ORDER BY created_at DESC LIMIT 1`,
		utils.GetSQLTime(), ipHash, cookieHash).Scan(&ban.Reason, &ban.ExpiresAt)

	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("ERROR: Failed to query for ban details: %v", err)
		}
		return ban, false
	}
	return ban, true
}

// DeletePost handles the logic of deleting a post or an entire thread.
func (ds *DatabaseService) DeletePost(postID int64, uploadDir string, modHash, details string) (boardID string, isOp bool, err error) {
	tx, err := ds.DB.Begin()
	if err != nil {
		return "", false, err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			log.Printf("ERROR: Failed to rollback transaction in DeletePost: %v", rerr)
		}
	}()

	var imagePath, imageHash sql.NullString
	var threadID int64
	err = tx.QueryRow(`SELECT p.board_id, p.thread_id, p.image_path, p.image_hash, (SELECT id FROM posts WHERE thread_id = p.thread_id ORDER BY id ASC LIMIT 1) = p.id as is_op FROM posts p WHERE id = ?`, postID).Scan(&boardID, &threadID, &imagePath, &imageHash, &isOp)
	if err != nil {
		return "", false, fmt.Errorf("post not found: %w", err)
	}

	imagesToCheck := make(map[string]string)
	if isOp {
		rows, err := tx.Query("SELECT image_path, image_hash FROM posts WHERE thread_id = ? AND image_path IS NOT NULL AND image_path != ''", threadID)
		if err != nil {
			return "", false, fmt.Errorf("failed to query images for thread deletion: %w", err)
		}
		for rows.Next() {
			var p, h string
			if err := rows.Scan(&p, &h); err == nil {
				imagesToCheck[p] = h
			}
		}
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: Failed to close rows for thread images: %v", err)
		}
		if _, err := tx.Exec("DELETE FROM threads WHERE id = ?", threadID); err != nil {
			return "", false, fmt.Errorf("failed to delete thread: %w", err)
		}
	} else {
		if imagePath.Valid && imageHash.Valid {
			imagesToCheck[imagePath.String] = imageHash.String
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

	for path, hash := range imagesToCheck {
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM posts WHERE image_hash = ?", hash).Scan(&count); err != nil {
			log.Printf("WARNING: Failed to check for duplicate images with hash %s: %v", hash, err)
			continue
		}
		if count == 0 {
			filePath := filepath.Join(uploadDir, filepath.Base(path))
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				log.Printf("WARNING: Failed to remove image file %s: %v", filePath, err)
			}
		}
	}
	return boardID, isOp, tx.Commit()
}

// DeleteBoard permanently removes a board and all its content.
func (ds *DatabaseService) DeleteBoard(boardID, uploadDir, modHash string) error {
	tx, err := ds.DB.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			log.Printf("ERROR: Failed to rollback transaction in DeleteBoard: %v", rerr)
		}
	}()

	rows, err := tx.Query("SELECT image_path FROM posts WHERE board_id = ?", boardID)
	if err != nil {
		return fmt.Errorf("failed to query image paths for board deletion: %w", err)
	}

	for rows.Next() {
		var imgPath sql.NullString
		if err := rows.Scan(&imgPath); err == nil && imgPath.Valid && imgPath.String != "" {
			filePath := filepath.Join(uploadDir, filepath.Base(imgPath.String))
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				log.Printf("WARNING: Failed to remove image file %s during board deletion: %v", filePath, err)
			}
		}
	}
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: Failed to close rows for board images: %v", err)
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
		log.Printf("ERROR: FTS Search failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var p models.Post
		if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.Subject, &p.IsOp); err != nil {
			log.Printf("ERROR: Failed to scan post during search: %v", err)
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
	defer stmt.Close()

	_, err = stmt.Exec(utils.GetSQLTime(), modHash, action, targetID, details)
	if err != nil {
		return fmt.Errorf("failed to execute mod action log: %w", err)
	}
	return nil
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
func (ds *DatabaseService) fetchAndAssignReplies(threadMap map[int64]*models.Thread, postMap map[int64]*models.Post) {
	if len(threadMap) == 0 {
		return
	}
	threadIDs := make([]interface{}, 0, len(threadMap))
	for id := range threadMap {
		threadIDs = append(threadIDs, id)
	}

	query := `
        WITH RankedReplies AS (
            SELECT p.*, ROW_NUMBER() OVER(PARTITION BY p.thread_id ORDER BY p.id DESC) as rn
            FROM posts p WHERE p.thread_id IN (?` + strings.Repeat(",?", len(threadIDs)-1) + `) AND p.id != (SELECT MIN(id) FROM posts WHERE thread_id=p.thread_id)
        )
        SELECT id, board_id, thread_id, name, tripcode, content, image_path, timestamp, ip_hash, cookie_hash
        FROM RankedReplies WHERE rn <= 3 ORDER BY thread_id, id ASC`
	replyRows, err := ds.DB.Query(query, threadIDs...)
	if err != nil {
		log.Printf("ERROR: Failed to fetch replies for board view: %v", err)
		return
	}
	defer func() {
		if err := replyRows.Close(); err != nil {
			log.Printf("ERROR: Failed to close rows in fetchAndAssignReplies: %v", err)
		}
	}()

	for replyRows.Next() {
		var p models.Post
		if err := replyRows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.ImagePath, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
			log.Printf("ERROR: Failed to scan reply post: %v", err)
			continue
		}
		if thread, ok := threadMap[p.ThreadID]; ok {
			thread.Posts = append(thread.Posts, p)
			postMap[p.ID] = &thread.Posts[len(thread.Posts)-1]
		}
	}
	if err := replyRows.Err(); err != nil {
		log.Printf("ERROR: Row error during reply scan: %v", err)
	}
}
func (ds *DatabaseService) fetchAndAssignBacklinks(postIDs []interface{}, assignFunc func(targetID, backlinkID int64)) {
	if len(postIDs) == 0 {
		return
	}
	query := "SELECT target_post_id, source_post_id FROM backlinks WHERE target_post_id IN (?" + strings.Repeat(",?", len(postIDs)-1) + ")"
	rows, err := ds.DB.Query(query, postIDs...)
	if err != nil {
		log.Printf("ERROR: Failed to query backlinks: %v", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("ERROR: Failed to close rows in fetchAndAssignBacklinks: %v", err)
		}
	}()
	for rows.Next() {
		var sourceID, targetID int64
		if err := rows.Scan(&targetID, &sourceID); err == nil {
			assignFunc(targetID, sourceID)
		} else {
			log.Printf("ERROR: Failed to scan backlink row: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: Row error during backlink scan: %v", err)
	}
}
