//go:build fts5

package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"yib/models"
	"yib/utils"
)

func TestHandlePost(t *testing.T) {
	app := setupTestApp(t)
	handler := http.HandlerFunc(MakeHandler(app, HandlePost))

	t.Run("Success - Create New Thread", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("board_id", "b")
		writer.WriteField("subject", "Test Thread")
		writer.WriteField("content", "This is the OP.")
		token, answer := solveChallenge(app.challenges)
		writer.WriteField("challenge_token", token)
		writer.WriteField("challenge_answer", answer)
		writer.Close()

		req := newTestRequest(t, "POST", "/post", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var resp map[string]string
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if !strings.Contains(resp["redirect"], "/b/thread/") {
			t.Errorf("Expected redirect URL to contain '/b/thread/', got %s", resp["redirect"])
		}

		var count int
		app.db.DB.QueryRow("SELECT COUNT(*) FROM threads WHERE subject = 'Test Thread'").Scan(&count)
		if count != 1 {
			t.Error("Expected thread to be created in database, but it was not found.")
		}
	})

	t.Run("Success - Create Reply", func(t *testing.T) {
		res, _ := app.db.DB.Exec("INSERT INTO threads (id, board_id, bump) VALUES (100, 'b', '2025-01-01')")
		threadID, _ := res.LastInsertId()

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("board_id", "b")
		writer.WriteField("thread_id", "100")
		writer.WriteField("content", "This is a reply.")
		token, answer := solveChallenge(app.challenges)
		writer.WriteField("challenge_token", token)
		writer.WriteField("challenge_answer", answer)
		writer.Close()

		req := newTestRequest(t, "POST", "/post", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var replyCount int
		var bumpTime string
		app.db.DB.QueryRow("SELECT reply_count, bump FROM threads WHERE id = ?", threadID).Scan(&replyCount, &bumpTime)
		if replyCount != 1 {
			t.Errorf("Expected reply_count to be 1, got %d", replyCount)
		}
		if bumpTime == "2025-01-01" {
			t.Error("Expected bump time to be updated, but it was not.")
		}
	})

	t.Run("Validation and Security Failures", func(t *testing.T) {
		testCases := []struct {
			name           string
			setup          func(db *sql.DB, rl *models.RateLimiter)
			formValues     map[string]string
			remoteAddr     string
			expectedStatus int
			expectedError  string
		}{
			{
				name:       "Banned User",
				remoteAddr: "1.2.3.4:12345",
				setup: func(db *sql.DB, rl *models.RateLimiter) {
					ipHash := utils.HashIP("1.2.3.4")
					db.Exec("INSERT INTO bans (hash, ban_type, reason) VALUES (?, 'ip', 'test ban')", ipHash)
				},
				expectedStatus: http.StatusForbidden,
				expectedError:  "You are banned",
			},
			{
				name:       "Rate Limited User",
				remoteAddr: "5.6.7.8:12345",
				setup: func(db *sql.DB, rl *models.RateLimiter) {
					limiter := rl.GetLimiter("5.6.7.8")
					for i := 0; i < 5; i++ {
						limiter.Allow()
					}
				},
				expectedStatus: http.StatusTooManyRequests,
				expectedError:  "Rate limit exceeded",
			},
			{
				name:       "Reply to Locked Thread",
				formValues: map[string]string{"board_id": "b", "thread_id": "200", "content": "should fail"},
				setup: func(db *sql.DB, rl *models.RateLimiter) {
					db.Exec("INSERT INTO threads (id, board_id, locked) VALUES (200, 'b', 1)")
				},
				expectedStatus: http.StatusForbidden,
				expectedError:  "Thread is locked",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				app.db.DB.Exec("DELETE FROM bans; DELETE FROM threads;")
				if tc.setup != nil {
					tc.setup(app.db.DB, app.rateLimiter)
				}

				body := &bytes.Buffer{}
				writer := multipart.NewWriter(body)
				for k, v := range tc.formValues {
					writer.WriteField(k, v)
				}
				token, answer := solveChallenge(app.challenges)
				writer.WriteField("challenge_token", token)
				writer.WriteField("challenge_answer", answer)
				writer.Close()

				req := newTestRequest(t, "POST", "/post", body)
				req.Header.Set("Content-Type", writer.FormDataContentType())
				if tc.remoteAddr != "" {
					req.RemoteAddr = tc.remoteAddr
				}

				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)

				if rr.Code != tc.expectedStatus {
					t.Errorf("Expected status %d, got %d. Body: %s", tc.expectedStatus, rr.Code, rr.Body.String())
				}

				var resp map[string]string
				json.Unmarshal(rr.Body.Bytes(), &resp)
				if !strings.Contains(resp["error"], tc.expectedError) {
					t.Errorf("Expected error message to contain '%s', got '%s'", tc.expectedError, resp["error"])
				}
			})
		}
	})
}

func TestHandleCookieDelete(t *testing.T) {
	app := setupTestApp(t)
	handler := http.HandlerFunc(MakeHandler(app, HandleCookieDelete))

	ownerCookieHash := utils.HashIP("owner-cookie")

	t.Run("Success - Owner Deletes Reply", func(t *testing.T) {
		app.db.DB.Exec("INSERT INTO threads (id, board_id, reply_count) VALUES (300, 'b', 1)")
		app.db.DB.Exec("INSERT INTO posts (id, thread_id, board_id, cookie_hash) VALUES (301, 300, 'b', 'op-cookie')")
		app.db.DB.Exec("INSERT INTO posts (id, thread_id, board_id, cookie_hash) VALUES (302, 300, 'b', ?)", ownerCookieHash)
		t.Cleanup(func() { app.db.DB.Exec("DELETE FROM threads; DELETE FROM posts;") })

		form := strings.NewReader("post_id=302")
		req := newTestRequest(t, "POST", "/delete", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		*req = *req.WithContext(context.WithValue(req.Context(), UserCookieKey, "owner-cookie"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		var count int
		app.db.DB.QueryRow("SELECT COUNT(*) FROM posts WHERE id = 302").Scan(&count)
		if count != 0 {
			t.Error("Expected post to be deleted from database, but it still exists.")
		}
	})

	t.Run("Failure - Non-Owner Tries to Delete", func(t *testing.T) {
		app.db.DB.Exec("INSERT INTO threads (id, board_id, reply_count) VALUES (300, 'b', 1)")
		app.db.DB.Exec("INSERT INTO posts (id, thread_id, board_id, cookie_hash) VALUES (301, 300, 'b', 'op-cookie')")
		app.db.DB.Exec("INSERT INTO posts (id, thread_id, board_id, cookie_hash) VALUES (302, 300, 'b', ?)", ownerCookieHash)
		t.Cleanup(func() { app.db.DB.Exec("DELETE FROM threads; DELETE FROM posts;") })

		form := strings.NewReader("post_id=302")
		req := newTestRequest(t, "POST", "/delete", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		*req = *req.WithContext(context.WithValue(req.Context(), UserCookieKey, "other-user-cookie"))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d. Body: %s", rr.Code, rr.Body.String())
		}
	})
}
