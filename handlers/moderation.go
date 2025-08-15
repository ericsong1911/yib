// yib/handlers/moderation.go

package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
	var reports []models.Report
	rows, err := app.DB().DB.Query(`
        SELECT r.id, r.reason, r.ip_hash, r.created_at, p.id, p.board_id, p.thread_id
        FROM reports r JOIN posts p ON r.post_id = p.id
        WHERE r.resolved = 0 ORDER BY r.created_at DESC LIMIT 10`)
	if err != nil {
		log.Printf("ERROR: Failed to query for active reports: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var rep models.Report
			if err := rows.Scan(&rep.ID, &rep.Reason, &rep.IPHash, &rep.CreatedAt, &rep.Post.ID, &rep.Post.BoardID, &rep.Post.ThreadID); err != nil {
				log.Printf("ERROR: Failed to scan report row: %v", err)
			} else {
				reports = append(reports, rep)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning reports: %v", err)
		}
	}

	var recentPosts []models.Post
	rows, err = app.DB().DB.Query(`SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts ORDER BY id DESC LIMIT 15`)
	if err != nil {
		log.Printf("ERROR: Failed to query for recent posts: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p models.Post
			if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
				log.Printf("ERROR: Failed to scan recent post row: %v", err)
			} else {
				recentPosts = append(recentPosts, p)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning recent posts: %v", err)
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
		log.Printf("ERROR: Failed to query categories for mod panel: %v", err)
	} else {
		defer catRows.Close()
		for catRows.Next() {
			var cat models.Category
			if err := catRows.Scan(&cat.ID, &cat.Name, &cat.SortOrder); err != nil {
				log.Printf("ERROR: Failed to scan category row: %v", err)
			} else {
				allCategories = append(allCategories, &cat)
			}
		}
		if err := catRows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning categories: %v", err)
		}
	}

	boardRows, err := app.DB().DB.Query(`
		SELECT b.id, b.name, b.require_pass, b.category_id, b.sort_order, COUNT(t.id) as thread_count
		FROM boards b LEFT JOIN threads t ON b.id = t.board_id AND t.archived = 0
		GROUP BY b.id ORDER BY b.sort_order, b.name`)
	if err != nil {
		log.Printf("ERROR: Failed to query boards for mod panel: %v", err)
	} else {
		defer boardRows.Close()
		for boardRows.Next() {
			var board ManagedBoard
			if err := boardRows.Scan(&board.ID, &board.Name, &board.RequirePass, &board.CategoryID, &board.SortOrder, &board.ThreadCount); err != nil {
				log.Printf("ERROR: Failed to scan managed board row: %v", err)
			} else {
				allBoards = append(allBoards, board)
			}
		}
		if err := boardRows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning managed boards: %v", err)
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

// HandleEditBoard shows the form to edit a board's details or processes the update.
func HandleEditBoard(w http.ResponseWriter, r *http.Request, app App) {
	if r.Method == http.MethodPost {
		boardID := r.FormValue("board_id")
		name := r.FormValue("name")
		description := r.FormValue("description")
		maxThreads, _ := strconv.Atoi(r.FormValue("max_threads")) // Ignoring error is acceptable for non-critical number fields
		bumpLimit, _ := strconv.Atoi(r.FormValue("bump_limit"))
		imageRequired := r.FormValue("image_required") == "on"
		colorScheme := r.FormValue("color_scheme")
		password := r.FormValue("password")
		require := r.FormValue("require") == "on"

		tx, err := app.DB().DB.Begin()
		if err != nil {
			log.Printf("ERROR: Failed to start transaction for board edit: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		if password != "" {
			hashedPass, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				log.Printf("ERROR: Failed to hash new board password for /%s/: %v", boardID, err)
				http.Error(w, "Failed to process new password", http.StatusInternalServerError)
				return
			}
			if _, err := tx.Exec(`UPDATE boards SET name=?, description=?, max_threads=?, bump_limit=?, image_required=?, color_scheme=?, password=?, require_pass=? WHERE id = ?`,
				name, description, maxThreads, bumpLimit, imageRequired, colorScheme, string(hashedPass), require, boardID); err != nil {
				log.Printf("ERROR: Failed to update board with new password for /%s/: %v", boardID, err)
				http.Error(w, "Database error updating board", http.StatusInternalServerError)
				return
			}
		} else {
			if _, err := tx.Exec(`UPDATE boards SET name=?, description=?, max_threads=?, bump_limit=?, image_required=?, color_scheme=?, require_pass=? WHERE id = ?`,
				name, description, maxThreads, bumpLimit, imageRequired, colorScheme, require, boardID); err != nil {
				log.Printf("ERROR: Failed to update board for /%s/: %v", boardID, err)
				http.Error(w, "Database error updating board", http.StatusInternalServerError)
				return
			}
		}

		if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "edit_board", 0, boardID); err != nil {
			log.Printf("ERROR: Failed to log board edit for /%s/: %v", boardID, err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			log.Printf("ERROR: Failed to commit board edit for /%s/: %v", boardID, err)
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
		log.Printf("ERROR: Mod tried to edit non-existent board /%s/: %v", boardID, err)
		http.Error(w, "Board not found.", http.StatusNotFound)
		return
	}
	render(w, r, app, "mod_layout.html", "mod_edit_board.html", map[string]interface{}{
		"Title": "Edit Board", "Board": boardConfig,
	})
}

// HandleDeleteBoard permanently deletes a board and all its content.
func HandleDeleteBoard(w http.ResponseWriter, r *http.Request, app App) {
	boardID := r.FormValue("board_id")
	modHash := utils.HashIP(utils.GetIPAddress(r))
	if err := app.DB().DeleteBoard(boardID, app.UploadDir(), modHash); err != nil {
		log.Printf("ERROR: Failed to delete board /%s/: %v", boardID, err)
		http.Error(w, "Failed to delete board: "+err.Error(), 500)
		return
	}
	app.DB().ClearBoardCache(boardID)
	ClearBoardListCache()
	log.Printf("INFO: Board /%s/ was deleted by a moderator.", boardID)
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}

// HandleManageCategories updates category and board sorting, names, and assignments.
func HandleManageCategories(w http.ResponseWriter, r *http.Request, app App) {
	if err := r.ParseForm(); err != nil {
		log.Printf("ERROR: Failed to parse form for category management: %v", err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		log.Printf("ERROR: Could not begin transaction for category management: %v", err)
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
				if name == "" { // Delete category
					if _, err := tx.Exec("UPDATE boards SET category_id = 1 WHERE category_id = ?", id); err != nil {
						log.Printf("ERROR: Failed to reassign boards from deleted category %d: %v", id, err)
					}
					if _, err := tx.Exec("DELETE FROM categories WHERE id = ? AND id != 1", id); err != nil {
						log.Printf("ERROR: Failed to delete category %d: %v", id, err)
					}
				} else { // Update category
					if _, err := tx.Exec("UPDATE categories SET name = ?, sort_order = ? WHERE id = ?", name, order, id); err != nil {
						log.Printf("ERROR: Failed to update category %d: %v", id, err)
					}
				}
			}
		} else if strings.HasPrefix(key, "board_cat-") {
			boardID := strings.TrimPrefix(key, "board_cat-")
			catID, _ := strconv.Atoi(values[0])
			order, _ := strconv.Atoi(r.FormValue("board_order-" + boardID))
			if _, err := tx.Exec("UPDATE boards SET category_id = ?, sort_order = ? WHERE id = ?", catID, order, boardID); err != nil {
				log.Printf("ERROR: Failed to update board /%s/ category/sort: %v", boardID, err)
			}
		}
	}
	if newCatName := r.FormValue("name-0"); newCatName != "" {
		newCatOrder, _ := strconv.Atoi(r.FormValue("order-0"))
		if _, err := tx.Exec("INSERT INTO categories (name, sort_order) VALUES (?, ?)", newCatName, newCatOrder); err != nil {
			log.Printf("ERROR: Failed to create new category '%s': %v", newCatName, err)
		}
	}

	if err := database.LogModAction(tx, utils.HashIP(utils.GetIPAddress(r)), "manage_categories", 0, "Updated categories and board sorting"); err != nil {
		log.Printf("ERROR: Failed to log category management: %v", err)
		http.Error(w, "Database error saving changes", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("ERROR: Failed to commit category management transaction: %v", err)
		http.Error(w, "Database error saving changes", http.StatusInternalServerError)
		return
	}
	app.DB().ClearAllBoardCaches()
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}

// HandleModDelete allows a moderator to delete any post.
func HandleModDelete(w http.ResponseWriter, r *http.Request, app App) {
	postID, err := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid post ID.", http.StatusBadRequest)
		return
	}
	modHash := utils.HashIP(utils.GetIPAddress(r))
	details := fmt.Sprintf("Deleted post %d", postID)

	boardID, isOp, err := app.DB().DeletePost(postID, app.UploadDir(), modHash, details)
	if err != nil {
		log.Printf("ERROR: Mod failed to delete post %d: %v", postID, err)
		http.Error(w, "Failed to delete post.", http.StatusInternalServerError)
		return
	}
	log.Printf("INFO: Post %d was deleted by a moderator.", postID)
	if isOp {
		http.Redirect(w, r, "/"+boardID+"/", http.StatusSeeOther)
	} else {
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)
	}
}

// HandleBan applies a ban to an IP and/or cookie hash.
func HandleBan(w http.ResponseWriter, r *http.Request, app App) {
	ipHash := r.FormValue("ip_hash")
	cookieHash := r.FormValue("cookie_hash")
	reason := r.FormValue("reason")
	durationHours, _ := strconv.Atoi(r.FormValue("duration"))
	modHash := utils.HashIP(utils.GetIPAddress(r))

	if ipHash == "" && cookieHash == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "No hash provided to ban."})
		return
	}
	if reason == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "A ban reason is required."})
		return
	}

	var expiresAt sql.NullTime
	if durationHours > 0 {
		expiresAt.Time = utils.GetSQLTime().Add(time.Duration(durationHours) * time.Hour)
		expiresAt.Valid = true
	}
	tx, err := app.DB().DB.Begin()
	if err != nil {
		log.Printf("ERROR: Could not begin transaction for ban: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}
	defer tx.Rollback()

	if ipHash != "" {
		_, err := tx.Exec(`INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES (?, 'ip', ?, ?, ?) ON CONFLICT(hash, ban_type) DO UPDATE SET reason=excluded.reason, expires_at=excluded.expires_at`,
			ipHash, reason, utils.GetSQLTime(), expiresAt)
		if err != nil {
			log.Printf("ERROR: Failed to apply IP ban to hash %s: %v", ipHash, err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error applying IP ban."})
			return
		}
		if err := database.LogModAction(tx, modHash, "apply_ban", 0, fmt.Sprintf("IP Hash: %s, Reason: %s", ipHash, reason)); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error logging ban."})
			return
		}
	}
	if cookieHash != "" {
		_, err := tx.Exec(`INSERT INTO bans (hash, ban_type, reason, created_at, expires_at) VALUES (?, 'cookie', ?, ?, ?) ON CONFLICT(hash, ban_type) DO UPDATE SET reason=excluded.reason, expires_at=excluded.expires_at`,
			cookieHash, reason, utils.GetSQLTime(), expiresAt)
		if err != nil {
			log.Printf("ERROR: Failed to apply Cookie ban to hash %s: %v", cookieHash, err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error applying cookie ban."})
			return
		}
		if err := database.LogModAction(tx, modHash, "apply_ban", 0, fmt.Sprintf("Cookie Hash: %s, Reason: %s", cookieHash, reason)); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error logging ban."})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("ERROR: Failed to commit ban transaction: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error applying ban."})
		return
	}
	log.Printf("INFO: Ban applied for reason '%s' to IP hash (%s) and/or Cookie hash (%s)", reason, ipHash, cookieHash)
	respondJSON(w, http.StatusOK, map[string]string{"success": "Ban successfully applied."})
}

// HandleToggleSticky toggles a thread's sticky status.
func HandleToggleSticky(w http.ResponseWriter, r *http.Request, app App) {
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
		log.Printf("ERROR: Failed to toggle sticky for thread %d: %v", threadID, err)
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

// HandleToggleLock toggles a thread's locked status.
func HandleToggleLock(w http.ResponseWriter, r *http.Request, app App) {
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
		log.Printf("ERROR: Failed to toggle lock for thread %d: %v", threadID, err)
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

// HandleBanList shows all active and expired bans.
func HandleBanList(w http.ResponseWriter, r *http.Request, app App) {
	now := utils.GetSQLTime()
	var activeBans []models.Ban
	var expiredBans []models.Ban

	// Query for active bans
	activeRows, err := app.DB().DB.Query("SELECT id, hash, ban_type, reason, created_at, expires_at FROM bans WHERE expires_at IS NULL OR expires_at > ? ORDER BY created_at DESC", now)
	if err != nil {
		log.Printf("ERROR: Failed to query active ban list: %v", err)
	} else {
		defer activeRows.Close()
		for activeRows.Next() {
			var ban models.Ban
			if err := activeRows.Scan(&ban.ID, &ban.Hash, &ban.BanType, &ban.Reason, &ban.CreatedAt, &ban.ExpiresAt); err != nil {
				log.Printf("ERROR: Failed to scan active ban row: %v", err)
			} else {
				activeBans = append(activeBans, ban)
			}
		}
		if err := activeRows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning active ban list: %v", err)
		}
	}

	// Query for expired bans
	expiredRows, err := app.DB().DB.Query("SELECT id, hash, ban_type, reason, created_at, expires_at FROM bans WHERE expires_at IS NOT NULL AND expires_at <= ? ORDER BY expires_at DESC", now)
	if err != nil {
		log.Printf("ERROR: Failed to query expired ban list: %v", err)
	} else {
		defer expiredRows.Close()
		for expiredRows.Next() {
			var ban models.Ban
			if err := expiredRows.Scan(&ban.ID, &ban.Hash, &ban.BanType, &ban.Reason, &ban.CreatedAt, &ban.ExpiresAt); err != nil {
				log.Printf("ERROR: Failed to scan expired ban row: %v", err)
			} else {
				expiredBans = append(expiredBans, ban)
			}
		}
		if err := expiredRows.Err(); err != nil {
			log.Printf("ERROR: Row error scanning expired ban list: %v", err)
		}
	}

	render(w, r, app, "mod_layout.html", "banlist.html", map[string]interface{}{
		"Title":       "Ban List",
		"ActiveBans":  activeBans,
		"ExpiredBans": expiredBans,
	})
}

// HandleRemoveBan lifts a ban.
func HandleRemoveBan(w http.ResponseWriter, r *http.Request, app App) {
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
		log.Printf("ERROR: Failed to remove ban %d: %v", banID, err)
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

	log.Printf("INFO: Ban ID %d was removed by a moderator.", banID)
	http.Redirect(w, r, "/mod/bans", http.StatusSeeOther)
}

// HandleIPLookup shows all posts from a given IP hash.
func HandleIPLookup(w http.ResponseWriter, r *http.Request, app App) {
	ipHash := r.URL.Query().Get("ip_hash")
	var posts []models.Post
	rows, err := app.DB().DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts WHERE ip_hash = ? ORDER BY id DESC", ipHash)
	if err != nil {
		log.Printf("ERROR: Failed to look up posts for IP hash %s: %v", ipHash, err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p models.Post
			if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
				log.Printf("ERROR: Failed to scan post for IP lookup: %v", err)
			} else {
				posts = append(posts, p)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR: Row error during IP lookup: %v", err)
		}
	}
	render(w, r, app, "mod_layout.html", "iplookup.html", map[string]interface{}{"Title": "IP Lookup", "IPHash": ipHash, "Posts": posts})
}

// HandleCookieLookup shows all posts from a given cookie hash.
func HandleCookieLookup(w http.ResponseWriter, r *http.Request, app App) {
	cookieHash := r.URL.Query().Get("cookie_hash")
	var posts []models.Post
	rows, err := app.DB().DB.Query("SELECT id, board_id, thread_id, name, tripcode, content, timestamp, ip_hash, cookie_hash FROM posts WHERE cookie_hash = ? ORDER BY id DESC", cookieHash)
	if err != nil {
		log.Printf("ERROR: Failed to look up posts for cookie hash %s: %v", cookieHash, err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var p models.Post
			if err := rows.Scan(&p.ID, &p.BoardID, &p.ThreadID, &p.Name, &p.Tripcode, &p.Content, &p.Timestamp, &p.IPHash, &p.CookieHash); err != nil {
				log.Printf("ERROR: Failed to scan post for cookie lookup: %v", err)
			} else {
				posts = append(posts, p)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR: Row error during cookie lookup: %v", err)
		}
	}
	render(w, r, app, "mod_layout.html", "cookielookup.html", map[string]interface{}{"Title": "Cookie Lookup", "CookieHash": cookieHash, "Posts": posts})
}

// HandleUnifiedLookup shows post history for both an IP and Cookie hash.
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

// HandleResolveReport marks a report as resolved.
func HandleResolveReport(w http.ResponseWriter, r *http.Request, app App) {
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
		log.Printf("ERROR: Failed to resolve report %d: %v", reportID, err)
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

// HandleCreateBoard creates a new board.
func HandleCreateBoard(w http.ResponseWriter, r *http.Request, app App) {
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
		categoryID = 1 // Default to 'General' if something goes wrong
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
			log.Printf("ERROR: Failed to hash password for new board /%s/: %v", id, err)
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
		log.Printf("ERROR: Failed to create board /%s/: %v", id, err)
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
	log.Printf("INFO: Board /%s/ - '%s' was created by a moderator.", id, name)
	http.Redirect(w, r, "/mod/", http.StatusSeeOther)
}

// HandleModLog displays the moderator action log.
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

// HandleBanner allows a moderator to set a global banner.
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