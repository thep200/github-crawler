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

func (c *CrawlerV3) crawlReleases(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, error) {
	//
	atomic.AddInt32(&c.pendingRelease, 1)
	defer atomic.AddInt32(&c.pendingRelease, -1)

	// Gọi API để lấy thông tin releases
	c.applyRateLimit()
	releases, err := apiCaller.CallReleases(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Crawl releases hit ratelimit")
			c.handleRateLimit(ctx, err)
			c.applyRateLimit()
			releases, err = apiCaller.CallReleases(user, repoName)
			if err != nil {
				return nil, err
			}
		} else {
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
				releaseTx := db.Begin()
				if releaseTx.Error != nil {
					releaseErrChan <- releaseTx.Error
					return
				}

				defer func() {
					if r := recover(); r != nil {
						releaseTx.Rollback()
					}
				}()

				// Lưu thông tin release vào database
				releaseModel := &model.Release{
					Content: model.TruncateString(release.Body, 65000),
					RepoID:  repoID,
					Model: model.Model{
						Config: c.Config,
						Logger: c.Logger,
						Mysql:  c.Mysql,
					},
				}

				if err := releaseTx.Create(releaseModel).Error; err != nil {
					releaseTx.Rollback()
					if !strings.Contains(err.Error(), "Duplicate entry") {
						releaseErrChan <- err
					}
					return
				}

				// Commit transaction
				if err := releaseTx.Commit().Error; err != nil {
					releaseErrChan <- err
					return
				}

				// Cập nhật counter và đánh dấu đã xử lý
				atomic.AddInt32(&c.releaseCount, 1)
				c.addProcessedRelease(repoID, release.Name)

				// Crawl commits cho release này
				select {
				case commitSemaphore <- struct{}{}:
					go func() {
						defer func() { <-commitSemaphore }()
						_, err := c.crawlCommits(releaseCtx, db, apiCaller, user, repoName, releaseModel.ID)
						if err != nil {
							c.Logger.Warn(ctx, "Crawl commits cho release %s: %v failed", release.Name, err)
						}
					}()
				default:
					_, err := c.crawlCommits(releaseCtx, db, apiCaller, user, repoName, releaseModel.ID)
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
