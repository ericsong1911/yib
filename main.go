// yib/main.go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"yib/config"
	"yib/database"
	"yib/handlers"
	"yib/models"
	"yib/utils"
)

// Application holds all the dependencies for the web server.
type Application struct {
	db          *database.DatabaseService
	rateLimiter *models.RateLimiter
	challenges  *models.ChallengeStore
	logger      *slog.Logger
	uploadDir   string
	bannerFile  string
	maxFileSize int64
	maxWidth    int
	maxHeight   int
}

// --- Methods to satisfy the handlers.App interface ---
func (a *Application) DB() *database.DatabaseService      { return a.db }
func (a *Application) RateLimiter() *models.RateLimiter   { return a.rateLimiter }
func (a *Application) Challenges() *models.ChallengeStore { return a.challenges }
func (a *Application) Logger() *slog.Logger               { return a.logger }
func (a *Application) UploadDir() string                  { return a.uploadDir }
func (a *Application) BannerFile() string                 { return a.bannerFile }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mrand.New(mrand.NewSource(time.Now().UnixNano()))
	saltBytes := make([]byte, 32)
	if _, err := rand.Read(saltBytes); err != nil {
		logger.Error("Failed to generate IP salt", "error", err)
		os.Exit(1)
	}
	utils.IPSalt = hex.EncodeToString(saltBytes)

	// --- External Configuration ---
	port := os.Getenv("YIB_PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("YIB_DB_PATH")
	if dbPath == "" {
		dbPath = "./yalie.db?_journal_mode=WAL&_foreign_keys=on"
	}
	backupDir := os.Getenv("YIB_BACKUP_DIR")
	if backupDir == "" {
		backupDir = "./backups"
	}
	utils.BackupDir = backupDir // Set global backup directory
	if err := os.MkdirAll(utils.BackupDir, 0755); err != nil {
		logger.Error("FATAL: Could not create backup directory", "path", utils.BackupDir, "error", err)
		os.Exit(1)
	}

	rateLimitEvery, err := time.ParseDuration(utils.GetEnv("YIB_RATE_EVERY", config.DefaultRateLimitEvery))
	if err != nil {
		logger.Warn("Invalid YIB_RATE_EVERY duration, using default", "value", utils.GetEnv("YIB_RATE_EVERY", ""), "default", config.DefaultRateLimitEvery)
		rateLimitEvery, _ = time.ParseDuration(config.DefaultRateLimitEvery)
	}
	rateLimitBurst, err := strconv.Atoi(utils.GetEnv("YIB_RATE_BURST", strconv.Itoa(config.DefaultRateLimitBurst)))
	if err != nil {
		logger.Warn("Invalid YIB_RATE_BURST integer, using default", "value", utils.GetEnv("YIB_RATE_BURST", ""), "default", config.DefaultRateLimitBurst)
		rateLimitBurst = config.DefaultRateLimitBurst
	}
	rateLimitPrune, err := time.ParseDuration(utils.GetEnv("YIB_RATE_PRUNE", config.DefaultRateLimitPrune))
	if err != nil {
		logger.Warn("Invalid YIB_RATE_PRUNE duration, using default", "value", utils.GetEnv("YIB_RATE_PRUNE", ""), "default", config.DefaultRateLimitPrune)
		rateLimitPrune, _ = time.ParseDuration(config.DefaultRateLimitPrune)
	}
	rateLimitExpire, err := time.ParseDuration(utils.GetEnv("YIB_RATE_EXPIRE", config.DefaultRateLimitExpire))
	if err != nil {
		logger.Warn("Invalid YIB_RATE_EXPIRE duration, using default", "value", utils.GetEnv("YIB_RATE_EXPIRE", ""), "default", config.DefaultRateLimitExpire)
		rateLimitExpire, _ = time.ParseDuration(config.DefaultRateLimitExpire)
	}

	dbService, err := database.InitDB(dbPath, logger) // Pass logger to DB
	if err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := dbService.DB.Close(); err != nil {
			logger.Error("Failed to close database", "error", err)
		}
	}()

	if err := handlers.LoadTemplates(); err != nil {
		logger.Error("Failed to load templates", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll("./uploads", 0755); err != nil {
		logger.Error("FATAL: Could not create uploads directory", "error", err)
		os.Exit(1)
	}
	utils.CreatePlaceholderImage(logger)

	app := &Application{
		db:          dbService,
		rateLimiter: models.NewRateLimiter(rateLimitEvery, rateLimitBurst, rateLimitPrune, rateLimitExpire),
		challenges:  models.NewChallengeStore(),
		logger:      logger, // Store logger
		uploadDir:   "./uploads",
		bannerFile:  "./banner.txt",
		maxFileSize: config.MaxFileSize,
		maxHeight:   config.MaxHeight,
		maxWidth:    config.MaxWidth,
	}

	// Chi router setup
	mux := handlers.SetupRouter(app)
	finalHandler := handlers.AppContextMiddleware(app, handlers.CookieMiddleware(handlers.CSRFMiddleware(handlers.SecurityHeadersMiddleware(mux))))

	// --- Graceful Shutdown ---
	server := &http.Server{Addr: ":" + port, Handler: finalHandler}

	// Start the server in a background goroutine.
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed unexpectedly", "error", err)
			os.Exit(1) // Exit if the server can't start (e.g., port in use).
		}
	}()

	logger.Info("yib server started successfully",
		"version", config.AppVersion,
		"address", "http://localhost:"+port,
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit // This is where the program will "hang" (correctly).

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}
	logger.Info("Server exiting")
}
