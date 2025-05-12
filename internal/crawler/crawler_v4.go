// Crawler v4
// Dựa trên CrawlerV3 nhưng sử dụng Kafka thay vì ghi trực tiếp vào database
// Gửi dữ liệu vào các Kafka topic và có các consumer để xử lý dữ liệu

package crawler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/limiter"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	kafkapkg "github.com/thep200/github-crawler/pkg/kafka"
	"github.com/thep200/github-crawler/pkg/log"
)

type CrawlerV4 struct {
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

	// Worker channels
	repoWorkers    chan struct{}
	releaseWorkers chan struct{}
	commitWorkers  chan struct{}
	errorChan      chan error
	backgroundWg   sync.WaitGroup

	// Counters
	repoCount      int32
	releaseCount   int32
	commitCount    int32
	pendingRelease int32
	pendingCommit  int32
	maxRepos       int32
	pageWorkers    chan struct{}
	// pageWaitGroup  sync.WaitGroup

	// Time-based
	timeWindows       []timeWindow
	currentWindowLock sync.Mutex
	currentWindowIdx  int

	// Phase 1 monitor
	allReposMutex sync.Mutex
	allRepos      []RepositorySummary

	// Phase control
	firstPhaseDone  chan struct{}
	secondPhaseDone chan struct{}
	crawlComplete   bool

	// Kafka producers
	repoProducer    *kafkapkg.Producer
	releaseProducer *kafkapkg.Producer
	commitProducer  *kafkapkg.Producer
}

func NewCrawlerV4(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV4, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)

	// Số lượng worker
	maxRepoWorkers := 10
	maxReleaseWorkers := 20
	maxCommitWorkers := 30
	maxPageWorkers := 15

	// Khởi tạo Kafka producers
	repoProducer := kafkapkg.NewProducer(config, logger, config.Kafka.Producer.TopicRepo)
	releaseProducer := kafkapkg.NewProducer(config, logger, config.Kafka.Producer.TopicRelease)
	commitProducer := kafkapkg.NewProducer(config, logger, config.Kafka.Producer.TopicCommit)

	timeWindows := generateTimeWindows()
	return &CrawlerV4{
		Logger:                logger,
		Config:                config,
		Mysql:                 mysql,
		RepoMd:                repoMd,
		ReleaseMd:             releaseMd,
		CommitMd:              commitMd,
		rateLimiter:           rateLimiter,
		processedRepoIDs:      make(map[int64]bool, 10000),
		processedReleaseKeys:  make(map[string]bool, 20000),
		processedCommitHashes: make(map[string]bool, 40000),
		processedLock:         sync.RWMutex{},
		repoWorkers:           make(chan struct{}, maxRepoWorkers),
		releaseWorkers:        make(chan struct{}, maxReleaseWorkers),
		commitWorkers:         make(chan struct{}, maxCommitWorkers),
		pageWorkers:           make(chan struct{}, maxPageWorkers),
		errorChan:             make(chan error, 200),
		backgroundWg:          sync.WaitGroup{},
		repoCount:             0,
		releaseCount:          0,
		commitCount:           0,
		maxRepos:              5000,
		timeWindows:           timeWindows,
		currentWindowIdx:      0,
		allReposMutex:         sync.Mutex{},
		allRepos:              make([]RepositorySummary, 0, 20000),
		firstPhaseDone:        make(chan struct{}),
		secondPhaseDone:       make(chan struct{}),
		repoProducer:          repoProducer,
		releaseProducer:       releaseProducer,
		commitProducer:        commitProducer,
	}, nil
}

func (c *CrawlerV4) Crawl() bool {
	ctx := context.Background()
	startTime := time.Now()
	crawlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Monitor error
	go c.errorMonitor(crawlCtx)

	//
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Failed to get database connection: %v", err)
		return false
	}

	// Phase 1
	c.Logger.Info(ctx, "===== Phase 1 =====")
	c.collectRepositoriesPhase(ctx, db)

	// Phase 2
	c.Logger.Info(ctx, "===== Phase 2 =====")
	c.processRepositoriesPhase(ctx, db)

	// Logging
	close(c.errorChan)
	c.logCrawlResults(ctx, startTime)

	// Close Kafka producers
	if err := c.repoProducer.Close(); err != nil {
		c.Logger.Error(ctx, "Error closing repo producer: %v", err)
	}
	if err := c.releaseProducer.Close(); err != nil {
		c.Logger.Error(ctx, "Error closing release producer: %v", err)
	}
	if err := c.commitProducer.Close(); err != nil {
		c.Logger.Error(ctx, "Error closing commit producer: %v", err)
	}

	return true
}

// Time-base
func (c *CrawlerV4) getTimeBasedQueryURL(window timeWindow) string {
	baseUrl := "https://api.github.com/search/repositories"
	startDate := window.startDate.Format("2006-01-02")
	endDate := window.endDate.Format("2006-01-02")
	return fmt.Sprintf("%s?q=stars:>100+created:%s..%s&sort=stars&order=desc", baseUrl, startDate, endDate)
}

