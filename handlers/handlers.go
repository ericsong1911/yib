// yib/handlers/handlers.go

package handlers

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"log"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"yib/database"
	"yib/models"
	"yib/utils"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// App is an interface that defines the dependencies our handlers need.
type App interface {
	DB() *database.DatabaseService
	RateLimiter() *models.RateLimiter
	Challenges() *models.ChallengeStore
	Logger() *slog.Logger
	UploadDir() string
	BannerFile() string
}

// --- Board List Cache ---
var (
	boardListCache []models.NavBoardEntry
	cacheLock      sync.RWMutex
)

// getBoardList is a cached function to retrieve all boards for the nav dropdown.
func getBoardList(app App) []models.NavBoardEntry {
	cacheLock.RLock()
	if boardListCache != nil {
		cacheLock.RUnlock()
		return boardListCache
	}
	cacheLock.RUnlock()

	cacheLock.Lock()
	defer cacheLock.Unlock()

	if boardListCache != nil {
		return boardListCache
	}

	rows, err := app.DB().DB.Query("SELECT id, name FROM boards WHERE archived = 0 ORDER BY id")
	if err != nil {
		app.Logger().Error("Failed to query board list for global dropdown", "error", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			app.Logger().Error("Failed to close rows in getBoardList", "error", err)
		}
	}()

	var boards []models.NavBoardEntry
	for rows.Next() {
		var b models.NavBoardEntry
		if err := rows.Scan(&b.ID, &b.Name); err == nil {
			boards = append(boards, b)
		}
	}
	if err := rows.Err(); err != nil {
		app.Logger().Error("Row error scanning board list for global dropdown", "error", err)
	}

	boardListCache = boards
	return boards
}

// ClearBoardListCache invalidates the global board list cache.
func ClearBoardListCache() {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	boardListCache = nil
}

