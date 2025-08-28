// yib/handlers/moderation.go
package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"yib/database"
	"yib/models"
	"yib/utils"

	"golang.org/x/crypto/bcrypt"
)

// HandleModeration serves the main moderation dashboard.
func HandleModeration(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleModeration")
	var reports []models.Report
	rows, err := app.DB().DB.Query(`
        SELECT r.id, r.reason, r.ip_hash, r.created_at, p.id, p.board_id, p.thread_id
        FROM reports r JOIN posts p ON r.post_id = p.id
        WHERE r.resolved = 0 ORDER BY r.created_at DESC LIMIT 10`)
	if err != nil {
		logger.Error("Failed to query for active reports", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var rep models.Report
			if err := rows.Scan(&rep.ID, &rep.Reason, &rep.IPHash, &rep.CreatedAt, &rep.Post.ID, &rep.Post.BoardID, &rep.Post.ThreadID); err != nil {
				logger.Error("Failed to scan report row", "error", err)
			} else {
				reports = append(reports, rep)
			}
		}
		if err := rows.Err(); err != nil {
			logger.Error("Row error scanning reports", "error", err)
		}
	}

	var recentPosts []models.Post
	rows, err = app.DB().DB.Query(`SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts ORDER BY id DESC LIMIT 15`)
	if err != nil {
		logger.Error("Failed to query for recent posts", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p models.Post
			if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
				logger.Error("Failed to scan recent post row", "error", err)
			} else {
				recentPosts = append(recentPosts, p)
			}
		}
		if err := rows.Err(); err != nil {
			logger.Error("Row error scanning recent posts", "error", err)
		}
	}

	type ManagedBoard struct {
		models.BoardConfig
		ThreadCount int
	}
	var allBoards []ManagedBoard
	var allCategories []*models.Category

	catRows, err := app.DB().DB.Query("SELECT id, name, sort_order FROM categories ORDER BY sort_order, name")
	if err != nil {
		logger.Error("Failed to query categories for mod panel", "error", err)
	} else {
		defer catRows.Close()
		for catRows.Next() {
			var cat models.Category
			if err := catRows.Scan(&cat.ID, &cat.Name, &cat.SortOrder); err != nil {
				logger.Error("Failed to scan category row", "error", err)
			} else {
				allCategories = append(allCategories, &cat)
			}
		}
		if err := catRows.Err(); err != nil {
			logger.Error("Row error scanning categories", "error", err)
		}
	}

	boardRows, err := app.DB().DB.Query(`
		SELECT b.id, b.name, b.require_pass, b.category_id, b.sort_order, COUNT(t.id) as thread_count
		FROM boards b LEFT JOIN threads t ON b.id = t.board_id AND t.archived = 0
		GROUP BY b.id ORDER BY b.sort_order, b.name`)
	if err != nil {
		logger.Error("Failed to query boards for mod panel", "error", err)
	} else {
		defer boardRows.Close()
		for boardRows.Next() {
			var board ManagedBoard
			if err := boardRows.Scan(&board.ID, &board.Name, &board.RequirePass, &board.CategoryID, &board.SortOrder, &board.ThreadCount); err != nil {
				logger.Error("Failed to scan managed board row", "error", err)
			} else {
				allBoards = append(allBoards, board)
			}
		}
		if err := boardRows.Err(); err != nil {
			logger.Error("Row error scanning managed boards", "error", err)
		}
	}

	render(w, r, app, "mod_layout.html", "moderation.html", map[string]interface{}{
		"Title":       "Dashboard",
		"Boards":      allBoards,
		"Categories":  allCategories,
		"Reports":     reports,
		"RecentPosts": recentPosts,
		"RemoteAddr":  utils.GetIPAddress(r),
	})
}

