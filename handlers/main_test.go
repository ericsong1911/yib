//go:build fts5

package handlers

import (
	"context"
	"io"
	"log/slog"
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
func setupTestApp(t *testing.T) *MockApplication {
	if err := os.Chdir(".."); err != nil {
		t.Fatalf("Failed to change directory to project root: %v", err)
	}
	if err := LoadTemplates(); err != nil {
		t.Fatalf("Failed to load templates: %v", err)
	}
	if err := os.Chdir("handlers"); err != nil {
		t.Fatalf("Failed to change back to handlers directory: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dbDir, err := os.MkdirTemp("", "yib_test_db_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir for test DB: %v", err)
	}
	dbPath := filepath.Join(dbDir, "test.db?mode=memory&cache=shared&_journal_mode=WAL&_foreign_keys=on")
	dbService, err := database.InitDB(dbPath, logger)
	if err != nil {
		t.Fatalf("Failed to initialize test database: %v", err)
	}

	uploadDir, err := os.MkdirTemp("", "yib_test_uploads_*")
	if err != nil {
		t.Fatalf("Failed to create temp upload dir: %v", err)
	}

	app := &MockApplication{
		db:          dbService,
		rateLimiter: models.NewRateLimiter(30*time.Second, 3, 1*time.Hour, 24*time.Hour),
		challenges:  models.NewChallengeStore(),
		uploadDir:   uploadDir,
		logger:      logger,
	}

	utils.IPSalt = "test-salt"

	t.Cleanup(func() {
		app.db.DB.Close()
		os.RemoveAll(dbDir)
		os.RemoveAll(uploadDir)
		os.Remove("./banner.txt")
		utils.IPSalt = ""
	})

	return app
}

func solveChallenge(cs *models.ChallengeStore) (string, string) {
	token, question := cs.GenerateChallenge()
	parts := strings.Fields(question)
	num1, _ := strconv.Atoi(parts[2])
	num2Str := strings.TrimSuffix(parts[4], "?")
	num2, _ := strconv.Atoi(num2Str)
	answer := strconv.Itoa(num1 + num2)
	return token, answer
}

func newTestRequest(_ *testing.T, method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	ctx := context.WithValue(req.Context(), UserCookieKey, "test-cookie-id")
	return req.WithContext(ctx)
}
