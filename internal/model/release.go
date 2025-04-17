package model

import (
	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Release struct {
	Model
	ID      int    `json:"id" gorm:"id"`
	Content string `json:"content" gorm:"content"`
	RepoID  int    `json:"repo_id" gorm:"repo_id"`
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
	newRelease := &Release{}
	newRelease.Content = content
	newRelease.RepoID = repoID

	db, err := r.Mysql.Db()
	if err != nil {
		return err
	}

	if err := db.Create(newRelease).Error; err != nil {
		return err
	}

	return nil
}