func HandleEditBoard(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleEditBoard")
	if r.Method == http.MethodPost {
		boardID := r.FormValue("board_id")
		name := r.FormValue("name")
		description := r.FormValue("description")
		maxThreads, _ := strconv.Atoi(r.FormValue("max_threads"))
		bumpLimit, _ := strconv.Atoi(r.FormValue("bump_limit"))
		imageRequired := r.FormValue("image_required") == "on"
		colorScheme := r.FormValue("color_scheme")
		password := r.FormValue("password")
		require := r.FormValue("require") == "on"

		tx, err := app.DB().DB.Begin()
		if err != nil {
			logger.Error("Failed to start transaction for board edit", "error", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		if password != "" {
			hashedPass, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				logger.Error("Failed to hash new board password", "board_id", boardID, "error", err)
				http.Error(w, "Failed to process new password", http.StatusInternalServerError)
				return
			}
			_, err = tx.Exec(`UPDATE boards SET name=?, description=?, max_threads=?, bump_limit=?, image_required=?, color_scheme=?, password=?, require_pass=? WHERE id = ?`,
				name, description, maxThreads, bumpLimit, imageRequired, colorScheme, string(hashedPass), require, boardID)
			if err != nil {
				logger.Error("Failed to update board with new password", "board_id", boardID, "error", err)
				http.Error(w, "Database error updating board", http.StatusInternalServerError)
				return
			}
		} else {
			_, err = tx.Exec(`UPDATE boards SET name=?, description=?, max_threads=?, bump_limit=?, image_required=?, color_scheme=?, require_pass=? WHERE id = ?`,
				name, description, maxThreads, bumpLimit, imageRequired, colorScheme, require, boardID)
			if err != nil {
				logger.Error("Failed to update board", "board_id", boardID, "error", err)
				http.Error(w, "Database error updating board", http.StatusInternalServerError)
				return
			}
		}
		if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "edit_board", 0, boardID); err != nil {
			logger.Error("Failed to log board edit", "board_id", boardID, "error", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			logger.Error("Failed to commit board edit", "board_id", boardID, "error", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		app.DB().ClearBoardCache(boardID)
		ClearBoardListCache()
		http.Redirect(w, r, "/mod/", http.StatusSeeOther)
		return
	}
	boardID := r.URL.Query().Get("id")
	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		logger.Warn("Mod tried to edit non-existent board", "board_id", boardID, "error", err)
		http.Error(w, "Board not found.", http.StatusNotFound)
		return
	}
	render(w, r, app, "mod_layout.html", "mod_edit_board.html", map[string]interface{}{
		"Title": "Edit Board", "Board": boardConfig,
	})
}

func HandleDeleteBoard(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleDeleteBoard")
	boardID := r.FormValue("board_id")
	if boardID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Board ID is required."}, app)
		return
	}
	modHash := utils.HashIP(utils.GetIPAddress(r))
	if err := app.DB().DeleteBoard(boardID, app.UploadDir(), modHash); err != nil {
		logger.Error("Failed to delete board", "board_id", boardID, "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete board: " + err.Error()}, app)
		return
	}
	app.DB().ClearBoardCache(boardID)
	ClearBoardListCache()
	logger.Info("Board deleted by moderator", "board_id", boardID)
	respondJSON(w, http.StatusOK, map[string]string{"success": "Board deleted successfully."}, app)
}

