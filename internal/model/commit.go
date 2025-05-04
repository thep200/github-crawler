package model

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Commit struct {
	Model
	Hash      string `json:"hash" gorm:"column:hash;type:varchar(255);uniqueIndex"`
	Message   string `json:"message" gorm:"column:message;type:text;size:65535"`
	ReleaseID int    `json:"release_id" gorm:"column:release_id;index;not null"`
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

func (c *Commit) Create(hash string, message string, releaseID int) error {
	ctx := context.Background()
	hash = TruncateString(hash, 250)
	message = TruncateString(message, 65000)

	newCommit := &Commit{}
	newCommit.Hash = hash
	newCommit.Message = message
	newCommit.ReleaseID = releaseID

	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Failed to get database connection: %v", err)
		return err
	}

	if err := db.Create(newCommit).Error; err != nil {
		c.Logger.Error(ctx, "Failed to create commit: %v", err)
		return err
	}

	c.Logger.Info(ctx, "Successfully created commit with ID=%d", newCommit.ID)
	return nil
}
