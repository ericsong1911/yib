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
	_ "image/gif" // Import gif decoder
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"yib/config"
	"yib/models"
	"yib/utils"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// HandlePost is the main handler for creating new threads and replies.
func HandlePost(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandlePost")

	userInput := &models.FormInput{
		Name:    r.FormValue("name"),
		Subject: r.FormValue("subject"),
		Content: r.FormValue("content"),
	}

	if err := r.ParseMultipartForm(config.MaxFileSize + 1024); err != nil {
		if userInput.Content == "" {
			userInput.Content = r.FormValue("content")
		}
		logger.Warn("Form parsing error", "error", err)
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Form parsing error: " + err.Error()}, app)
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
		logger.Warn("Banned user tried to post", "ip_hash", ipHash, "cookie_hash", cookieHash)
		respondJSON(w, http.StatusForbidden, map[string]string{"error": errMsg}, app)
		return
	}
	if !app.RateLimiter().GetLimiter(ip).Allow() {
		logger.Warn("Rate limit exceeded", "ip", ip)
		respondJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Rate limit exceeded. Please wait a moment."}, app)
		return
	}
	if !app.Challenges().Verify(r.FormValue("challenge_token"), r.FormValue("challenge_answer")) {
		newToken, newQuestion := app.Challenges().GenerateChallenge()
		logger.Warn("Invalid challenge answer", "ip", ip)
		respondJSON(w, http.StatusForbidden, map[string]string{
			"error":       "Invalid challenge answer. Please try again.",
			"newToken":    newToken,
			"newQuestion": newQuestion,
		}, app)
		return
	}

	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		logger.Warn("User tried to post to non-existent board", "board_id", boardID)
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Board not found."}, app)
		return
	}

	name := r.FormValue("name")
	subject := r.FormValue("subject")
	content := r.FormValue("content")
	if len(name) > config.MaxNameLen || len(subject) > config.MaxSubjectLen || len(content) > config.MaxCommentLen {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "A form field exceeds the maximum length."}, app)
		return
	}

	// processImage now returns thumbnail path as well
	imagePath, thumbPath, imageHash, hasImage, err := processImage(r, app, logger)
	if err != nil {
		logger.Warn("Image processing failed", "error", err)
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Image processing failed: " + err.Error()}, app)
		return
	}
	if strings.TrimSpace(content) == "" && !hasImage {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Post must have content or an image."}, app)
		return
	}

	displayName, tripcode := utils.GenerateTripcode(name)
	isModerator := utils.IsModerator(r) // Check if poster is a mod

	tx, err := app.DB().DB.Begin()
	if err != nil {
		logger.Error("Could not begin transaction", "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error."}, app)
		return
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && rerr != sql.ErrTxDone {
			logger.Error("Failed to rollback transaction in HandlePost", "error", rerr)
		}
	}()

	var newPostID int64
	var redirectURL string
	if threadIDStr == "" || threadIDStr == "0" { // New thread
		if boardConfig.ImageRequired && !hasImage {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Image required for new threads on this board."}, app)
			return
		}
		res, err := tx.Exec("INSERT INTO threads (board_id, subject, bump, reply_count, image_count) VALUES (?, ?, ?, 0, ?)", boardID, subject, utils.GetSQLTime(), utils.BtoI(hasImage))
		if err != nil {
			logger.Error("Failed to insert new thread", "error", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error creating thread."}, app)
			return
		}
		threadID, _ := res.LastInsertId()
		// Insert new columns
		res, err = tx.Exec(`
			INSERT INTO posts (board_id, thread_id, name, tripcode, content, image_path, thumbnail_path, image_hash, timestamp, ip_hash, cookie_hash, is_moderator)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			boardID, threadID, displayName, tripcode, processContent(content), imagePath, thumbPath, imageHash, utils.GetSQLTime(), ipHash, cookieHash, isModerator)
		if err != nil {
			logger.Error("Failed to insert new OP", "error", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error creating post."}, app)
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
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Thread not found."}, app)
			return
		}
		if locked {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "Thread is locked."}, app)
			return
		}
		// Insert new columns
		res, err := tx.Exec(`
			INSERT INTO posts (board_id, thread_id, name, tripcode, content, image_path, thumbnail_path, image_hash, timestamp, ip_hash, cookie_hash, is_moderator)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			boardID, threadID, displayName, tripcode, processContent(content), imagePath, thumbPath, imageHash, utils.GetSQLTime(), ipHash, cookieHash, isModerator)
		if err != nil {
			logger.Error("Failed to insert reply", "error", err)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error creating reply."}, app)
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
			logger.Error("Failed to update thread metadata", "error", updateErr)
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error updating thread."}, app)
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
						logger.Warn("Failed to insert backlink", "from", newPostID, "to", targetID, "error", err)
					}
				}
				seen[match[1]] = true
			}
		}
	}
	if err := tx.Commit(); err != nil {
		logger.Error("Failed to commit transaction for new post", "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error saving post."}, app)
		return
	}

	logger.Info("New post created", "post_id", newPostID, "board_id", boardID)
	respondJSON(w, http.StatusOK, map[string]string{"redirect": redirectURL}, app)
}

