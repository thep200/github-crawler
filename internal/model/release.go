package model

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Release struct {
	Model
	ID      int    `json:"id" gorm:"column:id;primaryKey;autoIncrement:false"`
	Content string `json:"content" gorm:"column:content;type:text;size:65535"`
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
	content = TruncateString(content, 65000)

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
