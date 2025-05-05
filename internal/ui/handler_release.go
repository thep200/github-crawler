package ui

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/thep200/github-crawler/internal/model"
)

type Release struct {
	ID        uint   `json:"id"`
	Content   string `json:"content"`
	RepoID    int    `json:"repoId"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func (h *Handler) getReleases(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
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

	// JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(releaseResponses); err != nil {
		h.Logger.Error(r.Context(), "Failed to encode JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
