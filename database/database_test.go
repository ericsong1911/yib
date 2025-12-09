//go:build fts5

// yib/database/database_test.go
package database

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
	"yib/utils"
)

// setupTestDB creates a new in-memory SQLite database for testing.
func setupTestDB(t *testing.T) *DatabaseService {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	dir, err := os.MkdirTemp("", "yib_test_db")
	if err != nil {
		t.Fatalf("Failed to create temp dir for test DB: %v", err)
	}
	dbPath := filepath.Join(dir, "test.db?_journal_mode=WAL&_foreign_keys=on")

	ds, err := InitDB(dbPath, logger)
	if err != nil {
		t.Fatalf("Failed to initialize test database: %v", err)
	}

	t.Cleanup(func() {
		ds.DB.Close()
		os.RemoveAll(dir)
	})

	return ds
}

// TestInitDB checks if the database is seeded with default data correctly.
func TestInitDB(t *testing.T) {
	ds := setupTestDB(t)

	var categoryCount int
	err := ds.DB.QueryRow("SELECT COUNT(*) FROM categories").Scan(&categoryCount)
	if err != nil {
		t.Fatalf("Failed to query categories: %v", err)
	}
	if categoryCount == 0 {
		t.Error("Expected categories to be seeded, but count is 0")
	}

	var boardCount int
	err = ds.DB.QueryRow("SELECT COUNT(*) FROM boards").Scan(&boardCount)
	if err != nil {
		t.Fatalf("Failed to query boards: %v", err)
	}
	if boardCount == 0 {
		t.Error("Expected boards to be seeded, but count is 0")
	}
}

// TestMigrations verifies that our schema migrations run successfully.
func TestMigrations(t *testing.T) {
	ds := setupTestDB(t)

	// Check if the columns added in migration version 1 exist.
	rows, err := ds.DB.Query("SELECT thumbnail_path, is_moderator FROM posts LIMIT 1")
	if err != nil {
		t.Fatalf("Migration test failed. Could not query for new columns in 'posts' table: %v", err)
	}
	defer rows.Close()

	// We only need to know the query succeeded, which means the columns exist.
	// If the columns didn't exist, the query would have returned an error.
	t.Log("Migration for 'posts' table columns successful.")

	var version int
	err = ds.DB.QueryRow("SELECT version FROM schema_migrations WHERE version = 1").Scan(&version)
	if err != nil {
		t.Fatalf("Migration test failed. Migration version 1 was not recorded in schema_migrations table: %v", err)
	}
	if version != 1 {
		t.Errorf("Expected migration version to be 1, but got %d", version)
	}
}