// HandleCookieDelete allows a user to delete their own post.
func HandleCookieDelete(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleCookieDelete")
	postID, err := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid post ID."}, app)
		return
	}

	currentUserCookieID := r.Context().Value(UserCookieKey).(string)
	currentUserCookieHash := utils.HashIP(currentUserCookieID)

	var postCookieHash string
	if err := app.DB().DB.QueryRow("SELECT cookie_hash FROM posts WHERE id = ?", postID).Scan(&postCookieHash); err != nil {
		if err == sql.ErrNoRows {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "Post not found."}, app)
			return
		}
		logger.Error("DB error checking post ownership", "post_id", postID, "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error."}, app)
		return
	}

	if currentUserCookieHash != postCookieHash {
		logger.Warn("User failed to delete post they do not own", "user_hash", currentUserCookieHash, "post_id", postID, "owner_hash", postCookieHash)
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "You do not have permission to delete this post."}, app)
		return
	}

	_, _, err = app.DB().DeletePost(postID, app.UploadDir(), "", "")
	if err != nil {
		logger.Error("Failed to delete post by user request", "post_id", postID, "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete post."}, app)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"success": "Post deleted successfully."}, app)
}

// HandleReport allows a user to report a post for moderation.
func HandleReport(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleReport")
	ipHash := utils.HashIP(utils.GetIPAddress(r))
	cookieHash := utils.HashIP(r.Context().Value(UserCookieKey).(string))

	if _, isBanned := app.DB().GetBanDetails(ipHash, cookieHash); isBanned {
		logger.Warn("Banned user tried to submit report", "ip_hash", ipHash)
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "You are banned and cannot submit reports."}, app)
		return
	}

	postID, err := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	if err != nil || postID == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid post ID provided."}, app)
		return
	}
	reason := r.FormValue("reason")
	if reason == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Reason for reporting cannot be empty."}, app)
		return
	}

	_, err = app.DB().DB.Exec("INSERT INTO reports (post_id, reason, ip_hash, created_at) VALUES (?, ?, ?, ?)", postID, reason, ipHash, utils.GetSQLTime())
	if err != nil {
		logger.Error("Failed to insert new report", "post_id", postID, "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to submit report."}, app)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"success": "Report submitted successfully."}, app)
}

// --- Internal Helper Functions ---

