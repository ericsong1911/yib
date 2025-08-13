// yib/handlers/actions.go

package handlers

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"html/template"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"yib/config"
	"yib/utils"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// HandlePost is the main handler for creating new threads and replies.
func HandlePost(w http.ResponseWriter, r *http.Request, app App) {
	if err := r.ParseMultipartForm(config.MaxFileSize + 1024); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Form parsing error: " + err.Error()})
		return
	}
	boardID := r.FormValue("board_id")
	threadIDStr := r.FormValue("thread_id")

	ip := utils.GetIPAddress(r)
	ipHash := utils.HashIP(ip)
	cookieID := r.Context().Value(UserCookieKey).(string)
	cookieHash := utils.HashIP(cookieID)

	if ban, isBanned := app.DB().GetBanDetails(ipHash, cookieHash); isBanned {
		expiryText := "This ban is permanent."
		if ban.ExpiresAt.Valid {
			isoTime := ban.ExpiresAt.Time.Format(time.RFC3339)
			utcTime := ban.ExpiresAt.Time.Format("01/02/06(Mon)15:04:05")
			expiryText = fmt.Sprintf(`This ban will expire on <time class="post-time" datetime="%s">%s UTC</time>.`, isoTime, utcTime)
		}
		errMsg := fmt.Sprintf("You are banned. Reason: %s. %s", template.HTMLEscapeString(ban.Reason), expiryText)
		respondJSON(w, http.StatusForbidden, map[string]string{"error": errMsg})
		return
	}
	if !app.RateLimiter().GetLimiter(ip).Allow() {
		respondJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Rate limit exceeded. Please wait a moment."})
		return
	}
	if !app.Challenges().Verify(r.FormValue("challenge_token"), r.FormValue("challenge_answer")) {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "Invalid challenge answer. Please try again."})
		return
	}
	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Board not found."})
		return
	}

	name := r.FormValue("name")
	subject := r.FormValue("subject")
	content := r.FormValue("content")
	if len(name) > config.MaxNameLen || len(subject) > config.MaxSubjectLen || len(content) > config.MaxCommentLen {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "A form field exceeds the maximum length."})
		return
	}

	imagePath, imageHash, hasImage, err := processImage(r, app)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Image processing failed: " + err.Error()})
		return
	}
	if strings.TrimSpace(content) == "" && !hasImage {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Post must have content or an image."})
		return
	}

	displayName, tripcode := utils.GenerateTripcode(name)
	tx, err := app.DB().DB.Begin()
	if err != nil {
		log.Printf("ERROR: Could not begin transaction: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error."})
		return
	}
	defer tx.Rollback()

	var newPostID int64
	var redirectURL string
	if threadIDStr == "" || threadIDStr == "0" { // New thread
		if boardConfig.ImageRequired && !hasImage {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Image required for new threads on this board."})
			return
		}
		res, err := tx.Exec("INSERT INTO threads (board_id, subject, bump, reply_count, image_count) VALUES (?, ?, ?, 0, ?)", boardID, subject, utils.GetSQLTime(), utils.BtoI(hasImage))
		if err != nil {
			log.Printf("ERROR: API: Failed to insert new thread: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error creating thread."})
			return
		}
		threadID, _ := res.LastInsertId()
		res, err = tx.Exec("INSERT INTO posts (board_id, thread_id, name, tripcode, content, image_path, image_hash, timestamp, ip_hash, cookie_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", boardID, threadID, displayName, tripcode, processContent(content), imagePath, imageHash, utils.GetSQLTime(), ipHash, cookieHash)
		if err != nil {
			log.Printf("ERROR: API: Failed to insert new OP: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error creating post."})
			return
		}
		newPostID, _ = res.LastInsertId()
		redirectURL = fmt.Sprintf("/%s/thread/%d", boardID, threadID)
		go archiveOldThreads(app, boardID)
	} else { // New reply
		threadID, _ := strconv.ParseInt(threadIDStr, 10, 64)
		var locked bool
		var replyCount int
		if err := tx.QueryRow("SELECT locked, reply_count FROM threads WHERE id = ?", threadID).Scan(&locked, &replyCount); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Thread not found."})
			return
		}
		if locked {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "Thread is locked."})
			return
		}
		res, err := tx.Exec("INSERT INTO posts (board_id, thread_id, name, tripcode, content, image_path, image_hash, timestamp, ip_hash, cookie_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", boardID, threadID, displayName, tripcode, processContent(content), imagePath, imageHash, utils.GetSQLTime(), ipHash, cookieHash)
		if err != nil {
			log.Printf("ERROR: API: Failed to insert reply: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error creating reply."})
			return
		}

		newPostID, _ = res.LastInsertId()

		var updateErr error
		if !strings.Contains(strings.ToLower(displayName), "sage") && replyCount < boardConfig.BumpLimit {
			_, updateErr = tx.Exec("UPDATE threads SET reply_count = reply_count + 1, image_count = image_count + ?, bump = ? WHERE id = ?", utils.BtoI(hasImage), utils.GetSQLTime(), threadID)
		} else {
			_, updateErr = tx.Exec("UPDATE threads SET reply_count = reply_count + 1, image_count = image_count + ? WHERE id = ?", utils.BtoI(hasImage), threadID)
		}
		if updateErr != nil {
			log.Printf("ERROR: API: Failed to update thread metadata: %v", updateErr)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error updating thread."})
			return
		}
		redirectURL = fmt.Sprintf("/%s/thread/%d#p%d", boardID, threadID, newPostID)
	}

	re := regexp.MustCompile(`>>(\d+)`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) > 0 {
		seen := make(map[string]bool)
		for _, match := range matches {
			if !seen[match[1]] {
				if targetID, err := strconv.ParseInt(match[1], 10, 64); err == nil && targetID > 0 {
					if _, err := tx.Exec("INSERT OR IGNORE INTO backlinks (source_post_id, target_post_id) VALUES (?, ?)", newPostID, targetID); err != nil {
						log.Printf("WARNING: Failed to insert backlink from %d to %d: %v", newPostID, targetID, err)
					}
				}
				seen[match[1]] = true
			}
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("ERROR: Failed to commit transaction for new post: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error saving post."})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"redirect": redirectURL})
}

