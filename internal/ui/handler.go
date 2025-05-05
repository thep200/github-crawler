package ui

import (
	"encoding/json"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
	"gorm.io/gorm"
)

// Handler manages HTTP requests for the UI
type Handler struct {
	Logger  log.Logger
	Config  *cfg.Config
	MySQL   *db.Mysql
	RepoMd  *model.Repo
	db      *gorm.DB
	baseDir string
}

// NewHandler creates a new UI handler
func NewHandler(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*Handler, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)

	db, err := mysql.Db()
	if err != nil {
		return nil, err
	}

	return &Handler{
		Logger:  logger,
		Config:  config,
		MySQL:   mysql,
		RepoMd:  repoMd,
		db:      db,
		baseDir: "internal/ui/static",
	}, nil
}

// RegisterRoutes sets up the HTTP routes for the UI
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static file server for CSS, JS, etc.
	fileServer := http.FileServer(http.Dir(h.baseDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	// API routes
	mux.HandleFunc("/api/repos", h.getRepos)
	mux.HandleFunc("/api/releases", h.getReleases)
	mux.HandleFunc("/api/commits", h.getCommits)

	// HTML routes
	mux.HandleFunc("/", h.showHomePage)
}

// ShowHomePage renders the main page
func (h *Handler) showHomePage(w http.ResponseWriter, r *http.Request) {
	templatePath := filepath.Join(h.baseDir, "index.html")
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		h.Logger.Error(r.Context(), "Failed to parse template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, nil); err != nil {
		h.Logger.Error(r.Context(), "Failed to execute template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// Repository represents a GitHub repository with all necessary fields for the UI
type Repository struct {
	ID         int    `json:"id"`
	User       string `json:"user"`
	Name       string `json:"name"`
	StarCount  int    `json:"starCount"`
	ForkCount  int    `json:"forkCount"`
	WatchCount int    `json:"watchCount"`
	IssueCount int    `json:"issueCount"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// GetRepos returns a list of repositories as JSON
func (h *Handler) getRepos(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters for pagination
	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pageSizeStr := r.URL.Query().Get("pageSize")
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}

	// Get search parameter
	search := r.URL.Query().Get("search")

	offset := (page - 1) * pageSize

	// Query the database for repositories
	query := h.db.Offset(offset).Limit(pageSize).Order("star_count DESC")

	// Apply search filter if provided
	if search != "" {
		search = "%" + search + "%"
		query = query.Where("name LIKE ? OR user LIKE ?", search, search)
	}

	var repos []model.Repo
	result := query.Find(&repos)
	if result.Error != nil {
		h.Logger.Error(r.Context(), "Failed to fetch repositories: %v", result.Error)
		http.Error(w, "Failed to fetch repositories", http.StatusInternalServerError)
		return
	}

	// Count total repositories for pagination
	var totalCount int64
	countQuery := h.db.Model(&model.Repo{})

	// Apply search filter to count query if provided
	if search != "" {
		countQuery = countQuery.Where("name LIKE ? OR user LIKE ?", search, search)
	}

	countQuery.Count(&totalCount)

	// Convert to response format
	var repositories []Repository
	for _, repo := range repos {
		repositories = append(repositories, Repository{
			ID:         repo.ID,
			User:       repo.User,
			Name:       repo.Name,
			StarCount:  repo.StarCount,
			ForkCount:  repo.ForkCount,
			WatchCount: repo.WatchCount,
			IssueCount: repo.IssueCount,
			CreatedAt:  repo.CreatedAt.Format("2006-01-02"),
			UpdatedAt:  repo.UpdatedAt.Format("2006-01-02"),
		})
	}

	// Create response with metadata
	response := map[string]interface{}{
		"repositories": repositories,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"totalCount": totalCount,
			"totalPages": (totalCount + int64(pageSize) - 1) / int64(pageSize),
		},
	}

	// Send JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.Logger.Error(r.Context(), "Failed to encode JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// Release represents a repository release
type Release struct {
	ID        uint   `json:"id"`
	Content   string `json:"content"`
	RepoID    int    `json:"repoId"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// GetReleases returns releases for a given repository
func (h *Handler) getReleases(w http.ResponseWriter, r *http.Request) {
	repoIDStr := r.URL.Query().Get("repoId")
	repoID, err := strconv.Atoi(repoIDStr)
	if err != nil {
		http.Error(w, "Invalid repository ID", http.StatusBadRequest)
		return
	}

	var releases []model.Release
	result := h.db.Where("repo_id = ?", repoID).Find(&releases)
	if result.Error != nil {
		h.Logger.Error(r.Context(), "Failed to fetch releases: %v", result.Error)
		http.Error(w, "Failed to fetch releases", http.StatusInternalServerError)
		return
	}

	var releaseResponses []Release
	for _, release := range releases {
		releaseResponses = append(releaseResponses, Release{
			ID:        uint(release.ID),
			Content:   release.Content,
			RepoID:    release.RepoID,
			CreatedAt: release.CreatedAt.Format("2006-01-02"),
			UpdatedAt: release.UpdatedAt.Format("2006-01-02"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(releaseResponses); err != nil {
		h.Logger.Error(r.Context(), "Failed to encode JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// Commit represents a repository commit
type Commit struct {
	ID        uint   `json:"id"`
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	ReleaseID int    `json:"releaseId"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// GetCommits returns commits for a given release
func (h *Handler) getCommits(w http.ResponseWriter, r *http.Request) {
	releaseIDStr := r.URL.Query().Get("releaseId")
	releaseID, err := strconv.Atoi(releaseIDStr)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}

	var commits []model.Commit
	result := h.db.Where("release_id = ?", releaseID).Find(&commits)
	if result.Error != nil {
		h.Logger.Error(r.Context(), "Failed to fetch commits: %v", result.Error)
		http.Error(w, "Failed to fetch commits", http.StatusInternalServerError)
		return
	}

	var commitResponses []Commit
	for _, commit := range commits {
		commitResponses = append(commitResponses, Commit{
			ID:        uint(commit.ID),
			Hash:      commit.Hash,
			Message:   commit.Message,
			ReleaseID: commit.ReleaseID,
			CreatedAt: commit.CreatedAt.Format("2006-01-02"),
			UpdatedAt: commit.UpdatedAt.Format("2006-01-02"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(commitResponses); err != nil {
		h.Logger.Error(r.Context(), "Failed to encode JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
