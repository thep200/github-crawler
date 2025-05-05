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

func (c *CrawlerV2) crawlReleases(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, error) {
	c.applyRateLimit()

	// Call API to get releases
	releases, err := apiCaller.CallReleases(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Rate limit đạt ngưỡng khi crawl releases, đợi...")
			c.handleRateLimit(ctx, err)
			releases, err = apiCaller.CallReleases(user, repoName)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	if len(releases) == 0 {
		return releases, nil
	}

	c.Logger.Info(ctx, "Đã tìm thấy %d releases cho repo %s/%s", len(releases), user, repoName)

	//
	releaseErrChan := make(chan error, len(releases))
	var wg sync.WaitGroup

	//
	releaseCtx, cancelRelease := context.WithCancel(ctx)
	defer cancelRelease()

	//
	commitSemaphore := make(chan struct{}, 5)

	//
	for i := range releases {
		release := releases[i]
		if c.isReleaseProcessed(repoID, release.Name) {
			continue
		}

		select {
		case <-releaseCtx.Done():
			return releases, ctx.Err()
		case c.releaseWorkers <- struct{}{}:
			wg.Add(1)
			go func(release githubapi.ReleaseResponse) {
				defer wg.Done()
				defer func() { <-c.releaseWorkers }()

				// Tạo transaction mới cho mỗi release
				releaseTx := db.Begin()
				if releaseTx.Error != nil {
					releaseErrChan <- releaseTx.Error
					return
				}

				defer func() {
					if r := recover(); r != nil {
						releaseTx.Rollback()
						releaseErrChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý release: %v", r)
					}
				}()

				// Lưu release
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

				if err := releaseTx.Commit().Error; err != nil {
					releaseErrChan <- err
					return
				}

				//
				atomic.AddInt32(&c.releaseCount, 1)
				c.addProcessedRelease(repoID, release.Name)

				//
				select {
				case commitSemaphore <- struct{}{}:
					go func() {
						defer func() { <-commitSemaphore }()
						_, err := c.crawlCommits(releaseCtx, db, apiCaller, user, repoName, releaseModel.ID)
						if err != nil {
							c.Logger.Warn(ctx, "Lỗi khi crawl commits cho release %s: %v", release.Name, err)
						}
					}()
				default:
					c.crawlCommits(releaseCtx, db, apiCaller, user, repoName, releaseModel.ID)
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
