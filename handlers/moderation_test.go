//go:build fts5

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func setupModServer(_ *testing.T, app *MockApplication) *httptest.Server {
	mux := SetupRouter(app)
	finalHandler := AppContextMiddleware(app, CookieMiddleware(CSRFMiddleware(NewSecurityHeadersMiddleware("")(mux))))
	return httptest.NewServer(finalHandler)
}

// getModClientWithToken is a new helper that creates a client with a cookie jar,
// makes an initial request to get the CSRF cookie, and returns the client and the token value.
func getModClientWithToken(t *testing.T, serverURL string) (*http.Client, string) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}

	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, _ := http.NewRequest("GET", serverURL+"/mod/", nil)
	req.Header.Set("X-Real-IP", "127.0.0.1") // Act as a moderator
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make initial request for CSRF token: %v", err)
	}
	defer resp.Body.Close()

	// Find the CSRF token's value from the cookies now stored in the jar
	u, _ := url.Parse(serverURL)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "csrf_token" {
			return client, cookie.Value
		}
	}

	t.Fatal("CSRF token cookie not found in jar")
	return nil, ""
}

func TestRequireLAN_Middleware(t *testing.T) {
	app := setupTestApp(t)
	server := setupModServer(t, app)
	defer server.Close()
	client := server.Client()

	t.Run("Allowed LAN IP", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/mod/", nil)
		req.Header.Set("X-Real-IP", "192.168.1.100")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200 for LAN IP, got %d", resp.StatusCode)
		}
	})

	t.Run("Forbidden Public IP", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/mod/", nil)
		req.Header.Set("X-Real-IP", "8.8.8.8")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected status 403 for public IP, got %d", resp.StatusCode)
		}
	})
}

func TestHandleModDelete(t *testing.T) {
	app := setupTestApp(t)
	server := setupModServer(t, app)
	defer server.Close()

	app.db.DB.Exec("INSERT INTO threads (id, board_id, reply_count) VALUES (400, 'b', 1)")
	app.db.DB.Exec("INSERT INTO posts (id, thread_id, board_id) VALUES (401, 400, 'b')")
	app.db.DB.Exec("INSERT INTO posts (id, thread_id, board_id) VALUES (402, 400, 'b')")

	t.Run("Moderator deletes a reply", func(t *testing.T) {
		client, csrfToken := getModClientWithToken(t, server.URL)

		form := url.Values{}
		form.Add("post_id", "402")
		form.Add("csrf_token", csrfToken)

		req, _ := http.NewRequest("POST", server.URL+"/mod/delete-post", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Real-IP", "127.0.0.1")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if result["redirect"] != "" {
			t.Errorf("Expected empty redirect for reply deletion, got %v", result["redirect"])
		}

		var count int
		app.db.DB.QueryRow("SELECT COUNT(*) FROM posts WHERE id = 402").Scan(&count)
		if count != 0 {
			t.Error("Expected reply post to be deleted")
		}
	})
}

func TestHandleCreateBoard(t *testing.T) {
	app := setupTestApp(t)
	server := setupModServer(t, app)
	defer server.Close()

	t.Run("Success - Create new board", func(t *testing.T) {
		client, csrfToken := getModClientWithToken(t, server.URL)

		form := url.Values{}
		form.Add("id", "g")
		form.Add("name", "Technology")
		form.Add("description", "Gears and Gadgets")
		form.Add("category_id", "1")
		form.Add("csrf_token", csrfToken)

		req, _ := http.NewRequest("POST", server.URL+"/mod/create-board", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Real-IP", "127.0.0.1")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("Expected status 303 See Other, got %d", resp.StatusCode)
		}

		var name string
		app.db.DB.QueryRow("SELECT name FROM boards WHERE id = 'g'").Scan(&name)
		if name != "Technology" {
			t.Errorf("Expected board 'g' to have name 'Technology', but it was not found or incorrect.")
		}
	})
}
