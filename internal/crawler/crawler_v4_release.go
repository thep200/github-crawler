package crawler

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

func (c *CrawlerV4) crawlReleases(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, error) {
	//
	atomic.AddInt32(&c.pendingRelease, 1)
	defer atomic.AddInt32(&c.pendingRelease, -1)

	// Gọi API để lấy thông tin releases
	var releases []githubapi.ReleaseResponse
	var err error

	// Keep trying until we succeed or context is cancelled
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		c.applyRateLimit()
		releases, err = apiCaller.CallReleases(user, repoName)
		if err == nil {
			// Success, break the retry loop
			break
		}

		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Crawl releases hit ratelimit")
			c.handleRateLimit(ctx, err)
		} else {
			// For non-rate limit errors, return error
			return nil, err
		}
	}

	// Nếu không có releases nào, trả về mảng rỗng
	if len(releases) == 0 {
		return releases, nil
	}

	// Ghi log
	c.Logger.Info(ctx, "Đã tìm thấy %d releases cho repo %s/%s", len(releases), user, repoName)

	// Kênh lỗi cho các goroutine xử lý releases
	releaseErrChan := make(chan error, len(releases))
	var wg sync.WaitGroup
	releaseCtx, cancelRelease := context.WithCancel(ctx)
	defer cancelRelease()

	// Semaphore cho commits
	commitSemaphore := make(chan struct{}, 5)

	// Xử lý từng release
	for i := range releases {
		release := releases[i]
		if c.isReleaseProcessed(repoID, release.Name) {
			continue
		}

		//
		select {
		case <-releaseCtx.Done():
			return releases, ctx.Err()
		case c.releaseWorkers <- struct{}{}:
			wg.Add(1)
			go func(release githubapi.ReleaseResponse) {
				defer wg.Done()
				defer func() { <-c.releaseWorkers }()

				// Xử lý panic nếu có
				defer func() {
					if r := recover(); r != nil {
						c.Logger.Error(ctx, "Panic when processing release: %v", r)
					}
				}()

				// Tạo release message để gửi vào Kafka
				releaseMsg := model.ReleaseMessage{
					Content: model.TruncateString(release.Body, 65000),
					RepoID:  repoID,
				}

				// Gửi message vào Kafka
				if err := c.releaseProducer.Publish(ctx, "release", releaseMsg); err != nil {
					if !strings.Contains(err.Error(), "Duplicate entry") {
						releaseErrChan <- err
					}
					return
				}

				// Tính release ID - trong thực tế, chúng ta cần xác định ID này từ database
				// Đây là một giả định rằng chúng ta có một service khác đang xử lý release và trả về ID
				// Trong trường hợp thực tế, bạn cần có cách để biết được ID thực sau khi lưu vào DB
				mockReleaseID := len(release.Body) % 10000 // Chỉ để mô phỏng một ID

				// Cập nhật counter và đánh dấu đã xử lý
				atomic.AddInt32(&c.releaseCount, 1)
				c.addProcessedRelease(repoID, release.Name)

				// Crawl commits cho release này
				select {
				case commitSemaphore <- struct{}{}:
					go func() {
						defer func() { <-commitSemaphore }()
						_, err := c.crawlCommits(releaseCtx, db, apiCaller, user, repoName, mockReleaseID)
						if err != nil {
							c.Logger.Warn(ctx, "Crawl commits cho release %s: %v failed", release.Name, err)
						}
					}()
				default:
					_, err := c.crawlCommits(releaseCtx, db, apiCaller, user, repoName, mockReleaseID)
					if err != nil {
						c.Logger.Warn(ctx, "Crawl commits cho release %s: %v failed", release.Name, err)
					}
				}
			}(release)
		}
	}

	//
	wg.Wait()

	//
	select {
	case err := <-releaseErrChan:
		return releases, err
	default:
		return releases, nil
	}
}
