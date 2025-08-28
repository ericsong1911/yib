// yib/handlers/actions_test.go

//go:build fts5

package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"yib/database"
	"yib/models"
	"yib/utils"
)

// MockApplication holds dependencies for handler tests.
type MockApplication struct {
	db          *database.DatabaseService
	rateLimiter *models.RateLimiter
	challenges  *models.ChallengeStore
	uploadDir   string
	logger      *slog.Logger
}

func (a *MockApplication) DB() *database.DatabaseService      { return a.db }
func (a *MockApplication) RateLimiter() *models.RateLimiter   { return a.rateLimiter }
func (a *MockApplication) Challenges() *models.ChallengeStore { return a.challenges }
func (a *MockApplication) Logger() *slog.Logger               { return a.logger }
func (a *MockApplication) UploadDir() string                  { return a.uploadDir }
func (a *MockApplication) BannerFile() string                 { return "./banner.txt" }

// setupTestApp creates a full application stack with a test database for integration testing.
func setupTestApp(t *testing.T) (*MockApplication, func()) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dbDir, err := os.MkdirTemp("", "yib_test_db")
	if err != nil {
		t.Fatalf("Failed to create temp dir for test DB: %v", err)
	}
	dbPath := filepath.Join(dbDir, "test.db?_journal_mode=WAL&_foreign_keys=on")
	dbService, err := database.InitDB(dbPath, logger)
	if err != nil {
		t.Fatalf("Failed to initialize test database: %v", err)
	}

	uploadDir, err := os.MkdirTemp("", "yib_test_uploads")
	if err != nil {
		t.Fatalf("Failed to create temp upload dir: %v", err)
	}

	// Use new RateLimiter signature with default test values.
	app := &MockApplication{
		db:          dbService,
		rateLimiter: models.NewRateLimiter(30*time.Second, 3, 1*time.Hour, 24*time.Hour),
		challenges:  models.NewChallengeStore(),
		uploadDir:   uploadDir,
		logger:      logger,
	}

	utils.IPSalt = "test-salt"

	cleanup := func() {
		app.db.DB.Close()
		os.RemoveAll(dbDir)
		os.RemoveAll(uploadDir)
		utils.IPSalt = ""
	}

	return app, cleanup
}

// Helper function to solve a challenge.
func solveChallenge(challengeStore *models.ChallengeStore) (string, string) {
	token, question := challengeStore.GenerateChallenge()
	parts := strings.Fields(question)
	num1, _ := strconv.Atoi(parts[2])
	num2Str := strings.TrimSuffix(parts[4], "?")
	num2, _ := strconv.Atoi(num2Str)
	answer := strconv.Itoa(num1 + num2)
	return token, answer
}

// TestHandlePost integration tests the entire post creation flow.
func TestHandlePost(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	if err := os.Chdir(".."); err != nil {
		t.Fatalf("could not change to root dir: %v", err)
	}
	if err := LoadTemplates(); err != nil {
		t.Fatalf("Failed to load templates: %v", err)
	}

	// --- Test Case 1: Creating a new thread with an image and thumbnail ---
	t.Run("Create New Thread with Image", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("board_id", "b")
		writer.WriteField("subject", "Thread with Image")
		writer.WriteField("name", "tester")
		writer.WriteField("content", "This post should have a thumbnail.")
		token, answer := solveChallenge(app.challenges)
		writer.WriteField("challenge_token", token)
		writer.WriteField("challenge_answer", answer)

		// Create a dummy image file part
		part, _ := writer.CreateFormFile("image", "test.png")
		// This is the magic byte signature for a PNG file.
		part.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82"))
		writer.Close()

		req := httptest.NewRequest("POST", "/post", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		ctx := context.WithValue(req.Context(), UserCookieKey, "test-cookie-id")
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(MakeHandler(app, HandlePost))
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Fatalf("Handler returned wrong status code: got %v want %v. Body: %s", status, http.StatusOK, rr.Body.String())
		}

		// Verify in the database that thumbnail path is present
		var thumbPath sql.NullString
		err := app.db.DB.QueryRow("SELECT thumbnail_path FROM posts WHERE content = ?", "This post should have a thumbnail.").Scan(&thumbPath)
		if err != nil {
			t.Fatalf("Could not query for post: %v", err)
		}
		if !thumbPath.Valid || thumbPath.String == "" {
			t.Error("Expected thumbnail_path to be populated, but it was null or empty")
		}
		t.Logf("Thumbnail created at: %s", thumbPath.String)
	})

	// --- Test Case 2: Post as a Moderator ---
	t.Run("Create Post as Moderator", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("board_id", "b")
		writer.WriteField("content", "This is a moderator post.")
		token, answer := solveChallenge(app.challenges)
		writer.WriteField("challenge_token", token)
		writer.WriteField("challenge_answer", answer)
		writer.Close()

		req := httptest.NewRequest("POST", "/post", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.RemoteAddr = "127.0.0.1:12345" // Set moderator IP
		ctx := context.WithValue(req.Context(), UserCookieKey, "mod-cookie-id")
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		MakeHandler(app, HandlePost)(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Fatalf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
		}

		// Verify in the database
		var isModerator bool
		err := app.db.DB.QueryRow("SELECT is_moderator FROM posts WHERE content = ?", "This is a moderator post.").Scan(&isModerator)
		if err != nil {
			t.Fatalf("Could not query for moderator post: %v", err)
		}
		if !isModerator {
			t.Error("Expected is_moderator to be true, but it was false")
		}
	})

	// --- Test Case 3: Reject post with invalid file type ---
	t.Run("Reject Invalid File Type", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("board_id", "b")
		writer.WriteField("content", "This post should be rejected.")
		token, answer := solveChallenge(app.challenges)
		writer.WriteField("challenge_token", token)
		writer.WriteField("challenge_answer", answer)

		// Create a text file part, not an image
		part, _ := writer.CreateFormFile("image", "not_an_image.txt")
		part.Write([]byte("this is just plain text"))
		writer.Close()

		req := httptest.NewRequest("POST", "/post", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		ctx := context.WithValue(req.Context(), UserCookieKey, "test-cookie-id-3")
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		MakeHandler(app, HandlePost)(rr, req)

		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("Expected status 400 Bad Request for invalid file type, but got %d", status)
		}

		var response map[string]string
		json.Unmarshal(rr.Body.Bytes(), &response)
		if !strings.Contains(response["error"], "unsupported file type") {
			t.Errorf("Expected error message about unsupported file type, but got: %s", response["error"])
		}
	})
}