func HandleManageCategories(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleManageCategories")
	if err := r.ParseForm(); err != nil {
		logger.Error("Failed to parse form for category management", "error", err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		logger.Error("Could not begin transaction for category management", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	for key, values := range r.Form {
		if strings.HasPrefix(key, "name-") {
			idStr := strings.TrimPrefix(key, "name-")
			if id, err := strconv.Atoi(idStr); err == nil && id > 0 {
				name := values[0]
				order, _ := strconv.Atoi(r.FormValue("order-" + idStr))
				if name == "" {
					if _, err := tx.Exec("UPDATE boards SET category_id = 1 WHERE category_id = ?", id); err != nil {
						logger.Error("Failed to reassign boards from deleted category", "id", id, "error", err)
					}
					if _, err := tx.Exec("DELETE FROM categories WHERE id = ? AND id != 1", id); err != nil {
						logger.Error("Failed to delete category", "id", id, "error", err)
					}
				} else {
					if _, err := tx.Exec("UPDATE categories SET name = ?, sort_order = ? WHERE id = ?", name, order, id); err != nil {
						logger.Error("Failed to update category", "id", id, "error", err)
					}
				}
			}
		} else if strings.HasPrefix(key, "board_cat-") {
			boardID := strings.TrimPrefix(key, "board_cat-")
			catID, _ := strconv.Atoi(values[0])
			order, _ := strconv.Atoi(r.FormValue("board_order-" + boardID))
			if _, err := tx.Exec("UPDATE boards SET category_id = ?, sort_order = ? WHERE id = ?", catID, order, boardID); err != nil {
				logger.Error("Failed to update board category/sort", "board_id", boardID, "error", err)
			}
		}
	}
	if newCatName := r.FormValue("name-0"); newCatName != "" {
		newCatOrder, _ := strconv.Atoi(r.FormValue("order-0"))
		if _, err := tx.Exec("INSERT INTO categories (name, sort_order) VALUES (?, ?)", newCatName, newCatOrder); err != nil {
			logger.Error("Failed to create new category", "name", newCatName, "error", err)
		}
	}
	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "manage_categories", 0, "Updated categories and board sorting"); err != nil {
		logger.Error("Failed to log category management", "error", err)
		http.Error(w, "Database error saving changes", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		logger.Error("Failed to commit category management transaction", "error", err)
		http.Error(w, "Database error saving changes", http.StatusInternalServerError)
		return
	}
	app.DB().ClearAllBoardCaches()
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}

func HandleModDelete(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleModDelete")
	postID, err := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid post ID."}, app)
		return
	}
	modHash := utils.HashIP(utils.GetIPAddress(r))
	details := fmt.Sprintf("Deleted post %d", postID)
	_, _, err = app.DB().DeletePost(postID, app.UploadDir(), modHash, details)
	if err != nil {
		logger.Error("Mod failed to delete post", "post_id", postID, "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete post."}, app)
		return
	}
	logger.Info("Post deleted by moderator", "post_id", postID)
	respondJSON(w, http.StatusOK, map[string]string{"success": "Post deleted successfully."}, app)
}

func HandleBan(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleBan")
	ipHash := r.FormValue("ip_hash")
	cookieHash := r.FormValue("cookie_hash")
	reason := r.FormValue("reason")
	durationHours, _ := strconv.Atoi(r.FormValue("duration"))
	modHash := utils.HashIP(utils.GetIPAddress(r))
	if ipHash == "" && cookieHash == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "No hash provided to ban."}, app)
		return
	}
	if reason == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "A ban reason is required."}, app)
		return
	}
	var expiresAt sql.NullTime
	if durationHours > 0 {
		expiresAt.Time = utils.GetSQLTime().Add(time.Duration(durationHours) * time.Hour)
		expiresAt.Valid = true
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		logger.Error("Could not begin transaction for ban", "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"}, app)
		return
	}
	defer tx.Rollback()
	if ipHash != "" {
		_, err := tx.Exec(`INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES (?, 'ip', ?, ?, ?) ON CONFLICT(hash, ban_type) DO UPDATE SET reason=excluded.reason, expires_at=excluded.expires_at`,
			ipHash, reason, utils.GetSQLTime(), expiresAt)
		if err != nil {
			logger.Error("Failed to apply IP ban", "hash", ipHash, "error", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error applying IP ban."}, app)
			return
		}
		if err := database.LogModAction(tx, modHash, "apply_ban", 0, fmt.Sprintf("IP Hash: %s, Reason: %s", ipHash, reason)); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error logging ban."}, app)
			return
		}
	}
	if cookieHash != "" {
		_, err := tx.Exec(`INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES (?, 'cookie', ?, ?, ?) ON CONFLICT(hash, ban_type) DO UPDATE SET reason=excluded.reason, expires_at=excluded.expires_at`,
			cookieHash, reason, utils.GetSQLTime(), expiresAt)
		if err != nil {
			logger.Error("Failed to apply Cookie ban", "hash", cookieHash, "error", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error applying cookie ban."}, app)
			return
		}
		if err := database.LogModAction(tx, modHash, "apply_ban", 0, fmt.Sprintf("Cookie Hash: %s, Reason: %s", cookieHash, reason)); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error logging ban."}, app)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		logger.Error("Failed to commit ban transaction", "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error applying ban."}, app)
		return
	}
	logger.Info("Ban applied", "reason", reason, "ip_hash", ipHash, "cookie_hash", cookieHash)
	respondJSON(w, http.StatusOK, map[string]string{"success": "Ban successfully applied."}, app)
}

