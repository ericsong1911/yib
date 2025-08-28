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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
func (a *Application) Logger() *slog.Logger               { return a.logger } // NEW-FEATURE
func (a *Application) UploadDir() string                  { return a.uploadDir }
func (a *Application) BannerFile() string                 { return a.bannerFile }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mrand.Seed(time.Now().UnixNano())
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
	os.MkdirAll(utils.BackupDir, 0755)

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
	defer dbService.DB.Close()

	if err := handlers.LoadTemplates(); err != nil {
		logger.Error("Failed to load templates", "error", err)
		os.Exit(1)
	}
	os.MkdirAll("./uploads", 0755)
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
	mux := setupRouter(app)
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

// setupRouter now accepts our Application struct and uses Chi router
func setupRouter(app *Application) *chi.Mux {
	mux := chi.NewRouter()

	// Using Chi's standard middleware for logging and panic recovery
	mux.Use(middleware.RequestID)
	mux.Use(middleware.RealIP)
	// Replace Chi's logger with our structured logger
	mux.Use(handlers.NewStructuredLogger(app.logger))
	mux.Use(middleware.Recoverer)

	// Static file servers
	mux.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(app.uploadDir))))
	mux.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Action handlers
	mux.Post("/post", handlers.MakeHandler(app, handlers.HandlePost))
	mux.Post("/delete", handlers.MakeHandler(app, handlers.HandleCookieDelete))
	mux.Post("/report", handlers.MakeHandler(app, handlers.HandleReport))
	mux.Get("/api/post/{postID}", handlers.MakeHandler(app, handlers.HandlePostPreview))

	// Moderation handlers
	mux.Route("/mod", func(r chi.Router) {
		r.Use(handlers.RequireLAN)
		r.Get("/", handlers.MakeHandler(app, handlers.HandleModeration))
		r.Post("/create-board", handlers.MakeHandler(app, handlers.HandleCreateBoard))
		r.Post("/delete-post", handlers.MakeHandler(app, handlers.HandleModDelete))
		r.Post("/ban", handlers.MakeHandler(app, handlers.HandleBan))
		r.Post("/toggle-sticky", handlers.MakeHandler(app, handlers.HandleToggleSticky))
		r.Post("/toggle-lock", handlers.MakeHandler(app, handlers.HandleToggleLock))
		r.Get("/bans", handlers.MakeHandler(app, handlers.HandleBanList))
		r.Post("/remove-ban", handlers.MakeHandler(app, handlers.HandleRemoveBan))
		r.Get("/ip-lookup", handlers.MakeHandler(app, handlers.HandleIPLookup))
		r.Get("/cookie-lookup", handlers.MakeHandler(app, handlers.HandleCookieLookup))
		r.Get("/lookup", handlers.MakeHandler(app, handlers.HandleUnifiedLookup))
		r.Post("/resolve-report", handlers.MakeHandler(app, handlers.HandleResolveReport))
		r.Get("/edit-board", handlers.MakeHandler(app, handlers.HandleEditBoard))
		r.Post("/edit-board", handlers.MakeHandler(app, handlers.HandleEditBoard))
		r.Post("/delete-board", handlers.MakeHandler(app, handlers.HandleDeleteBoard))
		r.Post("/manage-categories", handlers.MakeHandler(app, handlers.HandleManageCategories))
		r.Get("/log", handlers.MakeHandler(app, handlers.HandleModLog))
		r.Get("/banner", handlers.MakeHandler(app, handlers.HandleBanner))
		r.Post("/banner", handlers.MakeHandler(app, handlers.HandleBanner))
		r.Post("/backup-db", handlers.MakeHandler(app, handlers.HandleDatabaseBackup))

	})

	// Page-serving router
	mux.Get("/", handlers.MakeHandler(app, handlers.HandleHome))
	mux.NotFound(handlers.MakeHandler(app, handlers.PageRouter)) // Fallback to custom page router
	mux.Get("/*", handlers.MakeHandler(app, handlers.PageRouter))
	mux.Post("/*", handlers.MakeHandler(app, handlers.PageRouter))

	return mux
}
