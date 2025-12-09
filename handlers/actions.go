package handlers

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"html/template"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
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

	if ban, isBanned := app.DB().GetBanDetails(ip, ipHash, cookieHash); isBanned {
		// Auto-ban logic:
		// 1. If user is banned by cookie, but IP is clean -> Ban IP (Evasion)
		// 2. If user is banned by IP/CIDR, but cookie is clean -> Ban Cookie (Reverse Evasion)

		isIPBanned, _ := app.DB().IsHashBanned(ipHash, "ip")
		if ban.BanType == "cookie" && !isIPBanned {
			logger.Info("Auto-banning evasion IP", "ip_hash", ipHash, "original_reason", ban.Reason)
			if cerr := app.DB().CreateBan(ipHash, "ip", ban.Reason, "system", ban.ExpiresAt); cerr != nil {
				logger.Error("Failed to auto-ban evasion IP", "error", cerr)
			}
		}

		isCookieBanned, _ := app.DB().IsHashBanned(cookieHash, "cookie")
		if (ban.BanType == "ip" || ban.BanType == "cidr") && !isCookieBanned {
			logger.Info("Auto-banning evasion cookie", "cookie_hash", cookieHash, "original_reason", ban.Reason)
			if cerr := app.DB().CreateBan(cookieHash, "cookie", ban.Reason, "system", ban.ExpiresAt); cerr != nil {
				logger.Error("Failed to auto-ban evasion cookie", "error", cerr)
			}
		}

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

	// Apply word filters to name
	filteredName, err := app.DB().ApplyFilters(name)
	if err != nil {
		if err.Error() == "BAN_TRIGGERED" {
			logger.Warn("User triggered auto-ban filter in name field", "ip", ip)
			if cerr := app.DB().CreateBan(ipHash, "ip", "Triggered spam filter (auto-ban)", "system", sql.NullTime{Valid: true, Time: utils.GetSQLTime().Add(24 * time.Hour)}); cerr != nil {
				logger.Error("Failed to auto-ban filter triggered IP", "error", cerr)
			}
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "You have been banned for posting restricted content."}, app)
			return
		}
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Name: " + err.Error()}, app)
		return
	}
	name = filteredName

	// Apply word filters to subject
	filteredSubject, err := app.DB().ApplyFilters(subject)
	if err != nil {
		if err.Error() == "BAN_TRIGGERED" {
			logger.Warn("User triggered auto-ban filter in subject field", "ip", ip)
			if cerr := app.DB().CreateBan(ipHash, "ip", "Triggered spam filter (auto-ban)", "system", sql.NullTime{Valid: true, Time: utils.GetSQLTime().Add(24 * time.Hour)}); cerr != nil {
				logger.Error("Failed to auto-ban filter triggered IP", "error", cerr)
			}
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "You have been banned for posting restricted content."}, app)
			return
		}
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Subject: " + err.Error()}, app)
		return
	}
	subject = filteredSubject

	// Apply word filters to content
	filteredContent, err := app.DB().ApplyFilters(content)
	if err != nil {
		if err.Error() == "BAN_TRIGGERED" {
			logger.Warn("User triggered auto-ban filter", "ip", ip)
			if cerr := app.DB().CreateBan(ipHash, "ip", "Triggered spam filter (auto-ban)", "system", sql.NullTime{Valid: true, Time: utils.GetSQLTime().Add(24 * time.Hour)}); cerr != nil {
				logger.Error("Failed to auto-ban filter triggered IP", "error", cerr)
			}
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "You have been banned for posting restricted content."}, app)
			return
		}
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}, app)
		return
	}
	content = filteredContent

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
	var res sql.Result
	if threadIDStr == "" || threadIDStr == "0" { // New thread
		if boardConfig.ImageRequired && !hasImage {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Image required for new threads on this board."}, app)
			return
		}
		res, err = tx.Exec("INSERT INTO threads (board_id, subject, bump, reply_count, image_count) VALUES (?, ?, ?, 0, ?)", boardID, subject, utils.GetSQLTime(), utils.BtoI(hasImage))
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
		res, err = tx.Exec(`
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

	matches := reBacklinks.FindAllStringSubmatch(content, -1)
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

	var postCookieHash, boardID string // Capture boardID here
	if err := app.DB().DB.QueryRow("SELECT cookie_hash, board_id FROM posts WHERE id = ?", postID).Scan(&postCookieHash, &boardID); err != nil {
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

	// Capture the isOp flag from the database operation.
	_, isOp, err := app.DB().DeletePost(postID, app.Storage(), "", "")
	if err != nil {
		logger.Error("Failed to delete post by user request", "post_id", postID, "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete post."}, app)
		return
	}

	var redirectURL string
	if isOp {
		// If an OP was deleted, redirect to the board index.
		redirectURL = "/" + boardID + "/"
	}

	// If it was a reply, redirectURL will be "" and the frontend will just reload.
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success":  "Post deleted successfully.",
		"redirect": redirectURL,
	}, app)
}

// HandleReport allows a user to report a post for moderation.
func HandleReport(w http.ResponseWriter, r *http.Request, app App) {
	logger := app.Logger().With("handler", "HandleReport")
	ip := utils.GetIPAddress(r)
	ipHash := utils.HashIP(ip)
	cookieHash := utils.HashIP(r.Context().Value(UserCookieKey).(string))

	if _, isBanned := app.DB().GetBanDetails(ip, ipHash, cookieHash); isBanned {
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
	allowedImages := map[string]bool{
		"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	}
	allowedVideos := map[string]bool{
		"video/webm": true, "video/mp4": true,
	}

	if !allowedImages[contentType] && !allowedVideos[contentType] {
		logger.Warn("User uploaded file with invalid MIME type", "detected_type", contentType, "filename", header.Filename)
		return "", sql.NullString{}, "", true, fmt.Errorf("unsupported file type: %s. Only JPG, PNG, GIF, WebP, WebM, and MP4 are allowed", contentType)
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

	// Determine output filename
	var ext string
	switch contentType {
	case "image/jpeg":
		ext = ".jpeg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	case "video/webm":
		ext = ".webm"
	case "video/mp4":
		ext = ".mp4"
	}

	mainFilename := fmt.Sprintf("%d_%s%s", utils.GetTime().UnixNano(), hashStr[:12], ext)
	thumbFilename := fmt.Sprintf("%d_%s_thumb.jpeg", utils.GetTime().UnixNano(), hashStr[:12])

	var thumbData []byte

	// Processing
	if allowedVideos[contentType] {
		tempMain, err := os.CreateTemp("", "yib-vid-*"+ext)
		if err != nil {
			 return "", sql.NullString{}, "", true, err
		}
		defer func() {
			if rerr := os.Remove(tempMain.Name()); rerr != nil {
				logger.Error("Failed to remove temporary main file", "path", tempMain.Name(), "error", rerr)
			}
		}()
		if _, werr := tempMain.Write(data); werr != nil {
			logger.Error("Failed to write to temporary main file", "path", tempMain.Name(), "error", werr)
		}
		if cerr := tempMain.Close(); cerr != nil {
			logger.Error("Failed to close temporary main file", "path", tempMain.Name(), "error", cerr)
		}

		tempThumb := tempMain.Name() + "_thumb.jpg"
		defer func() {
			if rerr := os.Remove(tempThumb); rerr != nil {
				logger.Error("Failed to remove temporary thumbnail file", "path", tempThumb, "error", rerr)
			}
		}()

		// ffmpeg
		cmd := exec.Command("ffmpeg", "-y", "-i", tempMain.Name(), "-ss", "00:00:00.000", "-vframes", "1", "-vf", fmt.Sprintf("scale=%d:-1", config.ThumbnailWidth), tempThumb)
		if err := cmd.Run(); err == nil {
			thumbData, _ = os.ReadFile(tempThumb)
		} else {
			logger.Error("ffmpeg failed", "error", err)
		}
	} else {
		// Image
		reader := bytes.NewReader(data)
		img, err := imaging.Decode(reader, imaging.AutoOrientation(true))
		if err == nil {
			thumb := imaging.Fit(img, config.ThumbnailWidth, config.ThumbnailHeight, imaging.Lanczos)
			var buf bytes.Buffer
			if err := imaging.Encode(&buf, thumb, imaging.JPEG, imaging.JPEGQuality(85)); err == nil {
				thumbData = buf.Bytes()
			}
		} else {
			logger.Error("Failed to decode image for thumbnail", "error", err)
		}
	}

	// Upload Main
	mainPath, err := app.Storage().SaveFile(mainFilename, data, contentType)
	if err != nil {
		return "", sql.NullString{}, "", true, fmt.Errorf("failed to save file: %w", err)
	}

	// Upload Thumb
	var thumbPath sql.NullString
	if len(thumbData) > 0 {
		tPath, err := app.Storage().SaveFile(thumbFilename, thumbData, "image/jpeg")
		if err == nil {
			thumbPath = sql.NullString{String: tPath, Valid: true}
		} else {
			logger.Error("Failed to save thumbnail", "error", err)
		}
	}

	return mainPath, thumbPath, hashStr, true, nil
}

func processContent(content string) string {
	escaped := template.HTMLEscapeString(content)

	// Apply markdown formatting before other processing
	formatted := applyMarkdownFormatting(escaped)

	// Process backlinks (>>123)
	linked := reQuoteLinks.ReplaceAllString(formatted, `<a href="#p$1" class="backlink" data-post="$1">&gt;&gt;$1</a>`)

	// Process greentext (lines starting with >)
	lines := strings.Split(linked, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "&gt;") && !strings.HasPrefix(line, "&gt;&gt;") {
			lines[i] = `<span class="greentext">` + line + `</span>`
		}
	}
	return strings.Join(lines, "<br>")
}

// Precompiled regex patterns for text processing
var (
	// Markdown formatting patterns
	reSpoiler          = regexp.MustCompile(`\|\|([^|]+?)\|\|`)
	reStrike           = regexp.MustCompile(`~~([^~]+?)~~`)
	reBoldAsterisk     = regexp.MustCompile(`\*\*([^*]+?)\*\*`)
	reUnderscore       = regexp.MustCompile(`__([^_]+?)__`)
	reItalicAsterisk   = regexp.MustCompile(`\*([^*]+?)\*`)
	reItalicUnderscore = regexp.MustCompile(`_([^_]+?)_`)

	// Post processing patterns
	reBacklinks  = regexp.MustCompile(`>>(\d+)`)
	reQuoteLinks = regexp.MustCompile(`&gt;&gt;(\d+)`)
)

// applyMarkdownFormatting processes markdown-style formatting
func applyMarkdownFormatting(content string) string {
	// Process longer patterns first to avoid conflicts
	content = reSpoiler.ReplaceAllString(content, `<span class="spoiler">$1</span>`)
	content = reStrike.ReplaceAllString(content, `<span class="strikethrough">$1</span>`)
	content = reBoldAsterisk.ReplaceAllString(content, `<strong>$1</strong>`)
	content = reUnderscore.ReplaceAllString(content, `<span class="underline">$1</span>`)
	content = reItalicAsterisk.ReplaceAllString(content, `<em>$1</em>`)
	content = reItalicUnderscore.ReplaceAllString(content, `<em>$1</em>`)
	return content
}

func archiveOldThreads(app App, boardID string) {
	logger := app.Logger().With("system", "archiver", "board_id", boardID)
	boardConfig, err := app.DB().GetBoard(boardID)
	if err != nil {
		logger.Error("Archiver failed to get board config", "error", err)
		return
	}
	var threadCount int
	err = app.DB().DB.QueryRow("SELECT COUNT(*) FROM threads WHERE board_id = ? AND archived = 0 AND sticky = 0", boardID).Scan(&threadCount)
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