// HandleCookieDelete allows a user to delete their own post.
func HandleCookieDelete(w http.ResponseWriter, r *http.Request, app App) {
	postID, err := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid post ID.", http.StatusBadRequest)
		return
	}

	currentUserCookieID := r.Context().Value(UserCookieKey).(string)
	currentUserCookieHash := utils.HashIP(currentUserCookieID)

	var postCookieHash, boardID string
	var threadID int64
	if err := app.DB().DB.QueryRow("SELECT cookie_hash, board_id, thread_id FROM posts WHERE id = ?", postID).Scan(&postCookieHash, &boardID, &threadID); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Post not found.", http.StatusNotFound)
			return
		}
		log.Printf("ERROR: DB error checking post ownership for post %d: %v", postID, err)
		http.Error(w, "Database error.", http.StatusInternalServerError)
		return
	}

	if currentUserCookieHash != postCookieHash {
		log.Printf("SECURITY: User with cookie hash %s failed to delete post %d owned by %s", currentUserCookieHash, postID, postCookieHash)
		http.Error(w, "You do not have permission to delete this post.", http.StatusForbidden)
		return
	}

	// Provide empty strings for modHash and details, as this is a user action, not a mod action.
	_, isOp, err := app.DB().DeletePost(postID, app.UploadDir(), "", "")
	if err != nil {
		log.Printf("ERROR: Failed to delete post %d by user request: %v", postID, err)
		http.Error(w, "Failed to delete post.", http.StatusInternalServerError)
		return
	}

	if isOp {
		http.Redirect(w, r, "/"+boardID+"/", http.StatusSeeOther)
	} else {
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)
	}
}

// HandleReport allows a user to report a post for moderation.
func HandleReport(w http.ResponseWriter, r *http.Request, app App) {
	ipHash := utils.HashIP(utils.GetIPAddress(r))
	cookieHash := utils.HashIP(r.Context().Value(UserCookieKey).(string))

	if _, isBanned := app.DB().GetBanDetails(ipHash, cookieHash); isBanned {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "You are banned and cannot submit reports."})
		return
	}

	postID, err := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	if err != nil || postID == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid post ID provided."})
		return
	}
	reason := r.FormValue("reason")
	if reason == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Reason for reporting cannot be empty."})
		return
	}

	_, err = app.DB().DB.Exec("INSERT INTO reports (post_id, reason, ip_hash, created_at) VALUES (?, ?, ?, ?)", postID, reason, ipHash, utils.GetSQLTime())
	if err != nil {
		log.Printf("ERROR: Failed to insert new report for post %d: %v", postID, err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to submit report."})
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"success": "Report submitted successfully."})
}

// --- Internal Helper Functions ---

