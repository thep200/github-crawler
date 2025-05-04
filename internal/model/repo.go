package model

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Repo struct {
	Model
	ID         int    `json:"id" gorm:"column:id;primaryKey;autoIncrement:false"`
	User       string `json:"user" gorm:"column:user;type:varchar(255);not null"`
	Name       string `json:"name" gorm:"column:name;type:varchar(255);not null"`
	StarCount  int    `json:"star_count" gorm:"column:star_count;default:0"`
	ForkCount  int    `json:"fork_count" gorm:"column:fork_count;default:0"`
	WatchCount int    `json:"watch_count" gorm:"column:watch_count;default:0"`
	IssueCount int    `json:"issue_count" gorm:"column:issue_count;default:0"`
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

func (r *Repo) Create(user string, name string, starCount, forkCount, watchCount, issueCount int) error {
	ctx := context.Background()
	user = TruncateString(user, 250)
	name = TruncateString(name, 250)

	newRepo := &Repo{}
	newRepo.User = user
	newRepo.Name = name
	newRepo.StarCount = starCount
	newRepo.ForkCount = forkCount
	newRepo.WatchCount = watchCount
	newRepo.IssueCount = issueCount

	db, err := r.Mysql.Db()
	if err != nil {
		r.Logger.Error(ctx, "Failed to get database connection: %v", err)
		return err
	}

	if err := db.Create(newRepo).Error; err != nil {
		r.Logger.Error(ctx, "Failed to create repo: %v", err)
		return err
	}

	r.Logger.Info(ctx, "Successfully created repo with ID=%d", newRepo.ID)
	return nil
}