// processImage now returns imagePath, thumbnailPath, hash, hasImage, error
func processImage(r *http.Request, app App, logger *slog.Logger) (string, sql.NullString, string, bool, error) {
	file, header, err := r.FormFile("image")
	if err != nil {
		if err == http.ErrMissingFile {
			return "", sql.NullString{}, "", false, nil
		}
		return "", sql.NullString{}, "", false, fmt.Errorf("could not get form file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Error("Failed to close upload file", "error", err)
		}
	}()

	limitedReader := &io.LimitedReader{R: file, N: config.MaxFileSize + 1}
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", sql.NullString{}, "", false, fmt.Errorf("could not read file data: %w", err)
	}
	if limitedReader.N == 0 {
		return "", sql.NullString{}, "", true, fmt.Errorf("file is larger than the %dMB limit", config.MaxFileSize/1024/1024)
	}
	if len(data) == 0 {
		return "", sql.NullString{}, "", true, fmt.Errorf("file is empty")
	}

	// Magic byte validation
	contentType := http.DetectContentType(data)
	allowedTypes := map[string]bool{
		"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	}
	if !allowedTypes[contentType] {
		logger.Warn("User uploaded file with invalid MIME type", "detected_type", contentType, "filename", header.Filename)
		return "", sql.NullString{}, "", true, fmt.Errorf("unsupported file type: %s. Only JPG, PNG, GIF, and WebP are allowed", contentType)
	}

	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	var existingPath, existingThumb sql.NullString
	err = app.DB().DB.QueryRow("SELECT image_path, thumbnail_path FROM posts WHERE image_hash = ?", hashStr).Scan(&existingPath, &existingThumb)
	if err == nil && existingPath.Valid {
		return existingPath.String, existingThumb, hashStr, true, nil
	}
	if err != nil && err != sql.ErrNoRows {
		logger.Error("Failed to check for existing image hash", "error", err)
	}

	reader := bytes.NewReader(data)
	cfg, format, err := image.DecodeConfig(reader)
	if err != nil {
		return "", sql.NullString{}, "", true, fmt.Errorf("invalid image format, could not decode config: %w", err)
	}
	if cfg.Width > config.MaxWidth || cfg.Height > config.MaxHeight {
		return "", sql.NullString{}, "", true, fmt.Errorf("image dimensions (%dx%d) exceed maximum (%dx%d)", cfg.Width, cfg.Height, config.MaxWidth, config.MaxHeight)
	}
	if _, err := reader.Seek(0, 0); err != nil {
		return "", sql.NullString{}, "", true, fmt.Errorf("could not reset reader position: %w", err)
	}

	// Re-decode the full image for processing
	img, err := imaging.Decode(reader, imaging.AutoOrientation(true))
	if err != nil {
		return "", sql.NullString{}, "", true, fmt.Errorf("failed to decode image with orientation correction: %w", err)
	}

	// --- Save Main Image (Re-encoded) ---
	// Non-animated GIFs are converted to JPEG for consistency and size.
	outputFormat := "jpeg"
	if format == "png" {
		outputFormat = "png"
	}

	mainFilename := fmt.Sprintf("%d_%s.%s", utils.GetTime().UnixNano(), hashStr[:12], outputFormat)
	mainOutputPath := filepath.Join(app.UploadDir(), mainFilename)
	out, err := os.Create(mainOutputPath)
	if err != nil {
		return "", sql.NullString{}, "", true, fmt.Errorf("could not create main image file: %w", err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			logger.Error("Failed to close main image file", "path", mainOutputPath, "error", err)
		}
	}()

	// Encode and save the main image
	if outputFormat == "png" {
		err = imaging.Encode(out, img, imaging.PNG)
	} else {
		err = imaging.Encode(out, img, imaging.JPEG, imaging.JPEGQuality(90))
	}
	if err != nil {
		if removeErr := os.Remove(mainOutputPath); removeErr != nil {
			logger.Error("Failed to remove failed main image file", "path", mainOutputPath, "error", removeErr)
		}
		return "", sql.NullString{}, "", true, fmt.Errorf("failed to encode main image: %w", err)
	}
	mainPath := "/uploads/" + mainFilename

	// Create a thumbnail, preserving aspect ratio.
	thumb := imaging.Fit(img, config.ThumbnailWidth, config.ThumbnailHeight, imaging.Lanczos)
	thumbFilename := fmt.Sprintf("%s_thumb.jpeg", mainFilename[:len(mainFilename)-len(filepath.Ext(mainFilename))])
	thumbOutputPath := filepath.Join(app.UploadDir(), thumbFilename)
	thumbOut, err := os.Create(thumbOutputPath)
	if err != nil {
		logger.Error("Could not create thumbnail file", "error", err)
		// Don't fail the entire post if thumbnailing fails, just proceed without it.
		return mainPath, sql.NullString{}, hashStr, true, nil
	}
	defer func() {
		if err := thumbOut.Close(); err != nil {
			logger.Error("Failed to close thumbnail file", "path", thumbOutputPath, "error", err)
		}
	}()

	if err := imaging.Encode(thumbOut, thumb, imaging.JPEG, imaging.JPEGQuality(85)); err != nil {
		logger.Error("Failed to encode thumbnail", "error", err)
		// cleanup failed file
		if removeErr := os.Remove(thumbOutputPath); removeErr != nil {
			logger.Error("Failed to remove failed thumbnail file", "path", thumbOutputPath, "error", removeErr)
		}
		return mainPath, sql.NullString{}, hashStr, true, nil
	}
	thumbPath := sql.NullString{String: "/uploads/" + thumbFilename, Valid: true}

	return mainPath, thumbPath, hashStr, true, nil
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
	logger := app.Logger().With("system", "archiver", "board_id", boardID)
	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		logger.Error("Archiver failed to get board config", "error", err)
		return
	}
	var threadCount int
	err = app.DB().DB.QueryRow("SELECT COUNT(*) FROM threads WHERE board_id = ? AND archived = 0", boardID).Scan(&threadCount)
	if err != nil {
		logger.Error("Archiver failed to get thread count", "error", err)
		return
	}
	if threadCount <= boardConfig.MaxThreads {
		return
	}
	toArchive := threadCount - boardConfig.MaxThreads
	rows, err := app.DB().DB.Query(`SELECT id FROM threads WHERE board_id = ? AND archived = 0 AND sticky = 0 ORDER BY bump ASC LIMIT ?`, boardID, toArchive)
	if err != nil {
		logger.Error("Archiver failed to find threads to archive", "error", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logger.Error("Archiver failed to close rows", "error", err)
		}
	}()

	var threadIDs []interface{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			threadIDs = append(threadIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		logger.Error("Archiver failed scanning rows", "error", err)
	}

	if len(threadIDs) > 0 {
		query := "UPDATE threads SET archived = 1 WHERE id IN (?" + strings.Repeat(",?", len(threadIDs)-1) + ")"
		if _, err := app.DB().DB.Exec(query, threadIDs...); err != nil {
			logger.Error("Archiver failed to execute update", "error", err)
		} else {
			logger.Info("Archived threads", "count", len(threadIDs))
		}
	}
}
