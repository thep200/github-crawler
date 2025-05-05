package ui

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/thep200/github-crawler/internal/model"
)

//
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

//
func (h *Handler) getRepos(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pageSizeStr := r.URL.Query().Get("pageSize")
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}

	//
	search := r.URL.Query().Get("search")
	offset := (page - 1) * pageSize
	query := h.db.Offset(offset).Limit(pageSize).Order("star_count DESC")

	// Search query
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

	//
	var totalCount int64
	countQuery := h.db.Model(&model.Repo{})
	if search != "" {
		countQuery = countQuery.Where("name LIKE ? OR user LIKE ?", search, search)
	}
	countQuery.Count(&totalCount)

	// Response format
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

	//
	response := map[string]interface{}{
		"repositories": repositories,
		"pagination": map[string]interface{}{
			"page":       page,
			"pageSize":   pageSize,
			"totalCount": totalCount,
			"totalPages": (totalCount + int64(pageSize) - 1) / int64(pageSize),
		},
	}

	// JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.Logger.Error(r.Context(), "Failed to encode JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
