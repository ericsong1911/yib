// yib/main.go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
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
	uploadDir   string
	bannerFile  string
	maxFileSize int64
	maxWidth    int
	maxHeight   int
}

// --- Methods to satisfy the handlers.App interface ---
func (a *Application) DB() *database.DatabaseService { return a.db }
func (a *Application) RateLimiter() *models.RateLimiter { return a.rateLimiter }
func (a *Application) Challenges() *models.ChallengeStore { return a.challenges }
func (a *Application) UploadDir() string                 { return a.uploadDir }
func (a *Application) BannerFile() string                { return a.bannerFile }

func main() {
	mrand.Seed(time.Now().UnixNano())
	saltBytes := make([]byte, 32)
	if _, err := rand.Read(saltBytes); err != nil {
		log.Fatal("Failed to generate IP salt:", err)
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

	dbService, err := database.InitDB(dbPath)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer dbService.DB.Close()

	if err := handlers.LoadTemplates(); err != nil {
		log.Fatal("Failed to load templates:", err)
	}
	os.MkdirAll("./uploads", 0755)
	utils.CreatePlaceholderImage()

	app := &Application{
		db:          dbService,
		rateLimiter: models.NewRateLimiter(),
		challenges:  models.NewChallengeStore(),
		uploadDir:   "./uploads",
		bannerFile:  "./banner.txt",
		maxFileSize: config.MaxFileSize,
		maxHeight:   config.MaxHeight,
		maxWidth:    config.MaxWidth,
	}

	// Chi router setup
	mux := setupRouter(app)
	finalHandler := handlers.AppContextMiddleware(app, handlers.CookieMiddleware(handlers.CSRFMiddleware(mux)))

	// --- Graceful Shutdown ---
	server := &http.Server{Addr: ":" + port, Handler: finalHandler}
	go func() {
		log.Printf("yib v%s running on :%s", config.AppVersion, port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Could not listen on :%s: %v\n", port, err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exiting")
}

// setupRouter now accepts our Application struct and uses Chi router
func setupRouter(app *Application) *chi.Mux {
	mux := chi.NewRouter()

	// Using Chi's standard middleware for logging and panic recovery
	mux.Use(middleware.RequestID)
	mux.Use(middleware.RealIP)
	mux.Use(middleware.Logger)
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
	})

	// Page-serving router
	mux.Get("/", handlers.MakeHandler(app, handlers.HandleHome))
	mux.NotFound(handlers.MakeHandler(app, handlers.PageRouter)) // Fallback to custom page router
	mux.Get("/*", handlers.MakeHandler(app, handlers.PageRouter))

	return mux
}