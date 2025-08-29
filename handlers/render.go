// yib/handlers/render.go

package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"time"
	"yib/config"
	"yib/models"
	"yib/utils"
)

var (
	templates *template.Template
)

// LoadTemplates parses all HTML files from the templates directory.
func LoadTemplates() error {
	funcMap := template.FuncMap{
		"safeHTML":   func(s string) template.HTML { return template.HTML(s) },
		"formatTime": func(t time.Time) string { return t.Format("01/02/06(Mon)15:04:05") },
		"formatISO":  func(t time.Time) string { return t.Format(time.RFC3339) },
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"basename": func(fp string) string { return filepath.Base(fp) },
		"default": func(dflt, val string) string {
			if val == "" {
				return dflt
			}
			return val
		},
		"subtract": func(a, b int) int { return a - b },
		"imageCount": func(postCount int, posts []models.Post, op models.Post) int {
			count := 0
			if op.ImagePath != "" {
				count++
			}
			for _, p := range posts {
				if p.ImagePath != "" {
					count++
				}
			}
			return count
		},
		"stripHTML": func(s string) string { return regexp.MustCompile("<[^>]*>").ReplaceAllString(s, "") },
		"truncate": func(max int, s string) string {
			runes := []rune(s)
			if len(runes) > max {
				return string(runes[:max]) + "..."
			}
			return s
		},
	}
	templateFiles, err := filepath.Glob("templates/*.html")
	if err != nil {
		return fmt.Errorf("failed to find templates: %w", err)
	}
	templates = template.New("").Funcs(funcMap)
	templates = template.Must(templates.ParseFiles(templateFiles...))
	return nil
}

// render executes the given templates with the provided data.
func render(w http.ResponseWriter, r *http.Request, app App, layout, contentTmpl string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}

	data["GlobalBoards"] = getBoardList(app)
	data["AppVersion"] = config.AppVersion
	data["DefaultTheme"] = config.DefaultTheme

	data["MaxFileSizeMB"] = config.MaxFileSize / 1024 / 1024
	if csrfToken, ok := r.Context().Value(CSRFTokenKey).(string); ok {
		data["csrfToken"] = csrfToken
	}

	// Start with the site's default theme
	data["ColorScheme"] = config.DefaultTheme
	// If a board-specific config exists, let its theme override the default
	if cfg, ok := data["BoardConfig"].(*models.BoardConfig); ok && cfg.ColorScheme != "" {
		data["ColorScheme"] = cfg.ColorScheme
	}

	// Add global banner content
	if bannerContent, err := utils.ReadBanner(app.BannerFile()); err == nil && bannerContent != "" {
		data["Banner"] = bannerContent
	}

	if _, ok := data["FormInput"]; !ok {
		data["FormInput"] = &models.FormInput{}
	}

	if _, ok := data["ChallengeToken"]; !ok {
		token, question := app.Challenges().GenerateChallenge()
		data["ChallengeToken"] = token
		data["ChallengeQuestion"] = question
	}

	contentBuf := new(bytes.Buffer)
	err := templates.ExecuteTemplate(contentBuf, contentTmpl, data)
	if err != nil {
		log.Printf("Error rendering content template %s: %v", contentTmpl, err)
		http.Error(w, "Failed to render page content", http.StatusInternalServerError)
		return
	}
	data["Content"] = template.HTML(contentBuf.String())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = templates.ExecuteTemplate(w, layout, data)
	if err != nil {
		log.Printf("Error rendering layout template %s: %v", layout, err)
	}
}