func HandleToggleSticky(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleToggleSticky")
	threadID, err := strconv.ParseInt(r.FormValue("thread_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid thread ID.", http.StatusBadRequest)
		return
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE threads SET sticky = NOT sticky WHERE id = ?", threadID); err != nil {
		logger.Error("Failed to toggle sticky", "thread_id", threadID, "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "toggle_sticky", threadID, ""); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)
}

func HandleToggleLock(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleToggleLock")
	threadID, err := strconv.ParseInt(r.FormValue("thread_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid thread ID.", http.StatusBadRequest)
		return
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE threads SET locked = NOT locked WHERE id = ?", threadID); err != nil {
		logger.Error("Failed to toggle lock", "thread_id", threadID, "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "toggle_lock", threadID, ""); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)
}

func HandleBanList(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleBanList")
	now := utils.GetSQLTime()
	var activeBans []models.Ban
	var expiredBans []models.Ban
	activeRows, err := app.DB().DB.Query("SELECT id, hash, ban_type, reason, created_at, expires_at FROM bans WHERE expires_at IS NULL OR expires_at > ? ORDER BY created_at DESC", now)
	if err != nil {
		logger.Error("Failed to query active ban list", "error", err)
	} else {
		defer activeRows.Close()
		for activeRows.Next() {
			var ban models.Ban
			if err := activeRows.Scan(&ban.ID, &ban.Hash, &ban.BanType, &ban.Reason, &ban.CreatedAt, &ban.ExpiresAt); err != nil {
				logger.Error("Failed to scan active ban row", "error", err)
			} else {
				activeBans = append(activeBans, ban)
			}
		}
		if err := activeRows.Err(); err != nil {
			logger.Error("Row error scanning active ban list", "error", err)
		}
	}
	expiredRows, err := app.DB().DB.Query("SELECT id, hash, ban_type, reason, created_at, expires_at FROM bans WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY expires_at DESC", now)
	if err != nil {
		logger.Error("Failed to query expired ban list", "error", err)
	} else {
		defer expiredRows.Close()
		for expiredRows.Next() {
			var ban models.Ban
			if err := expiredRows.Scan(&ban.ID, &ban.Hash, &ban.BanType, &ban.Reason, &ban.CreatedAt, &ban.ExpiresAt); err != nil {
				logger.Error("Failed to scan expired ban row", "error", err)
			} else {
				expiredBans = append(expiredBans, ban)
			}
		}
		if err := expiredRows.Err(); err != nil {
			logger.Error("Row error scanning expired ban list", "error", err)
		}
	}
	render(w, r, app, "mod_layout.html", "banlist.html", map[string]interface{}{
		"Title":       "Ban List",
		"ActiveBans":  activeBans,
		"ExpiredBans": expiredBans,
	})
}

func HandleRemoveBan(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleRemoveBan")
	banID, err := strconv.ParseInt(r.FormValue("ban_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid ban ID.", http.StatusBadRequest)
		return
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM bans WHERE id = ?", banID); err != nil {
		logger.Error("Failed to remove ban", "ban_id", banID, "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "remove_ban", banID, ""); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	logger.Info("Ban removed by moderator", "ban_id", banID)
	http.Redirect(w, r, "/mod/bans", http.StatusSeeOther)
}

func HandleIPLookup(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleIPLookup")
	ipHash := r.URL.Query().Get("ip_hash")
	var posts []models.Post
	rows, err := app.DB().DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts WHERE ip_hash = ? ORDER BY id DESC", ipHash)
	if err != nil {
		logger.Error("Failed to look up posts for IP hash", "ip_hash", ipHash, "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p models.Post
			if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
				logger.Error("Failed to scan post for IP lookup", "error", err)
			} else {
				posts = append(posts, p)
			}
		}
		if err := rows.Err(); err != nil {
			logger.Error("Row error during IP lookup", "error", err)
		}
	}
	render(w, r, app, "mod_layout.html", "iplookup.html", map[string]interface{}{"Title": "IP Lookup", "IPHash": ipHash, "Posts": posts})
}

func HandleCookieLookup(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleCookieLookup")
	cookieHash := r.URL.Query().Get("cookie_hash")
	var posts []models.Post
	rows, err := app.DB().DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts WHERE cookie_hash = ? ORDER BY id DESC", cookieHash)
	if err != nil {
		logger.Error("Failed to look up posts for cookie hash", "cookie_hash", cookieHash, "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p models.Post
			if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
				logger.Error("Failed to scan post for cookie lookup", "error", err)
			} else {
				posts = append(posts, p)
			}
		}
		if err := rows.Err(); err != nil {
			logger.Error("Row error during cookie lookup", "error", err)
		}
	}
	render(w, r, app, "mod_layout.html", "cookielookup.html", map[string]interface{}{"Title": "Cookie Lookup", "CookieHash": cookieHash, "Posts": posts})
}

func HandleUnifiedLookup(w http.ResponseWriter, r *http.Request, app App) {
	ipHash := r.URL.Query().Get("ip_hash")
	cookieHash := r.URL.Query().Get("cookie_hash")
	type LookupResult struct {
		Title string
		Posts []models.Post
	}
	results := make(map[string]LookupResult)
	if ipHash != "" {
		var posts []models.Post
		rows, err := app.DB().DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts WHERE ip_hash = ? ORDER BY id DESC", ipHash)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var p models.Post
				if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err == nil {
					posts = append(posts, p)
				}
			}
		}
		results["IP"] = LookupResult{Title: "Posts by IP Hash: " + ipHash, Posts: posts}
	}
	if cookieHash != "" {
		var posts []models.Post
		rows, err := app.DB().DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts WHERE cookie_hash = ? ORDER BY id DESC", cookieHash)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var p models.Post
				if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err == nil {
					posts = append(posts, p)
				}
			}
		}
		results["Cookie"] = LookupResult{Title: "Posts by Cookie Hash: " + cookieHash, Posts: posts}
	}
	render(w, r, app, "mod_layout.html", "lookup.html", map[string]interface{}{
		"Title":      "Unified Lookup",
		"IPHash":     ipHash,
		"CookieHash": cookieHash,
		"Results":    results,
	})
}

