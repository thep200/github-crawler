package crawler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

func (c *CrawlerV3) crawlCommits(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, error) {
	//
	atomic.AddInt32(&c.pendingCommit, 1)
	defer atomic.AddInt32(&c.pendingCommit, -1)

	// Gọi API để lấy thông tin commits
	c.applyRateLimit()
	commits, err := apiCaller.CallCommits(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Crawl commits hit ratelimit")
			c.handleRateLimit(ctx, err)
			c.applyRateLimit()
			commits, err = apiCaller.CallCommits(user, repoName)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// Nếu không có commits nào, trả về mảng rỗng
	if len(commits) == 0 {
		return commits, nil
	}

	// Ghi log số lượng commits
	c.Logger.Info(ctx, "Đã tìm thấy %d commits cho %s/%s", len(commits), user, repoName)

	//
	var wg sync.WaitGroup
	commitCtx, cancelCommit := context.WithCancel(ctx)
	defer cancelCommit()

	//
	for _, commit := range commits {
		// Kiểm tra nếu commit đã được xử lý rồi
		if c.isCommitProcessed(commit.SHA) {
			continue
		}

		// Xử lý commit trong worker pool
		select {
		case <-commitCtx.Done():
			return commits, nil
		case c.commitWorkers <- struct{}{}:
			wg.Add(1)
			go func(commit githubapi.CommitResponse) {
				defer wg.Done()
				defer func() { <-c.commitWorkers }()

				// Xử lý panic nếu có
				defer func() {
					if r := recover(); r != nil {
						c.errorChan <- fmt.Errorf("panic khi crawl commit: %v", r)
					}
				}()

				// Tạo transaction
				commitTx := db.Begin()
				if commitTx.Error != nil {
					c.errorChan <- commitTx.Error
					return
				}

				// Lưu thông tin commit vào database
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

				if err := commitTx.Create(commitModel).Error; err != nil {
					commitTx.Rollback()
					c.errorChan <- err
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

	//
	wg.Wait()
	return commits, nil
}