func (c *CrawlerV4) getNextTimeWindow() *timeWindow {
	c.currentWindowLock.Lock()
	defer c.currentWindowLock.Unlock()

	if c.currentWindowIdx >= len(c.timeWindows) {
		return nil
	}
	window := &c.timeWindows[c.currentWindowIdx]
	c.currentWindowIdx++
	return window
}

// Các methods khác được sao chép từ CrawlerV3
// ...

func (c *CrawlerV4) errorMonitor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-c.errorChan:
			if err != nil {
				c.Logger.Error(ctx, "Error during crawling: %v", err)
			}
		}
	}
}

func (c *CrawlerV4) isProcessed(repoID int64) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedRepoIDs[repoID]
}

func (c *CrawlerV4) addProcessedID(repoID int64) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedRepoIDs[repoID] = true
}

func (c *CrawlerV4) isReleaseProcessed(repoID int, releaseName string) bool {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedReleaseKeys[key]
}

func (c *CrawlerV4) addProcessedRelease(repoID int, releaseName string) {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedReleaseKeys[key] = true
}

func (c *CrawlerV4) isCommitProcessed(commitHash string) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedCommitHashes[commitHash]
}

func (c *CrawlerV4) addProcessedCommit(commitHash string) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedCommitHashes[commitHash] = true
}

func (c *CrawlerV4) applyRateLimit() {
	attempts := 0
	maxAttempts := 5
	baseDelay := time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond

	for !c.rateLimiter.Allow() {
		attempts++
		if attempts > maxAttempts {
			time.Sleep(baseDelay * 5)
			return
		}
		time.Sleep(baseDelay)
	}
}

func (c *CrawlerV4) isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit")
}

func (c *CrawlerV4) handleRateLimit(ctx context.Context, err error) {
	if c.isRateLimitError(err) {
		// Extract reset time from error if available
		resetTime := c.extractResetTime(err)
		if resetTime > 0 {
			waitDuration := time.Duration(resetTime) * time.Second
			c.Logger.Warn(ctx, "Hit rate limit, waiting until reset: %v (approximately %v)",
				time.Now().Add(waitDuration).Format(time.RFC3339), waitDuration)
			time.Sleep(waitDuration)
		} else {
			// Fall back to the configured reset time if we couldn't extract it
			resetDuration := time.Duration(c.Config.GithubApi.RateLimitResetMin) * time.Minute
			c.Logger.Warn(ctx, "Hit rate limit, sleeping for %v", resetDuration)
			time.Sleep(resetDuration)
		}
	}
}

// extractResetTime attempts to extract the rate limit reset time from error messages
// Returns seconds until reset or 0 if couldn't extract
func (c *CrawlerV4) extractResetTime(err error) int64 {
	errMsg := err.Error()

	// Try to find reset timestamp in formats like "Reset in 1234 seconds" or "x-ratelimit-reset: 1234"
	resetIndex := strings.Index(strings.ToLower(errMsg), "reset in ")
	if resetIndex != -1 {
		resetStr := errMsg[resetIndex+9:] // Skip "reset in "
		endIndex := strings.Index(resetStr, " ")
		if endIndex != -1 {
			resetStr = resetStr[:endIndex]
			if seconds, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
				return seconds
			}
		}
	}

	// Try to find x-ratelimit-reset header value
	resetIndex = strings.Index(strings.ToLower(errMsg), "x-ratelimit-reset:")
	if resetIndex != -1 {
		resetStr := errMsg[resetIndex+18:] // Skip "x-ratelimit-reset:"
		endIndex := strings.Index(resetStr, "\n")
		if endIndex != -1 {
			resetStr = resetStr[:endIndex]
		}
		resetStr = strings.TrimSpace(resetStr)
		if timestamp, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			// Convert Unix timestamp to seconds from now
			now := time.Now().Unix()
			if timestamp > now {
				return timestamp - now
			}
			return 60 // At least wait a minute if timestamp seems to be in the past
		}
	}

	return 0
}

func (c *CrawlerV4) logCrawlResults(ctx context.Context, startTime time.Time) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL V4 ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
	c.Logger.Info(ctx, "Số lượng repositories đã thu thập thông tin: %d", len(c.allRepos))
	c.Logger.Info(ctx, "Số lượng repositories đã gửi vào Kafka: %d", atomic.LoadInt32(&c.repoCount))
	c.Logger.Info(ctx, "Số lượng releases đã gửi vào Kafka: %d", atomic.LoadInt32(&c.releaseCount))
	c.Logger.Info(ctx, "Số lượng commits đã gửi vào Kafka: %d", atomic.LoadInt32(&c.commitCount))
}