func processImage(r *http.Request, app App) (path, hashStr string, hasImage bool, err error) {
	file, _, err := r.FormFile("image")
	if err != nil {
		if err == http.ErrMissingFile {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("could not get form file: %w", err)
	}
	defer file.Close()

	limitedReader := &io.LimitedReader{R: file, N: config.MaxFileSize + 1}
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", "", false, fmt.Errorf("could not read file data: %w", err)
	}
	if limitedReader.N == 0 {
		return "", "", true, fmt.Errorf("file is larger than the %dMB limit", config.MaxFileSize/1024/1024)
	}
	if len(data) == 0 {
		return "", "", true, fmt.Errorf("file is empty")
	}

	hash := sha256.Sum256(data)
	hashStr = hex.EncodeToString(hash[:])
	var existingPath string
	err = app.DB().DB.QueryRow("SELECT image_path FROM posts WHERE image_hash = ?", hashStr).Scan(&existingPath)
	if err == nil {
		return existingPath, hashStr, true, nil // Found duplicate, success
	}
	if err != sql.ErrNoRows {
		log.Printf("ERROR: Failed to check for existing image hash: %v", err)
	}

	reader := bytes.NewReader(data)
	cfg, format, err := image.DecodeConfig(reader)
	if err != nil {
		return "", "", true, fmt.Errorf("invalid image format, could not decode config: %w", err)
	}
	if format != "jpeg" && format != "png" && format != "gif" && format != "webp" {
		return "", "", true, fmt.Errorf("unsupported image format: %s", format)
	}

	if cfg.Width > config.MaxWidth || cfg.Height > config.MaxHeight {
		return "", "", true, fmt.Errorf("image dimensions (%dx%d) exceed maximum (%dx%d)", cfg.Width, cfg.Height, config.MaxWidth, config.MaxHeight)
	}

	// Rewind the reader to be read again by the full decoder
	if _, err := reader.Seek(0, 0); err != nil {
		return "", "", true, fmt.Errorf("could not reset reader position: %w", err)
	}

	// Use imaging.Decode which automatically corrects orientation from EXIF data.
	img, err := imaging.Decode(reader, imaging.AutoOrientation(true))
	if err != nil {
		return "", "", true, fmt.Errorf("failed to decode image with orientation correction: %w", err)
	}

	outputFormat := format
	if format == "webp" || format == "gif" {
		outputFormat = "jpeg"
	}

	newFilename := fmt.Sprintf("%d_%s.%s", utils.GetTime().UnixNano(), hashStr[:12], outputFormat)
	outputPath := filepath.Join(app.UploadDir(), newFilename)
	out, err := os.Create(outputPath)
	if err != nil {
		return "", "", true, fmt.Errorf("could not create output file: %w", err)
	}
	defer out.Close()

	switch outputFormat {
	case "jpeg":
		// Use imaging.Encode which is a wrapper around the standard library
		err = imaging.Encode(out, img, imaging.JPEG, imaging.JPEGQuality(90))
	case "png":
		err = imaging.Encode(out, img, imaging.PNG)
	}
	if err != nil {
		if rerr := os.Remove(outputPath); rerr != nil {
			log.Printf("ERROR: Failed to remove corrupt file artifact %s: %v", outputPath, rerr)
		}
		return "", "", true, fmt.Errorf("failed to encode image to %s: %w", outputFormat, err)
	}
	return "/uploads/" + newFilename, hashStr, true, nil
}

func processContent(content string) string {
	escaped := template.HTMLEscapeString(content)
	reQuote := regexp.MustCompile(`&gt;&gt;(\d+)`)
	linked := reQuote.ReplaceAllString(escaped, `<a href="#p$1" class="backlink" data-post="$1">&gt;&gt;$1</a>`)
	lines := strings.Split(linked, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "&gt;") && !strings.HasPrefix(line, "&gt;&gt;") {
			lines[i] = `<span class="greentext">` + line + `</span>`
		}
	}
	return strings.Join(lines, "<br>")
}

func archiveOldThreads(app App, boardID string) {
	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		log.Printf("ERROR: Archiver failed to get board config for /%s/: %v", boardID, err)
		return
	}
	var threadCount int
	err = app.DB().DB.QueryRow("SELECT COUNT(*) FROM threads WHERE board_id = ? AND archived = 0", boardID).Scan(&threadCount)
	if err != nil {
		log.Printf("ERROR: Archiver failed to get thread count for /%s/: %v", boardID, err)
		return
	}
	if threadCount <= boardConfig.MaxThreads {
		return
	}
	toArchive := threadCount - boardConfig.MaxThreads
	rows, err := app.DB().DB.Query(`SELECT id FROM threads WHERE board_id = ? AND archived = 0 AND sticky = 0 ORDER BY bump ASC LIMIT ?`, boardID, toArchive)
	if err != nil {
		log.Printf("ERROR: Archiver failed to find threads to archive on /%s/: %v", boardID, err)
		return
	}
	defer rows.Close()

	var threadIDs []interface{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			threadIDs = append(threadIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: Archiver failed scanning rows for /%s/: %v", boardID, err)
	}

	if len(threadIDs) > 0 {
		query := "UPDATE threads SET archived = 1 WHERE id IN (?" + strings.Repeat(",?", len(threadIDs)-1) + ")"
		if _, err := app.DB().DB.Exec(query, threadIDs...); err != nil {
			log.Printf("ERROR: Archiver failed to execute update for /%s/: %v", boardID, err)
		} else {
			log.Printf("Archived %d threads from board /%s/", len(threadIDs), boardID)
		}
	}
}