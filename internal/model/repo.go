package model

import (
	"context"
	"fmt"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repo struct {
	Model
	ID         int       `json:"id" gorm:"column:id;primaryKey;autoIncrement:false"`
	User       string    `json:"user" gorm:"column:user;type:varchar(255);not null"`
	Name       string    `json:"name" gorm:"column:name;type:varchar(255);not null"`
	StarCount  int       `json:"star_count" gorm:"column:star_count;default:0"`
	ForkCount  int       `json:"fork_count" gorm:"column:fork_count;default:0"`
	WatchCount int       `json:"watch_count" gorm:"column:watch_count;default:0"`
	IssueCount int       `json:"issue_count" gorm:"column:issue_count;default:0"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP"`
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
	newRepo.CreatedAt = time.Now()
	newRepo.UpdatedAt = time.Now()

	db, err := r.Mysql.Db()
	if err != nil {
		r.Logger.Error(ctx, "Failed to get database connection: %v", err)
		return err
	}

	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"star_count", "fork_count", "watch_count", "issue_count", "updated_at"}),
	}).Create(newRepo).Error; err != nil {
		r.Logger.Error(ctx, "Failed to create repo: %v", err)
		return err
	}

	r.Logger.Info(ctx, "Successfully created repo with ID=%d", newRepo.ID)
	return nil
}

func (r *Repo) CreateBatch(repoMessages []RepoMessage) error {
	db, err := r.Mysql.Db()
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	repos := make([]Repo, 0, len(repoMessages))
	now := time.Now()

	for _, msg := range repoMessages {
		repo := Repo{
			ID:         msg.ID,
			User:       msg.User,
			Name:       msg.Name,
			StarCount:  msg.StarCount,
			ForkCount:  msg.ForkCount,
			WatchCount: msg.WatchCount,
			IssueCount: msg.IssueCount,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		repos = append(repos, repo)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"star_count", "fork_count", "watch_count", "issue_count", "updated_at"}),
		}).CreateInBatches(repos, 100)

		if result.Error != nil {
			return fmt.Errorf("failed to batch create repositories: %w", result.Error)
		}

		return nil
	})
}
