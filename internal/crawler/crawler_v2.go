// Crawler version 2
// Crawler áp dụng concurrency để tăng tốc việc thu thập dữ liệu

package crawler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

	// Counters for tracking progress
	repoCount     int32
	releaseCount  int32
	commitCount   int32
	maxRepos      int32
	pageWorkers   chan struct{}
	pageWaitGroup sync.WaitGroup
}

func NewCrawlerV2(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV2, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)

	//
	maxRepoWorkers := 10    // Increased from 5
	maxReleaseWorkers := 20 // Increased from 10
	maxCommitWorkers := 30  // Increased from 20
	maxPageWorkers := 15    // Increased from 10

	return &CrawlerV2{
		Logger:                logger,
		Config:                config,
		Mysql:                 mysql,
		RepoMd:                repoMd,
		ReleaseMd:             releaseMd,
		CommitMd:              commitMd,
		rateLimiter:           rateLimiter,
		processedRepoIDs:      make(map[int64]bool, 10000),  // Increased capacity
		processedReleaseKeys:  make(map[string]bool, 20000), // Increased capacity
		processedCommitHashes: make(map[string]bool, 40000), // Increased capacity
		processedLock:         sync.RWMutex{},
		repoWorkers:           make(chan struct{}, maxRepoWorkers),
		releaseWorkers:        make(chan struct{}, maxReleaseWorkers),
		commitWorkers:         make(chan struct{}, maxCommitWorkers),
		pageWorkers:           make(chan struct{}, maxPageWorkers),
		errorChan:             make(chan error, 200), // Increased buffer size
		backgroundWg:          sync.WaitGroup{},
		repoCount:             0,
		releaseCount:          0,
		commitCount:           0,
		maxRepos:              10000, // Increased from 5000
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

	//
	db, err := c.Mysql.Db()
	if err != nil {
		return false
	}

	//
	doneCh := make(chan bool)

	//
	go func() {
		for {
			select {
			case <-crawlCtx.Done():
				close(doneCh)
				return
			default:
				if atomic.LoadInt32(&c.repoCount) >= c.maxRepos {
					close(doneCh)
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	//
	maxConcurrentPages := 10
	perPage := 100

	//
	var startPage int32 = 0
	for i := 0; i < maxConcurrentPages; i++ {
		c.pageWaitGroup.Add(1)
		go func(pageOffset int) {
			defer c.pageWaitGroup.Done()

			for {
				currentPage := atomic.AddInt32(&startPage, 1)
				select {
				case <-doneCh:
					return
				case c.pageWorkers <- struct{}{}:
					c.crawlPage(crawlCtx, db, int(currentPage), perPage, doneCh)
					<-c.pageWorkers
				}
			}
		}(i)
	}

	//
	go func() {
		c.pageWaitGroup.Wait()
		c.backgroundWg.Wait()
	}()

	//
	waitTime := 60 * time.Minute
	time.Sleep(waitTime)

	close(c.errorChan)
	c.logCrawlResults(ctx, startTime)
	return true
}

func (c *CrawlerV2) crawlPage(ctx context.Context, db *gorm.DB, page, perPage int, doneCh chan bool) {
	//
	c.applyRateLimit()

	//
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, page, perPage)

	//
	repos, err := apiCaller.Call()
	if err != nil {
		if c.isRateLimitError(err) {
			time.Sleep(60 * time.Second)
			if _, err = apiCaller.Call(); err != nil {
				return
			}
		}

		return
	}

	//
	if len(repos) == 0 {
		return
	}

	//
	for _, repo := range repos {
		select {
		case <-doneCh:
			return
		case c.repoWorkers <- struct{}{}:
			if atomic.LoadInt32(&c.repoCount) >= c.maxRepos {
				<-c.repoWorkers
				return
			}

			//
			go func(repo githubapi.GithubAPIResponse) {
				defer func() { <-c.repoWorkers }()

				// Bắt đầu transaction
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

				// Commit transaction
				if err := repoTx.Commit().Error; err != nil {
					c.errorChan <- err
					return
				}

				// Tăng counter
				atomic.AddInt32(&c.repoCount, 1)

				// Extract user và repo name
				user := repo.Owner.Login
				repoName := repo.Name
				if user == "" {
					user, repoName = extractUserAndRepo(repo.FullName)
					if user == "" {
						user = "unknown"
					}
				}

				// Crawl releases và commits bất đồng bộ
				c.backgroundWg.Add(1)
				go func() {
					defer c.backgroundWg.Done()
					releasesCtx := context.Background()
					c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoModel.ID)
				}()
			}(repo)
		}
	}
}

func (c *CrawlerV2) crawlReleasesAndCommitsAsync(ctx context.Context, db *gorm.DB, user, repoName string, repoID int) {
	//
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	//
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, 1, 100)

	//
	var wg sync.WaitGroup
	wg.Add(1)

	//
	go func() {
		defer wg.Done()
		releases, err := c.crawlReleases(timeoutCtx, db, apiCaller, user, repoName, repoID)
		if err != nil {
			c.Logger.Warn(timeoutCtx, "Lỗi khi crawl releases cho %s/%s: %v", user, repoName, err)
		} else {
			c.Logger.Info(timeoutCtx, "Đã crawl %d releases cho %s/%s", len(releases), user, repoName)
		}
	}()

	//
	wg.Wait()
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

// Ghi log kết quả crawl với thông tin chi tiết hơn
func (c *CrawlerV2) logCrawlResults(ctx context.Context, startTime time.Time) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL V2 ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
	c.Logger.Info(ctx, "Số lượng repositories đã crawl: %d", atomic.LoadInt32(&c.repoCount))
	c.Logger.Info(ctx, "Số lượng releases đã crawl: %d", atomic.LoadInt32(&c.releaseCount))
	c.Logger.Info(ctx, "Số lượng commits đã crawl: %d", atomic.LoadInt32(&c.commitCount))
}
