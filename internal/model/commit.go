package model

import (
	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Commit struct {
	Model
	Hash    string `json:"hash" gorm:"hash"`
	Message string `json:"message" gorm:"message"`
	ReleaseID int    `json:"release_id" gorm:"release_id"`
}

func NewCommit(config *cfg.Config, logger log.Logger, db *db.Mysql) (*Commit, error) {
	commit := &Commit{
		Model: Model{
			Config: config,
			Logger: logger,
			Mysql:  db,
		},
	}
	return commit, nil
}

func (c *Commit) TableName() string {
	return "commits"
}
