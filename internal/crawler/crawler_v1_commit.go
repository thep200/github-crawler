package crawler

import (
	"context"
	"strings"

	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

func (c *CrawlerV1) crawlCommits(db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, int, error) {
	ctx := context.Background()
	c.applyRateLimit()

	// Call API to get commits
	commits, err := apiCaller.CallCommits(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.handleRateLimit(ctx, err)
			commits, err = apiCaller.CallCommits(user, repoName)
			if err != nil {
				return nil, 0, err
			}
		} else {
			return nil, 0, err
		}
	}

	commitsCount := 0

	//
	for _, commit := range commits {
		if err := c.saveCommit(db, commit, releaseID); err != nil {
			return commits, commitsCount, err
		}
		commitsCount++
	}

	return commits, commitsCount, nil
}

func (c *CrawlerV1) saveCommit(db *gorm.DB, commit githubapi.CommitResponse, releaseID int) error {
	hashValue := model.TruncateString(commit.SHA, 250)

	//
	if c.isCommitProcessed(hashValue) {
		return nil
	}

	messageValue := model.TruncateString(commit.Commit.Message, 65000)

	commitModel := &model.Commit{
		Hash:      hashValue,
		Message:   messageValue,
		ReleaseID: releaseID,
		Model: model.Model{
			Config: c.Config,
			Logger: c.Logger,
			Mysql:  c.Mysql,
		},
	}

	//
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	err := tx.Create(commitModel).Error
	if err != nil {
		tx.Rollback()
		if !strings.Contains(err.Error(), "Duplicate entry") {
			return err
		}

		//
		c.addProcessedCommit(hashValue)
		return nil
	}

	//
	if err := tx.Commit().Error; err != nil {
		return err
	}

	//
	c.addProcessedCommit(hashValue)
	return nil
}
