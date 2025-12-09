package handlers

import (
	"net/http"
	"yib/config"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func SetupRouter(app App) *chi.Mux {
	mux := chi.NewRouter()

	mux.Use(middleware.RequestID)
	mux.Use(middleware.RealIP)
	mux.Use(NewStructuredLogger(app.Logger()))
	mux.Use(middleware.Recoverer)

	// Static file servers
	mux.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(app.UploadDir()))))
	mux.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Action handlers
	mux.Post("/post", MakeHandler(app, HandlePost))
	mux.Post("/delete", MakeHandler(app, HandleCookieDelete))
	mux.Post("/report", MakeHandler(app, HandleReport))
	mux.Get("/api/post/{postID}", MakeHandler(app, HandlePostPreview))

	// Moderation handlers
	mux.Route("/mod", func(r chi.Router) {
		r.Use(RequireLAN)
		r.Get("/", MakeHandler(app, HandleModeration))
		r.Post("/create-board", MakeHandler(app, HandleCreateBoard))
		r.Post("/delete-post", MakeHandler(app, HandleModDelete))
		r.Post("/ban", MakeHandler(app, HandleBan))
		r.Post("/toggle-sticky", MakeHandler(app, HandleToggleSticky))
		r.Post("/toggle-lock", MakeHandler(app, HandleToggleLock))
		r.Get("/bans", MakeHandler(app, HandleBanList))
		r.Post("/remove-ban", MakeHandler(app, HandleRemoveBan))
		r.Get("/ip-lookup", MakeHandler(app, HandleIPLookup))
		r.Get("/cookie-lookup", MakeHandler(app, HandleCookieLookup))
		r.Get("/lookup", MakeHandler(app, HandleUnifiedLookup))
		r.Post("/resolve-report", MakeHandler(app, HandleResolveReport))
		r.Get("/edit-board", MakeHandler(app, HandleEditBoard))
		r.Post("/edit-board", MakeHandler(app, HandleEditBoard))
		r.Post("/delete-board", MakeHandler(app, HandleDeleteBoard))
		r.Post("/mass-delete", MakeHandler(app, HandleMassDelete))
		r.Post("/manage-categories", MakeHandler(app, HandleManageCategories))
		r.Get("/log", MakeHandler(app, HandleModLog))
		r.Get("/banner", MakeHandler(app, HandleBanner))
		r.Post("/banner", MakeHandler(app, HandleBanner))
		r.Post("/backup-db", MakeHandler(app, HandleDatabaseBackup))
	})

	// Page-serving router
	mux.Get("/", MakeHandler(app, HandleHome))
	mux.NotFound(MakeHandler(app, PageRouter))
	mux.Get("/*", MakeHandler(app, PageRouter))
	mux.Post("/*", MakeHandler(app, PageRouter))

	return mux
}

var configDummy struct {
	AppVersion string
}

func init() {
	configDummy.AppVersion = config.AppVersion
}
