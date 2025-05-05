// filepath: /Users/thep200/Projects/Study/github-crawler/internal/crawler/crawler_v3_commit.go
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
func (c *CrawlerV3) crawlCommits(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, error) {
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

	// Nếu không có commits, trả về mảng rỗng
	if len(commits) == 0 {
		return commits, nil
	}

	var wg sync.WaitGroup

	// Context cho việc xử lý commits
	commitCtx, cancelCommit := context.WithCancel(ctx)
	defer cancelCommit()

	// Xử lý từng commit
	for _, commit := range commits {
		// Nếu đã xử lý commit này thì bỏ qua
		if c.isCommitProcessed(commit.SHA) {
			continue
		}

		// Worker cho commit
		select {
		case <-commitCtx.Done():
			return commits, nil
		case c.commitWorkers <- struct{}{}:
			wg.Add(1)
			go func(commit githubapi.CommitResponse) {
				defer wg.Done()
				defer func() { <-c.commitWorkers }()

				// Xử lý panic
				defer func() {
					if r := recover(); r != nil {
						c.errorChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý commit: %v", r)
					}
				}()

				// Tạo transaction mới cho mỗi commit
				commitTx := db.Begin()
				if commitTx.Error != nil {
					c.errorChan <- commitTx.Error
					return
				}

				// Tạo commit model
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

				// Lưu commit vào database
				if err := commitTx.Create(commitModel).Error; err != nil {
					commitTx.Rollback()
					if !strings.Contains(err.Error(), "Duplicate entry") {
						c.errorChan <- err
					}
					return
				}

				// Commit transaction
				if err := commitTx.Commit().Error; err != nil {
					c.errorChan <- err
					return
				}

				// Cập nhật counter và đánh dấu đã xử lý
				atomic.AddInt32(&c.commitCount, 1)
				c.addProcessedCommit(commit.SHA)
			}(commit)
		}
	}

	// Đợi tất cả workers hoàn thành
	wg.Wait()

	return commits, nil
}
