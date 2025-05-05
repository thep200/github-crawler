package ui

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/thep200/github-crawler/internal/model"
)

type Commit struct {
	ID        uint   `json:"id"`
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	ReleaseID int    `json:"releaseId"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func (h *Handler) getCommits(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
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

	// JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(commitResponses); err != nil {
		h.Logger.Error(r.Context(), "Failed to encode JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