// TestDeletePost verifies the complex logic of post deletion and file cleanup.
func TestDeletePost(t *testing.T) {
	ds := setupTestDB(t)
	utils.IPSalt = "test-salt"

	uploadDir, err := os.MkdirTemp("", "yib_test_uploads")
	if err != nil {
		t.Fatalf("Failed to create temp upload dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	dummyImagePath := filepath.Join(uploadDir, "test-image.jpg")
	dummyThumbPath := filepath.Join(uploadDir, "test-image_thumb.jpg")
	os.WriteFile(dummyImagePath, []byte("image"), 0644)
	os.WriteFile(dummyThumbPath, []byte("thumb"), 0644)

	tx, _ := ds.DB.Begin()
	res, _ := tx.Exec("INSERT INTO threads (id, board_id, subject, bump) VALUES (1, 'b', 'Test Thread', ?)", time.Now())
	threadID, _ := res.LastInsertId()
	tx.Exec(`
		INSERT INTO posts (id, board_id, thread_id, content, image_path, thumbnail_path, image_hash)
		VALUES (1, 'b', ?, 'OP content', '/uploads/test-image.jpg', '/uploads/test-image_thumb.jpg', 'hash123')
	`, threadID)
	tx.Exec(`
		INSERT INTO posts (id, board_id, thread_id, content)
		VALUES (2, 'b', ?, 'Reply content')
	`, threadID)
	tx.Commit()

	// --- Test Case 1: Delete a reply ---
	boardID, isOp, err := ds.DeletePost(2, uploadDir, "mod_hash_1", "test deleting reply")
	if err != nil {
		t.Fatalf("Expected no error when deleting reply, but got: %v", err)
	}
	if isOp {
		t.Error("Expected isOp to be false when deleting a reply")
	}
	if boardID != "b" {
		t.Errorf("Expected boardID to be 'b', but got '%s'", boardID)
	}

	var postCount int
	ds.DB.QueryRow("SELECT COUNT(*) FROM posts WHERE id = 2").Scan(&postCount)
	if postCount != 0 {
		t.Error("Expected reply post to be deleted from the database, but it still exists")
	}

	// --- Test Case 2: Delete the OP (and the whole thread) ---
	boardID, isOp, err = ds.DeletePost(1, uploadDir, "mod_hash_2", "test deleting op")
	if err != nil {
		t.Fatalf("Expected no error when deleting OP, but got: %v", err)
	}
	if !isOp {
		t.Error("Expected isOp to be true when deleting an OP")
	}

	// Check that dummy files were removed.
	if _, err := os.Stat(dummyImagePath); !os.IsNotExist(err) {
		t.Error("Expected image file to be removed after OP deletion, but it still exists")
	}
	if _, err := os.Stat(dummyThumbPath); !os.IsNotExist(err) {
		t.Error("Expected thumbnail file to be removed after OP deletion, but it still exists")
	}
}

// TestGetBanDetails verifies ban checking logic.
func TestGetBanDetails(t *testing.T) {
	ds := setupTestDB(t)
	now := utils.GetSQLTime()

	tx, _ := ds.DB.Begin()
	// Permanent IP Ban
	tx.Exec("INSERT INTO bans (hash, ban_type, reason, created_at) VALUES ('ip_hash_perm', 'ip', 'Spamming', ?)", now)
	// Temporary Cookie Ban
	tx.Exec("INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES ('cookie_hash_temp', 'cookie', 'Flooding', ?, ?)", now, now.Add(1*time.Hour))
	// Expired IP Ban
	tx.Exec("INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES ('ip_hash_expired', 'ip', 'Old reason', ?, ?)", now.Add(-2*time.Hour), now.Add(-1*time.Hour))
	tx.Commit()

	testCases := []struct {
		name       string
		ipHash     string
		cookieHash string
		isBanned   bool
		banReason  string
	}{
		{"Permanent IP Ban", "ip_hash_perm", "some_cookie", true, "Spamming"},
		{"Temporary Cookie Ban", "some_ip", "cookie_hash_temp", true, "Flooding"},
		{"Expired IP Ban", "ip_hash_expired", "some_cookie", false, ""},
		{"Not Banned", "unbanned_ip", "unbanned_cookie", false, ""},
		{"Banned by IP, not Cookie", "ip_hash_perm", "unbanned_cookie", true, "Spamming"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ban, isBanned := ds.GetBanDetails("127.0.0.1", tc.ipHash, tc.cookieHash)
			if isBanned != tc.isBanned {
				t.Errorf("Expected isBanned to be %v, but got %v", tc.isBanned, isBanned)
			}
			if isBanned && ban.Reason != tc.banReason {
				t.Errorf("Expected ban reason to be '%s', but got '%s'", tc.banReason, ban.Reason)
			}
		})
	}
}

// TestBackupDatabase verifies the new VACUUM INTO backup method.
func TestBackupDatabase(t *testing.T) {
	// Use setupTestDB which correctly creates a DB service with WAL enabled.
	ds := setupTestDB(t)

	// Add some data to the database.
	_, err := ds.DB.Exec("CREATE TABLE test (id INT); INSERT INTO test (id) VALUES (123);")
	if err != nil {
		t.Fatalf("Failed to write to source DB: %v", err)
	}

	// --- Setup a temporary backup directory ---
	backupDir, err := os.MkdirTemp("", "yib_test_backup_dest")
	if err != nil {
		t.Fatalf("Failed to create temp backup dir: %v", err)
	}
	defer os.RemoveAll(backupDir)
	utils.BackupDir = backupDir // Set the global for the test

	// --- Run the backup method ---
	backupPath, err := ds.BackupDatabase()
	if err != nil {
		t.Fatalf("BackupDatabase failed: %v", err)
	}

	// --- Verify the results ---
	info, err := os.Stat(backupPath)
	if os.IsNotExist(err) {
		t.Fatalf("Backup file was not created at the expected path: %s", backupPath)
	}
	if info.Size() == 0 {
		t.Error("Backup file was created but is empty.")
	}

	destDB, err := sql.Open("sqlite3", backupPath)
	if err != nil {
		t.Fatalf("Could not open the created backup file as a database: %v", err)
	}
	defer destDB.Close()

	var id int
	err = destDB.QueryRow("SELECT id FROM test WHERE id = 123").Scan(&id)
	if err != nil {
		t.Errorf("Could not read test data from backup database: %v", err)
	}
	if id != 123 {
		t.Errorf("Expected to read value 123 from backup, but got %d", id)
	}
}
