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

func (c *CrawlerV4) crawlCommits(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, error) {
	//
	atomic.AddInt32(&c.pendingCommit, 1)
	defer atomic.AddInt32(&c.pendingCommit, -1)

	// Gọi API để lấy thông tin commits
	var commits []githubapi.CommitResponse
	var err error

	// Keep trying until we succeed or context is cancelled
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		c.applyRateLimit()
		commits, err = apiCaller.CallCommits(user, repoName)
		if err == nil {
			// Success, break the retry loop
			break
		}

		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Crawl commits hit ratelimit")
			c.handleRateLimit(ctx, err)
		} else {
			// For non-rate limit errors, return error
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

				// Tạo commit message để gửi vào Kafka
				commitMsg := model.CommitMessage{
					Hash:      model.TruncateString(commit.SHA, 250),
					Message:   model.TruncateString(commit.Commit.Message, 65000),
					ReleaseID: releaseID,
				}

				// Gửi message vào Kafka
				if err := c.commitProducer.Publish(ctx, "commit", commitMsg); err != nil {
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
