package model

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Release struct {
	Model
	Content string `json:"content" gorm:"column:content;type:text;size:65535"` // Định nghĩa rõ kích thước
	RepoID  int    `json:"repo_id" gorm:"column:repo_id;index;not null"`
}

func NewRelease(config *cfg.Config, logger log.Logger, db *db.Mysql) (*Release, error) {
	release := &Release{
		Model: Model{
			Config: config,
			Logger: logger,
			Mysql:  db,
		},
	}
	return release, nil
}

func (r *Release) TableName() string {
	return "releases"
}

func (r *Release) Create(content string, repoID int) error {
	ctx := context.Background()
	// Cắt nội dung để tránh lỗi "data too long"
	content = TruncateString(content, 65000) // Để dự phòng một chút

	r.Logger.Info(ctx, "Creating release with content length=%d, repoID=%d", len(content), repoID)

	newRelease := &Release{}
	newRelease.Content = content
	newRelease.RepoID = repoID

	db, err := r.Mysql.Db()
	if err != nil {
		r.Logger.Error(ctx, "Failed to get database connection: %v", err)
		return err
	}

	if err := db.Create(newRelease).Error; err != nil {
		r.Logger.Error(ctx, "Failed to create release: %v", err)
		return err
	}

	r.Logger.Info(ctx, "Successfully created release with ID=%d", newRelease.ID)
	return nil
}