func HandleResolveReport(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleResolveReport")
	reportID, err := strconv.ParseInt(r.FormValue("report_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid report ID.", http.StatusBadRequest)
		return
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE reports SET resolved = 1 WHERE id = ?", reportID); err != nil {
		logger.Error("Failed to resolve report", "report_id", reportID, "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "resolve_report", reportID, ""); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}

func HandleCreateBoard(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleCreateBoard")
	id := strings.ToLower(r.FormValue("id"))
	name := r.FormValue("name")
	description := r.FormValue("description")
	maxThreads, _ := strconv.Atoi(r.FormValue("max_threads"))
	bumpLimit, _ := strconv.Atoi(r.FormValue("bump_limit"))
	imageRequired := r.FormValue("image_required") == "on"
	colorScheme := r.FormValue("color_scheme")
	categoryID, _ := strconv.Atoi(r.FormValue("category_id"))
	password := r.FormValue("password")
	requirePass := r.FormValue("require_pass") == "on"
	modHash := utils.HashIP(utils.GetIPAddress(r))
	if categoryID == 0 {
		categoryID = 1
	}
	reserved := map[string]bool{"mod": true, "search": true, "about": true, "static": true, "uploads": true, "post": true, "delete": true, "report": true, "api": true}
	if reserved[id] || !regexp.MustCompile(`^[a-z0-9]{1,10}$`).MatchString(id) {
		http.Error(w, "Invalid or reserved Board ID.", http.StatusBadRequest)
		return
	}
	var hashedPass string
	if password != "" {
		hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			logger.Error("Failed to hash password for new board", "board_id", id, "error", err)
			http.Error(w, "Failed to process password", http.StatusInternalServerError)
			return
		}
		hashedPass = string(hashedBytes)
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO boards (id, name, description, max_threads, bump_limit, image_required, color_scheme, created, category_id, password, require_pass) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, description, maxThreads, bumpLimit, imageRequired, colorScheme, utils.GetSQLTime(), categoryID, hashedPass, requirePass)
	if err != nil {
		logger.Error("Failed to create board", "board_id", id, "error", err)
		http.Error(w, "Failed to create board: "+err.Error(), http.StatusInternalServerError)
		return
	}
	details, _ := json.Marshal(map[string]interface{}{"id": id, "name": name})
	if err := database.LogModAction(tx, modHash, "create_board", 0, string(details)); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	app.DB().ClearBoardCache(id)
	ClearBoardListCache()
	logger.Info("Board created by moderator", "board_id", id, "name", name)
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}

func HandleModLog(w http.ResponseWriter, r *http.Request, app App) {
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	pageSize := 50
	offset := (page - 1) * pageSize
	var totalLogs int
	app.DB().DB.QueryRow("SELECT COUNT(*) FROM mod_actions").Scan(&totalLogs)
	rows, err := app.DB().DB.Query("SELECT id, timestamp, moderator_hash, action, target_id, details FROM mod_actions ORDER BY timestamp DESC LIMIT ? OFFSET ?", pageSize, offset)
	if err != nil {
		http.Error(w, "Failed to retrieve log.", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var logs []models.ModAction
	for rows.Next() {
		var log models.ModAction
		if err := rows.Scan(&log.ID, &log.Timestamp, &log.ModeratorHash, &log.Action, &log.TargetID, &log.Details); err == nil {
			logs = append(logs, log)
		}
	}
	totalPages := int(math.Ceil(float64(totalLogs) / float64(pageSize)))
	render(w, r, app, "mod_layout.html", "modlog.html", map[string]interface{}{
		"Title":      "Moderator Log",
		"Logs":       logs,
		"Pagination": generatePagination(page, totalPages),
	})
}

func HandleBanner(w http.ResponseWriter, r *http.Request, app App) {
	if r.Method == http.MethodPost {
		content := r.FormValue("banner_content")
		if err := utils.WriteBanner(app.BannerFile(), content); err != nil {
			http.Error(w, "Failed to write banner file.", http.StatusInternalServerError)
			return
		}
		tx, err := app.DB().DB.Begin()
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "update_banner", 0, content); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/mod/", http.StatusSeeOther)
		return
	}
	content, _ := utils.ReadBanner(app.BannerFile())
	render(w, r, app, "mod_layout.html", "mod_banner.html", map[string]interface{}{
		"Title":         "Edit Global Banner",
		"BannerContent": content,
	})
}

func HandleDatabaseBackup(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleDatabaseBackup")
	backupPath, err := app.DB().BackupDatabase()
	if err != nil {
		logger.Error("Failed to create database backup", "error", err)
		http.Error(w, "Failed to create database backup: "+err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Info("Database backup created successfully", "path", backupPath)
	tx, err := app.DB().DB.Begin()
	if err != nil {
		http.Error(w, "Database error logging action", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "database_backup", 0, backupPath); err != nil {
		http.Error(w, "Database error logging action", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Database error logging action", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}
