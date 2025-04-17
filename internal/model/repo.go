package model

import (
	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Repo struct {
	Model
	ID   int    `json:"id" gorm:"id"`
	User string `json:"user" gorm:"user"`
	Name string `json:"name" gorm:"name"`
}

func NewRepo(config *cfg.Config, logger log.Logger, db *db.Mysql) (*Repo, error) {
	repo := &Repo{
		Model: Model{
			Config: config,
			Logger: logger,
			Mysql:  db,
		},
	}
	return repo, nil
}

func (r *Repo) TableName() string {
	return "repos"
}
