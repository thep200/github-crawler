// Crawler version 2
// Crawler áp dụng concurrency để tăng tốc việc thu thập dữ liệu

package crawler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thep200/github-crawler/cfg"
	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/limiter"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
	"gorm.io/gorm"
)

type CrawlerV2 struct {
	Logger                log.Logger
	Config                *cfg.Config
	Mysql                 *db.Mysql
	RepoMd                *model.Repo
	ReleaseMd             *model.Release
	CommitMd              *model.Commit
	rateLimiter           *limiter.RateLimiter
	processedRepoIDs      map[int64]bool
	processedReleaseKeys  map[string]bool
	processedCommitHashes map[string]bool
	processedLock         sync.RWMutex

	//
	repoWorkers    chan struct{}
	releaseWorkers chan struct{}
	commitWorkers  chan struct{}
	errorChan      chan error
	backgroundWg   sync.WaitGroup
}

func NewCrawlerV2(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV2, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)

	//
	maxRepoWorkers := 5
	maxReleaseWorkers := 10
	maxCommitWorkers := 20

	return &CrawlerV2{
		Logger:                logger,
		Config:                config,
		Mysql:                 mysql,
		RepoMd:                repoMd,
		ReleaseMd:             releaseMd,
		CommitMd:              commitMd,
		rateLimiter:           rateLimiter,
		processedRepoIDs:      make(map[int64]bool, 5000),
		processedReleaseKeys:  make(map[string]bool, 10000),
		processedCommitHashes: make(map[string]bool, 20000),
		processedLock:         sync.RWMutex{},
		repoWorkers:           make(chan struct{}, maxRepoWorkers),
		releaseWorkers:        make(chan struct{}, maxReleaseWorkers),
		commitWorkers:         make(chan struct{}, maxCommitWorkers),
		errorChan:             make(chan error, 100), // Kênh lớn để tránh blocking
		backgroundWg:          sync.WaitGroup{},
	}, nil
}

func (c *CrawlerV2) Crawl() bool {
	ctx := context.Background()
	startTime := time.Now()
	c.Logger.Info(ctx, "Bắt đầu crawl dữ liệu repository GitHub với phương pháp concurrency %s", startTime.Format(time.RFC3339))

	//
	crawlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	//
	go c.errorMonitor(crawlCtx)

	// Connect to database
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Cannot connect to database: %v", err)
		return false
	}

	// Các biến theo dõi tiến trình
	var (
		page          = 1
		totalRepos    = 0
		maxRepos      = 5000
		perPage       = 100
		apiCaller     = githubapi.NewCaller(c.Logger, c.Config, page, perPage)
		maxApiResults = 1000
		counterLock   sync.Mutex
		mainWg        sync.WaitGroup
	)

	//
	doneCh := make(chan bool)

	//
	go func() {
		defer close(doneCh)
		for {
			select {
			case <-crawlCtx.Done():
				return
			default:
				counterLock.Lock()
				repoCount := totalRepos
				counterLock.Unlock()
				if repoCount >= maxRepos {
					doneCh <- true
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	//
	for totalRepos < maxRepos {
		if page > maxApiResults/perPage {
			break
		}

		//
		c.applyRateLimit()

		//
		apiCaller.Page = page
		apiCaller.PerPage = perPage

		//
		repos, err := apiCaller.Call()
		if err != nil {
			if c.isRateLimitError(err) {
				time.Sleep(60 * time.Second)
				continue
			}
			c.Logger.Error(ctx, "Cannot call GitHub API: %v", err)
			return false
		}

		//
		if len(repos) == 0 {
			page++
			continue
		}

		//
		for _, repo := range repos {
			counterLock.Lock()
			if totalRepos >= maxRepos {
				counterLock.Unlock()
				break
			}
			counterLock.Unlock()

			// Semaphore
			select {
			case <-doneCh:
			case c.repoWorkers <- struct{}{}:
				mainWg.Add(1)
				go func(repo githubapi.GithubAPIResponse) {
					defer mainWg.Done()
					defer func() { <-c.repoWorkers }()

					//
					repoTx := db.Begin()
					if repoTx.Error != nil {
						c.errorChan <- repoTx.Error
						return
					}

					defer func() {
						if r := recover(); r != nil {
							repoTx.Rollback()
							c.errorChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý repo: %v", r)
						}
					}()

					// Xử lý repo
					repoModel, isSkipped, err := c.crawlRepo(repoTx, repo)
					if err != nil {
						repoTx.Rollback()
						c.errorChan <- err
						return
					}

					if isSkipped {
						repoTx.Rollback()
						return
					}

					//
					if err := repoTx.Commit().Error; err != nil {
						c.errorChan <- err
						return
					}

					//
					counterLock.Lock()
					totalRepos++
					counterLock.Unlock()

					//
					user := repo.Owner.Login
					repoName := repo.Name
					if user == "" {
						user, repoName = extractUserAndRepo(repo.FullName)
						if user == "" {
							user = "unknown"
						}
					}

					// Goroutine to rawl releases và commits
					c.backgroundWg.Add(1)
					go func() {
						defer c.backgroundWg.Done()
						releasesCtx := context.Background()
						c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoModel.ID)
					}()
				}(repo)
			}
		}

		page++

		//
		select {
		case <-doneCh:
		default:
		}
	}

	//
	mainWg.Wait()
	c.Logger.Info(ctx, "Hoàn thành crawl repositories. Các goroutine thu thập releases và commits đang được xử lý...")
	close(c.errorChan)
	c.logCrawlResults(ctx, startTime)
	return true
}

func (c *CrawlerV2) crawlReleasesAndCommitsAsync(ctx context.Context, db *gorm.DB, user, repoName string, repoID int) {
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, 1, 100)
	_, err := c.crawlReleases(ctx, db, apiCaller, user, repoName, repoID)
	if err != nil {
		c.Logger.Warn(ctx, "Lỗi khi crawl releases cho %s/%s: %v", user, repoName, err)
	}
}

func (c *CrawlerV2) errorMonitor(ctx context.Context) {
	for {
		select {
		case err, ok := <-c.errorChan:
			if !ok {
				return
			}
			if err != nil {
				c.Logger.Error(ctx, "Lỗi trong worker: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *CrawlerV2) isProcessed(repoID int64) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedRepoIDs[repoID]
}

func (c *CrawlerV2) addProcessedID(repoID int64) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedRepoIDs[repoID] = true
}

// Check if a release has been processed
func (c *CrawlerV2) isReleaseProcessed(repoID int, releaseName string) bool {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedReleaseKeys[key]
}

func (c *CrawlerV2) addProcessedRelease(repoID int, releaseName string) {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedReleaseKeys[key] = true
}

func (c *CrawlerV2) isCommitProcessed(commitHash string) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedCommitHashes[commitHash]
}

func (c *CrawlerV2) addProcessedCommit(commitHash string) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedCommitHashes[commitHash] = true
}

func (c *CrawlerV2) applyRateLimit() {
	attempts := 0
	maxAttempts := 5
	baseDelay := time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond
	for !c.rateLimiter.Allow() {
		attempts++
		if attempts > maxAttempts {
			time.Sleep(5 * time.Second)
			attempts = 0
		} else {
			delay := baseDelay * time.Duration(attempts)
			time.Sleep(delay)
		}
	}
}

func (c *CrawlerV2) isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "403") ||
		strings.Contains(err.Error(), "rate limit") ||
		strings.Contains(err.Error(), "API rate limit exceeded")
}

// Ghi log kết quả crawl
func (c *CrawlerV2) logCrawlResults(ctx context.Context, startTime time.Time) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL V2 ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
}
