package crawler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

// Crawl commits cho một release
func (c *CrawlerV2) crawlCommits(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, error) {
	c.applyRateLimit()

	// Gọi API để lấy commits
	commits, err := apiCaller.CallCommits(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Rate limit đạt ngưỡng khi crawl commits, đợi...")
			c.handleRateLimit(ctx, err)
			commits, err = apiCaller.CallCommits(user, repoName)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	if len(commits) == 0 {
		return commits, nil
	}

	var wg sync.WaitGroup

	//
	commitCtx, cancelCommit := context.WithCancel(ctx)
	defer cancelCommit()

	//
	for _, commit := range commits {
		if c.isCommitProcessed(commit.SHA) {
			continue
		}

		//
		select {
		case <-commitCtx.Done():
		case c.commitWorkers <- struct{}{}:
			wg.Add(1)
			go func(commit githubapi.CommitResponse) {
				defer wg.Done()
				defer func() { <-c.commitWorkers }()

				defer func() {
					if r := recover(); r != nil {
						c.errorChan <- fmt.Errorf("failed to processed commit: %v", r)
					}
				}()

				//
				commitTx := db.Begin()
				if commitTx.Error != nil {
					c.errorChan <- commitTx.Error
					return
				}

				//
				commitModel := &model.Commit{
					Hash:      model.TruncateString(commit.SHA, 250),
					Message:   model.TruncateString(commit.Commit.Message, 65000),
					ReleaseID: releaseID,
					Model: model.Model{
						Config: c.Config,
						Logger: c.Logger,
						Mysql:  c.Mysql,
					},
				}

				//
				if err := commitTx.Create(commitModel).Error; err != nil {
					commitTx.Rollback()
					if !strings.Contains(err.Error(), "Duplicate entry") {
						c.errorChan <- err
					}
					return
				}

				//
				if err := commitTx.Commit().Error; err != nil {
					c.errorChan <- err
					return
				}

				//
				atomic.AddInt32(&c.commitCount, 1)
				c.addProcessedCommit(commit.SHA)
			}(commit)
		}
	}

	//
	wg.Wait()

	return commits, nil
}
