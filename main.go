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
	dailySalt   string // New field for daily rotating salt
	storage     models.StorageService
}

// Methods to satisfy the handlers.App interface
func (a *Application) DB() *database.DatabaseService      { return a.db }
func (a *Application) RateLimiter() *models.RateLimiter   { return a.rateLimiter }
func (a *Application) Challenges() *models.ChallengeStore { return a.challenges }
func (a *Application) Logger() *slog.Logger               { return a.logger }
func (a *Application) UploadDir() string                  { return a.uploadDir }
func (a *Application) BannerFile() string                 { return a.bannerFile }
func (a *Application) DailySalt() string                  { return a.dailySalt }
func (a *Application) Storage() models.StorageService     { return a.storage }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	mrand.New(mrand.NewSource(time.Now().UnixNano()))
	saltBytes := make([]byte, 32)
	if _, err := rand.Read(saltBytes); err != nil {
		logger.Error("Failed to generate IP salt", "error", err)
		os.Exit(1)
	}
	utils.IPSalt = hex.EncodeToString(saltBytes)

	dailySalt := utils.GetDailySalt() // Initialize daily salt

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
	utils.BackupDir = backupDir
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

	// --- Storage Service Init ---
	var storageService models.StorageService
	if utils.GetEnv("YIB_S3_ENABLED", "false") == "true" {
		endpoint := utils.GetEnv("YIB_S3_ENDPOINT", "")
		accessKey := utils.GetEnv("YIB_S3_ACCESS_KEY", "")
		secretKey := utils.GetEnv("YIB_S3_SECRET_KEY", "")
		bucket := utils.GetEnv("YIB_S3_BUCKET", "")
		region := utils.GetEnv("YIB_S3_REGION", "us-east-1")
		publicURL := utils.GetEnv("YIB_S3_PUBLIC_URL", "")
		useSSL := utils.GetEnv("YIB_S3_USE_SSL", "true") == "true"

		var err error
		storageService, err = utils.NewS3Storage(endpoint, accessKey, secretKey, bucket, region, publicURL, useSSL)
		if err != nil {
			logger.Error("Failed to initialize S3 storage", "error", err)
			os.Exit(1)
		}
		logger.Info("S3 Storage initialized", "endpoint", endpoint, "bucket", bucket)
	} else {
		storageService = &utils.LocalStorage{UploadDir: "./uploads"}
		logger.Info("Local Storage initialized", "dir", "./uploads")
	}

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
		dailySalt:   dailySalt,
		storage:     storageService,
	}

	mux := handlers.SetupRouter(app)
	
	var s3PublicURL string
	if s3Store, ok := storageService.(*utils.S3Storage); ok {
		s3PublicURL = s3Store.PublicURL
	}
	
	finalHandler := handlers.AppContextMiddleware(app, handlers.CookieMiddleware(handlers.CSRFMiddleware(handlers.NewSecurityHeadersMiddleware(s3PublicURL)(mux))))

	// --- Graceful Shutdown ---
	server := &http.Server{Addr: ":" + port, Handler: finalHandler}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	logger.Info("yib server started successfully",
		"version", config.AppVersion,
		"address", "http://localhost:"+port,
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}
	logger.Info("Server exiting")
}