// respondJSON sends a JSON response with a given status code.
func respondJSON(w http.ResponseWriter, status int, payload interface{}, app App) {
	response, err := json.Marshal(payload)
	if err != nil {
		app.Logger().Error("Failed to marshal JSON payload", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		if _, werr := w.Write([]byte(`{"error":"Failed to marshal JSON response"}`)); werr != nil {
			app.Logger().Error("Failed to write internal server error response", "error", werr)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(response); err != nil {
		app.Logger().Error("Failed to write JSON response", "error", err)
	}
}

// MakeHandler now accepts our generic App interface.
func MakeHandler(app App, fn func(http.ResponseWriter, *http.Request, App)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fn(w, r, app)
	}
}

// HandleNewChallenge generates a new challenge and returns it as JSON.
func HandleNewChallenge(w http.ResponseWriter, r *http.Request, app App) {
	token, question := app.Challenges().GenerateChallenge()
	payload := map[string]string{
		"token":    token,
		"question": question,
	}
	respondJSON(w, http.StatusOK, payload, app)
}

// Page represents a single link in the pagination control.
type Page struct {
	Number     int
	IsCurrent  bool
	IsEllipsis bool
}

// PageRouter is the main router for serving different pages based on the URL path.
func PageRouter(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "PageRouter")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/favicon.ico" {
		http.NotFound(w, r)
		return
	}

	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	boardID := parts[0]

	switch boardID {
	case "search":
		HandleSearch(w, r, app)
		return
	case "about":
		HandleAbout(w, r, app)
		return
	}

	if !regexp.MustCompile(`^[a-z0-9]{1,10}$`).MatchString(boardID) {
		http.NotFound(w, r)
		return
	}

	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		logger.Info("User attempted to access non-existent board", "board_id", boardID)
		http.NotFound(w, r)
		return
	}

	if boardConfig.RequirePass && !verifyBoardAccess(boardConfig, r) {
		handleBoardLogin(w, r, app, boardConfig)
		return
	}

	switch len(parts) {
	case 1:
		HandleBoard(w, r, app, boardConfig)
	case 2:
		switch parts[1] {
		case "catalog":
			HandleCatalog(w, r, app, boardConfig)
		case "archive":
			HandleArchive(w, r, app, boardConfig)
		default:
			http.NotFound(w, r)
		}
	case 3:
		if parts[1] == "thread" {
			HandleThread(w, r, app, boardConfig, parts[2])
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// HandleHome serves the main homepage listing all boards.
func HandleHome(w http.ResponseWriter, r *http.Request, app App) {
	rows, err := app.DB().DB.Query(`
        SELECT c.id, c.name, b.id, b.name, b.description, b.require_pass
        FROM categories c
        LEFT JOIN boards b ON c.id = b.category_id AND b.archived = 0
        ORDER BY c.sort_order, c.name, b.sort_order, b.name`)
	if err != nil {
		app.Logger().Error("Failed to query boards and categories for homepage", "error", err)
		http.Error(w, "Database error loading homepage.", 500)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			app.Logger().Error("Failed to close rows in HandleHome", "error", err)
		}
	}()

	categoryMap := make(map[int]*models.Category)
	var categories []*models.Category

	for rows.Next() {
		var catID int
		var catName string
		var boardID, boardName, boardDesc sql.NullString
		var boardLocked sql.NullBool

		if err := rows.Scan(&catID, &catName, &boardID, &boardName, &boardDesc, &boardLocked); err != nil {
			app.Logger().Error("Failed to scan homepage board/category row", "error", err)
			continue
		}

		if _, ok := categoryMap[catID]; !ok {
			cat := &models.Category{ID: catID, Name: catName}
			categoryMap[catID] = cat
			categories = append(categories, cat)
		}
		if boardID.Valid {
			categoryMap[catID].Boards = append(categoryMap[catID].Boards, models.BoardEntry{
				ID:          boardID.String,
				Name:        boardName.String,
				Description: boardDesc.String,
				IsLocked:    boardLocked.Bool,
			})
		}
	}
	if err := rows.Err(); err != nil {
		app.Logger().Error("Row error scanning homepage data", "error", err)
	}

	render(w, r, app, "layout.html", "home.html", map[string]interface{}{
		"Title":      "Home",
		"BoardTitle": "yib - Yale Image Board",
		"Categories": categories,
	})
}

// HandleAbout serves the static "About" page.
func HandleAbout(w http.ResponseWriter, r *http.Request, app App) {
	render(w, r, app, "layout.html", "about.html", map[string]interface{}{
		"Title":      "About",
		"BoardTitle": "About yib",
	})
}

// HandleBoard serves a board's index page, showing its threads.
func HandleBoard(w http.ResponseWriter, r *http.Request, app App, boardConfig *models.BoardConfig) {
	logger := app.Logger().With("handler", "HandleBoard", "board_id", boardConfig.ID)
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	pageSize := 10

	threads, err := app.DB().GetThreadsForBoard(boardConfig.ID, false, page, pageSize, true)
	if err != nil {
		logger.Error("DB error getting threads", "error", err)
		http.Error(w, "Database error loading board.", 500)
		return
	}

	totalThreads, err := app.DB().GetThreadCount(boardConfig.ID, false)
	if err != nil {
		logger.Error("DB error getting thread count", "error", err)
	}
	totalPages := int(math.Ceil(float64(totalThreads) / float64(pageSize)))

	render(w, r, app, "layout.html", "board.html", map[string]interface{}{
		"Title":                 "/" + boardConfig.ID + "/ - " + boardConfig.Name,
		"BoardID":               boardConfig.ID,
		"BoardTitle":            "/" + boardConfig.ID + "/ - " + boardConfig.Name,
		"BoardSubtitle":         boardConfig.Description,
		"BoardConfig":           boardConfig,
		"Threads":               threads,
		"Pagination":            generatePagination(page, totalPages),
		"IsModerator":           utils.IsModerator(r),
		"CurrentUserCookieHash": utils.HashIP(r.Context().Value(UserCookieKey).(string)),
	})
}

// HandleCatalog serves a board's catalog page.
func HandleCatalog(w http.ResponseWriter, r *http.Request, app App, boardConfig *models.BoardConfig) {
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	pageSize := 25

	threads, err := app.DB().GetThreadsForBoard(boardConfig.ID, false, page, pageSize, false)
	if err != nil {
		log.Printf("ERROR: DB error getting catalog for /%s/: %v", boardConfig.ID, err)
		http.Error(w, "Database error loading catalog.", 500)
		return
	}

	totalThreads, err := app.DB().GetThreadCount(boardConfig.ID, false)
	if err != nil {
		log.Printf("ERROR: DB error getting thread count for /%s/ catalog: %v", boardConfig.ID, err)
	}
	totalPages := int(math.Ceil(float64(totalThreads) / float64(pageSize)))

	render(w, r, app, "layout.html", "catalog.html", map[string]interface{}{
		"Title":         "Catalog for /" + boardConfig.ID + "/",
		"BoardID":       boardConfig.ID,
		"BoardTitle":    "Catalog for /" + boardConfig.ID + "/",
		"BoardSubtitle": boardConfig.Name,
		"BoardConfig":   boardConfig,
		"Threads":       threads,
		"Pagination":    generatePagination(page, totalPages),
	})
}

// HandleArchive serves a board's archive page.
func HandleArchive(w http.ResponseWriter, r *http.Request, app App, boardConfig *models.BoardConfig) {
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	pageSize := 25

	threads, err := app.DB().GetThreadsForBoard(boardConfig.ID, true, page, pageSize, false)
	if err != nil {
		log.Printf("ERROR: DB error getting archive for /%s/: %v", boardConfig.ID, err)
		http.Error(w, "Database error loading archive.", 500)
		return
	}

	totalThreads, err := app.DB().GetThreadCount(boardConfig.ID, true)
	if err != nil {
		log.Printf("ERROR: DB error getting thread count for /%s/ archive: %v", boardConfig.ID, err)
	}
	totalPages := int(math.Ceil(float64(totalThreads) / float64(pageSize)))

	render(w, r, app, "layout.html", "archive.html", map[string]interface{}{
		"Title":         "Archive for /" + boardConfig.ID + "/",
		"BoardID":       boardConfig.ID,
		"BoardTitle":    "Archive for /" + boardConfig.ID + "/",
		"BoardSubtitle": boardConfig.Name,
		"BoardConfig":   boardConfig,
		"Threads":       threads,
		"Pagination":    generatePagination(page, totalPages),
	})
}

// HandleThread serves a single thread page.
func HandleThread(w http.ResponseWriter, r *http.Request, app App, boardConfig *models.BoardConfig, threadIDStr string) {
	threadID, err := strconv.ParseInt(threadIDStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var thread models.Thread
	err = app.DB().DB.QueryRow("SELECT id, board_id, subject, locked, sticky FROM threads WHERE id = ? AND board_id = ?", threadID, boardConfig.ID).Scan(&thread.ID, &thread.BoardID, &thread.Subject, &thread.Locked, &thread.Sticky)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		log.Printf("ERROR: DB error getting thread %d for /%s/: %v", threadID, boardConfig.ID, err)
		http.Error(w, "Database error loading thread.", 500)
		return
	}

	thread.Posts, err = app.DB().GetPostsForThread(threadID)
	if err != nil {
		log.Printf("ERROR: DB error getting posts for thread %d: %v", threadID, err)
		http.Error(w, "Database error loading posts.", 500)
		return
	}
	if len(thread.Posts) > 0 {
		thread.Posts[0].Subject = thread.Subject
	} else {
		log.Printf("ERROR: Thread %d has no posts, which should be impossible.", threadID)
		http.Error(w, "Data consistency error: thread has no posts.", 500)
		return
	}

	render(w, r, app, "layout.html", "thread.html", map[string]interface{}{
		"Title":                 thread.Subject,
		"BoardID":               boardConfig.ID,
		"BoardTitle":            "/" + boardConfig.ID + "/ - " + boardConfig.Name,
		"BoardConfig":           boardConfig,
		"Thread":                thread,
		"IsModerator":           utils.IsModerator(r),
		"CurrentUserCookieHash": utils.HashIP(r.Context().Value(UserCookieKey).(string)),
	})
}

// HandlePostPreview serves the raw HTML for a single post, used for hover previews.
func HandlePostPreview(w http.ResponseWriter, r *http.Request, app App) {
	postIDStr := chi.URLParam(r, "postID")
	postID, err := strconv.ParseInt(postIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid post ID.", http.StatusBadRequest)
		return
	}

	post, err := app.DB().GetPostByID(postID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		log.Printf("Error fetching post for preview %d: %v", postID, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Post":                  post,
		"IsModerator":           utils.IsModerator(r),
		"CurrentUserCookieHash": utils.HashIP(r.Context().Value(UserCookieKey).(string)),
		"IsThreadView":          true, // Previews should never show a "Reply" link
		"csrfToken":             r.Context().Value(CSRFTokenKey),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = templates.ExecuteTemplate(w, "post", data)
	if err != nil {
		log.Printf("Error rendering post preview template for post %d: %v", postID, err)
	}
}

// HandleSearch serves the search page and displays results.
func HandleSearch(w http.ResponseWriter, r *http.Request, app App) {
	query := r.URL.Query().Get("q")
	boardID := r.URL.Query().Get("board")
	var results []models.Post

	if query != "" {
		var err error
		results, err = app.DB().SearchPosts(query, boardID)
		if err != nil {
			results = []models.Post{}
		}
	}

	rows, err := app.DB().DB.Query("SELECT id, name FROM boards WHERE archived = 0 ORDER BY id")
	if err != nil {
		log.Printf("ERROR: Failed to query boards for search page: %v", err)
	}

	type BoardEntry struct{ ID, Name string }
	var boards []BoardEntry
	if rows != nil {
		defer func() {
			if err := rows.Close(); err != nil {
				app.Logger().Error("Failed to close rows in HandleSearch", "error", err)
			}
		}()
		for rows.Next() {
			var be BoardEntry
			if err := rows.Scan(&be.ID, &be.Name); err != nil {
				log.Printf("ERROR: Failed to scan board for search dropdown: %v", err)
			} else {
				boards = append(boards, be)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning boards for search: %v", err)
		}
	}

	render(w, r, app, "layout.html", "search.html", map[string]interface{}{
		"Title":      "Search",
		"BoardTitle": "Search",
		"Query":      query,
		"BoardID":    boardID,
		"Boards":     boards,
		"Results":    results,
	})
}

// handleBoardLogin serves the password prompt for protected boards.
func handleBoardLogin(w http.ResponseWriter, r *http.Request, _ App, boardConfig *models.BoardConfig) {
	loginError := false
	if r.Method == http.MethodPost {
		password := r.FormValue("password")
		err := bcrypt.CompareHashAndPassword([]byte(boardConfig.Password), []byte(password))
		if err == nil {
			sessionHash := utils.GenerateBoardSessionHash(boardConfig.Password)
			http.SetCookie(w, &http.Cookie{
				Name:     "board_" + boardConfig.ID,
				Value:    sessionHash,
				Path:     "/" + boardConfig.ID + "/",
				MaxAge:   86400 * 30, // 30 days
				HttpOnly: true,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/"+boardConfig.ID+"/", http.StatusSeeOther)
			return
		}
		if err != bcrypt.ErrMismatchedHashAndPassword {
			log.Printf("ERROR: Bcrypt error comparing hash for board /%s/: %v", boardConfig.ID, err)
		}
		loginError = true
	}

	data := map[string]interface{}{
		"Title":       "Password Required",
		"BoardID":     boardConfig.ID,
		"csrfToken":   r.Context().Value(CSRFTokenKey),
		"LoginError":  loginError,
		"ColorScheme": boardConfig.ColorScheme,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "login.html", data); err != nil {
		log.Printf("Error rendering login template: %v", err)
	}
}

// verifyBoardAccess checks if the user has a valid session cookie for a protected board.
func verifyBoardAccess(boardConfig *models.BoardConfig, r *http.Request) bool {
	cookie, err := r.Cookie("board_" + boardConfig.ID)
	if err != nil {
		return false
	}
	expectedHash := utils.GenerateBoardSessionHash(boardConfig.Password)
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expectedHash)) == 1
}

// generatePagination creates the list of page links for the UI.
func generatePagination(currentPage, totalPages int) []Page {
	if totalPages <= 1 {
		return nil
	}

	const pagesToShow = 2

	var pages []Page

	start := currentPage - pagesToShow
	end := currentPage + pagesToShow

	if start < 1 {
		end += (1 - start)
		start = 1
	}

	if end > totalPages {
		start -= (end - totalPages)
		end = totalPages
	}

	if start < 1 {
		start = 1
	}

	if start > 1 {
		pages = append(pages, Page{Number: 1})
		if start > 2 {
			pages = append(pages, Page{IsEllipsis: true})
		}
	}

	for i := start; i <= end; i++ {
		pages = append(pages, Page{Number: i, IsCurrent: i == currentPage})
	}

	if end < totalPages {
		if end < totalPages-1 {
			pages = append(pages, Page{IsEllipsis: true})
		}
		pages = append(pages, Page{Number: totalPages})
	}

	return pages
}
