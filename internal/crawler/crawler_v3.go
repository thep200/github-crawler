// Crawler v3
// Crawler GitHub API sử dụng time-based query
// Chia thành hai giai đoạn: ưu tiên thu thập 5000 repo trước, sau đó xử lý commits và releases sau

package crawler

import (
	"context"
	"fmt"
	"sort"
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

type CrawlerV3 struct {
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
	pageWaitGroup  sync.WaitGroup

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
}

// Time window để chạy time-base
type timeWindow struct {
	startDate time.Time
	endDate   time.Time
	processed bool
}

// Lưu tạm vào mem rồi sort sau
type RepositorySummary struct {
	ID        int64
	Stars     int64
	UserLogin string
	RepoName  string
	APIRepo   githubapi.GithubAPIResponse
}

func NewCrawlerV3(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV3, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)

	// Số lượng worker
	maxRepoWorkers := 10
	maxReleaseWorkers := 20
	maxCommitWorkers := 30
	maxPageWorkers := 15

	timeWindows := generateTimeWindows()
	return &CrawlerV3{
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
	}, nil
}

// Time-base
func generateTimeWindows() []timeWindow {
	windows := []timeWindow{
		{
			startDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Now(),
			processed: false,
		},
		{
			startDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		{
			startDate: time.Date(2007, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
	}
	return windows
}

func (c *CrawlerV3) getTimeBasedQueryURL(window timeWindow) string {
	baseUrl := "https://api.github.com/search/repositories"
	startDate := window.startDate.Format("2006-01-02")
	endDate := window.endDate.Format("2006-01-02")
	return fmt.Sprintf("%s?q=stars:>100+created:%s..%s&sort=stars&order=desc", baseUrl, startDate, endDate)
}

func (c *CrawlerV3) getNextTimeWindow() *timeWindow {
	c.currentWindowLock.Lock()
	defer c.currentWindowLock.Unlock()
	if c.currentWindowIdx >= len(c.timeWindows) {
		return nil
	}
	window := &c.timeWindows[c.currentWindowIdx]
	c.currentWindowIdx++
	return window
}

func (c *CrawlerV3) Crawl() bool {
	ctx := context.Background()
	startTime := time.Now()
	crawlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Monitor error
	go c.errorMonitor(crawlCtx)

	//
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Không thể kết nối đến database: %v", err)
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
	return true
}

func (c *CrawlerV3) startTimeWindowWorkers(ctx context.Context, db *gorm.DB, doneCh chan bool) {
	//
	maxConcurrentWindows := 2
	maxPagesPerWindow := 10
	perPage := 100

	//
	for i := 0; i < maxConcurrentWindows; i++ {
		c.pageWaitGroup.Add(1)
		go func(workerID int) {
			defer c.pageWaitGroup.Done()
			for {
				// Next time window
				window := c.getNextTimeWindow()
				if window == nil {
					return
				}
				url := c.getTimeBasedQueryURL(*window)
				configCopy := *c.Config
				configCopy.GithubApi.ApiUrl = url
				var startPage int32 = 0
				var windowPageWg sync.WaitGroup

				for p := 0; p < maxPagesPerWindow; p++ {
					windowPageWg.Add(1)
					go func() {
						defer windowPageWg.Done()
						for {
							currentPage := atomic.AddInt32(&startPage, 1)
							if currentPage > 10 {
								return
							}
							select {
							case <-doneCh:
								return
							case <-ctx.Done():
								return
							case c.pageWorkers <- struct{}{}:
								c.crawlTimeWindowPage(ctx, db, int(currentPage), perPage, &configCopy, doneCh)
								<-c.pageWorkers
							}
						}
					}()
				}
				windowPageWg.Wait()
			}
		}(i)
	}
}

func (c *CrawlerV3) crawlTimeWindowPage(ctx context.Context, db *gorm.DB, page, perPage int, config *cfg.Config, doneCh chan bool) {
	select {
	case <-doneCh:
		return
	default:
	}

	// Rate limiting
	c.applyRateLimit()

	// Call API với cơ chế retry
	maxRetries := 3
	var repos []githubapi.GithubAPIResponse
	var err error

	for retry := 0; retry <= maxRetries; retry++ {
		// Call API
		apiCaller := githubapi.NewCaller(c.Logger, config, page, perPage)
		repos, err = apiCaller.Call()

		if err == nil {
			break
		}

		if c.isRateLimitError(err) {
			c.Logger.Warn(ctx, "Rate limit hit ở phase 1 page %d! Đang chờ để retry...", page)
			c.handleRateLimit(ctx, err)
			continue
		} else {
			c.Logger.Error(ctx, "Error calling GitHub API (attempt %d/%d): %v", retry+1, maxRetries+1, err)
			if retry == maxRetries {
				return
			}
			time.Sleep(time.Duration(5*(retry+1)) * time.Second) // Backoff tăng dần
		}
	}

	//
	if len(repos) == 0 {
		c.Logger.Info(ctx, "No repositories found for page %d", page)
		return
	}

	//
	c.allReposMutex.Lock()
	initialCount := len(c.allRepos)
	for _, repo := range repos {
		if repo.StargazersCount < 100 {
			continue
		}
		c.allRepos = append(c.allRepos, RepositorySummary{
			ID:        repo.Id,
			Stars:     repo.StargazersCount,
			UserLogin: repo.Owner.Login,
			RepoName:  repo.Name,
			APIRepo:   repo,
		})
	}

	totalCollected := len(c.allRepos)
	newRepos := totalCollected - initialCount
	c.allReposMutex.Unlock()
	c.Logger.Info(ctx, "Đã thu thập thêm %d repositories (tổng cộng: %d)", newRepos, totalCollected)

	// Crawl 10k repositories
	if totalCollected >= 20000 {
		select {
		case <-doneCh:
		default:
			close(doneCh)
		}
	}
}

func (c *CrawlerV3) crawlReleasesAndCommitsAsync(ctx context.Context, db *gorm.DB, user, repoName string, repoID int) {
	//
	timeoutCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// New caller for release
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, 1, 100)
	var wg sync.WaitGroup
	wg.Add(1)

	// Goroutine xử lý releases
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

func (c *CrawlerV3) errorMonitor(ctx context.Context) {
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

func (c *CrawlerV3) isProcessed(repoID int64) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedRepoIDs[repoID]
}

func (c *CrawlerV3) addProcessedID(repoID int64) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedRepoIDs[repoID] = true
}

func (c *CrawlerV3) isReleaseProcessed(repoID int, releaseName string) bool {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedReleaseKeys[key]
}

func (c *CrawlerV3) addProcessedRelease(repoID int, releaseName string) {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedReleaseKeys[key] = true
}

func (c *CrawlerV3) isCommitProcessed(commitHash string) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedCommitHashes[commitHash]
}

func (c *CrawlerV3) addProcessedCommit(commitHash string) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedCommitHashes[commitHash] = true
}

func (c *CrawlerV3) applyRateLimit() {
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

func (c *CrawlerV3) isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit")
}

func (c *CrawlerV3) handleRateLimit(ctx context.Context, err error) {
	if c.isRateLimitError(err) {
		// Chờ thời gian reset của API
		waitMinutes := c.Config.GithubApi.RateLimitResetMin
		if waitMinutes <= 0 {
			waitMinutes = 60
		}
		waitTime := time.Duration(waitMinutes) * time.Minute
		c.Logger.Warn(
			ctx,
			"Rate limit của GitHub API hit!. Chờ %v để tiếp tục (đến %s)",
			waitTime.Round(time.Second), time.Now().Add(waitTime).Format(time.RFC3339),
		)

		//
		c.Logger.Info(
			ctx,
			"Tạm dừng các goroutines do hit rate limit - releases: %d, commits: %d",
			atomic.LoadInt32(&c.pendingRelease),
			atomic.LoadInt32(&c.pendingCommit),
		)

		//
		time.Sleep(waitTime)

		//
		c.rateLimiter = limiter.NewRateLimiter(c.Config.GithubApi.RequestsPerSecond)
	}
}

func (c *CrawlerV3) processTopRepositories(ctx context.Context, db *gorm.DB) {
	// Sort repositories theo số sao giảm dần
	c.allReposMutex.Lock()
	sort.Slice(c.allRepos, func(i, j int) bool {
		return c.allRepos[i].Stars > c.allRepos[j].Stars
	})

	// Chỉ lấy repositories có số sao cao nhất để xử lý
	c.allReposMutex.Unlock()

	// Reset counter
	atomic.StoreInt32(&c.repoCount, 0)

	// Semaphore cho worker xử lý repositories
	repoSemaphore := make(chan struct{}, cap(c.repoWorkers))

	//
	var failedRepos []githubapi.GithubAPIResponse
	var failedReposMutex sync.Mutex
	var processedCount int32 = 0
	repoIndex := 0

	// Process repositories until we reach the target count
	for atomic.LoadInt32(&c.repoCount) < c.maxRepos && repoIndex < len(c.allRepos) {
		select {
		case <-ctx.Done():
			return
		case repoSemaphore <- struct{}{}:
			c.backgroundWg.Add(1)
			repoSummary := c.allRepos[repoIndex]
			repoIndex++

			go func(repo githubapi.GithubAPIResponse, index int) {
				defer c.backgroundWg.Done()
				defer func() { <-repoSemaphore }()

				//
				repoTx := db.Begin()
				if repoTx.Error != nil {
					c.errorChan <- repoTx.Error
					failedReposMutex.Lock()
					failedRepos = append(failedRepos, repo)
					failedReposMutex.Unlock()
					return
				}

				defer func() {
					if r := recover(); r != nil {
						repoTx.Rollback()
						failedReposMutex.Lock()
						failedRepos = append(failedRepos, repo)
						failedReposMutex.Unlock()
					}
				}()

				//
				repoModel, isSkipped, err := c.crawlRepo(repoTx, repo)
				if err != nil {
					repoTx.Rollback()
					c.errorChan <- err
					failedReposMutex.Lock()
					failedRepos = append(failedRepos, repo)
					failedReposMutex.Unlock()
					return
				}

				if isSkipped {
					repoTx.Rollback()
					return
				}

				//
				if err := repoTx.Commit().Error; err != nil {
					c.errorChan <- err
					failedReposMutex.Lock()
					failedRepos = append(failedRepos, repo)
					failedReposMutex.Unlock()
					return
				}

				// Tăng counter
				newCount := atomic.AddInt32(&c.repoCount, 1)
				currentProcessed := atomic.AddInt32(&processedCount, 1)
				c.Logger.Info(
					ctx,
					"Tiến độ: %d/%d (Processed: %d) - Đã xử lý %s/%s (ID: %d, Stars: %d)",
					newCount, c.maxRepos, currentProcessed, repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount,
				)

				// Xử lý releases và commits
				if newCount <= c.maxRepos {
					c.backgroundWg.Add(1)
					go func(user, repoName string, repoID int) {
						defer c.backgroundWg.Done()
						releasesCtx := context.Background()
						c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoID)
					}(repoModel.User, repoModel.Name, repoModel.ID)
				}
			}(repoSummary.APIRepo, repoIndex-1)
		}
	}

	//
	for i := 0; i < cap(repoSemaphore); i++ {
		repoSemaphore <- struct{}{}
	}

	// Retry failed repositories
	if len(failedRepos) > 0 {
		c.Logger.Info(ctx, "Có %d repositories xử lý thất bại, đang thử lại", len(failedRepos))
		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			if len(failedRepos) == 0 || atomic.LoadInt32(&c.repoCount) >= c.maxRepos {
				break
			}
			c.Logger.Info(ctx, "Lần thử lại thứ %d cho %d repositories", retry+1, len(failedRepos))

			//
			currentFailedRepos := failedRepos
			failedRepos = make([]githubapi.GithubAPIResponse, 0)

			//
			for _, repo := range currentFailedRepos {
				if atomic.LoadInt32(&c.repoCount) >= c.maxRepos {
					break
				}

				repoTx := db.Begin()
				if repoTx.Error != nil {
					continue
				}

				repoModel, isSkipped, err := c.crawlRepo(repoTx, repo)
				if err != nil || isSkipped {
					repoTx.Rollback()
					continue
				}

				if err := repoTx.Commit().Error; err != nil {
					continue
				}

				newCount := atomic.AddInt32(&c.repoCount, 1)
				c.Logger.Info(
					ctx,
					"Thử lại thành công: %d/%d - Đã xử lý %s/%s (ID: %d, Stars: %d)",
					newCount, c.maxRepos, repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount,
				)
			}
		}
	}

	// Continue processing additional repositories if we haven't reached the target
	if atomic.LoadInt32(&c.repoCount) < c.maxRepos {
		c.Logger.Info(ctx, "Chưa đủ %d repos, tiếp tục xử lý thêm repositories", c.maxRepos)

		remainingNeeded := int(c.maxRepos - atomic.LoadInt32(&c.repoCount))
		startIdx := repoIndex
		endIdx := min(len(c.allRepos), startIdx+remainingNeeded*2) // Process double the needed amount to account for skips

		for i := startIdx; i < endIdx && atomic.LoadInt32(&c.repoCount) < c.maxRepos; i++ {
			repoSummary := c.allRepos[i]
			repoTx := db.Begin()
			if repoTx.Error != nil {
				continue
			}

			repoModel, isSkipped, err := c.crawlRepo(repoTx, repoSummary.APIRepo)
			if err != nil || isSkipped {
				repoTx.Rollback()
				continue
			}

			if err := repoTx.Commit().Error; err != nil {
				continue
			}

			newCount := atomic.AddInt32(&c.repoCount, 1)
			c.Logger.Info(
				ctx,
				"Bổ sung: %d/%d - Đã xử lý %s/%s (ID: %d, Stars: %d)",
				newCount, c.maxRepos, repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount,
			)

			if newCount <= c.maxRepos {
				c.backgroundWg.Add(1)
				go func(user, repoName string, repoID int) {
					defer c.backgroundWg.Done()
					releasesCtx := context.Background()
					c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoID)
				}(repoModel.User, repoModel.Name, repoModel.ID)
			}
		}
	}

	c.Logger.Info(ctx, "Đã hoàn thành xử lý repositories: %d/%d", atomic.LoadInt32(&c.repoCount), c.maxRepos)
}

func (c *CrawlerV3) logCrawlResults(ctx context.Context, startTime time.Time) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL V3 ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
	c.Logger.Info(ctx, "Số lượng repositories đã thu thập thông tin: %d", len(c.allRepos))
	c.Logger.Info(ctx, "Số lượng repositories đã xử lý và lưu vào database: %d", atomic.LoadInt32(&c.repoCount))
	c.Logger.Info(ctx, "Số lượng releases đã crawl: %d", atomic.LoadInt32(&c.releaseCount))
	c.Logger.Info(ctx, "Số lượng commits đã crawl: %d", atomic.LoadInt32(&c.commitCount))
}

// Phase 1
func (c *CrawlerV3) collectRepositoriesPhase(ctx context.Context, db *gorm.DB) {
	// doneCh để thông báo khi đã hoàn thành phase 1
	doneCh := make(chan bool)

	// Monitor số lượng repo đã crawl được
	go func() {
		for {
			c.allReposMutex.Lock()
			repoCount := len(c.allRepos)
			c.allReposMutex.Unlock()
			if repoCount >= 20000 {
				c.Logger.Info(ctx, "Đã crawl đủ thông tin %d repositories trong phase 1", repoCount)
				close(doneCh)
				return
			}
			c.Logger.Info(ctx, "Đang crawl repositories phase 1: %d/20000", repoCount)
			time.Sleep(5 * time.Second)
		}
	}()

	//
	c.startTimeWindowWorkers(ctx, db, doneCh)

	//
	waitCh := make(chan struct{})
	go func() {
		c.pageWaitGroup.Wait()
		close(waitCh)
	}()

	<-waitCh
	c.Logger.Info(ctx, "Phase 1: Crawl repositories hoàn tất với %d repos", len(c.allRepos))

	//
	c.allReposMutex.Lock()
	sort.Slice(c.allRepos, func(i, j int) bool {
		return c.allRepos[i].Stars > c.allRepos[j].Stars
	})
	c.allReposMutex.Unlock()
	c.Logger.Info(ctx, "Đã sắp xếp %d repositories theo số sao giảm dần, sẵn sàng cho phase 2", len(c.allRepos))

	close(c.firstPhaseDone)
}

// Phase 2
func (c *CrawlerV3) processRepositoriesPhase(ctx context.Context, db *gorm.DB) {
	// Sort repositories theo số sao giảm dần
	c.processTopRepositories(ctx, db)
	waitCh := make(chan struct{})

	//
	go func() {
		c.backgroundWg.Wait()
		close(waitCh)
		close(c.secondPhaseDone)
	}()

	//
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-waitCh:
			repoCount := atomic.LoadInt32(&c.repoCount)
			if repoCount >= c.maxRepos {
				c.Logger.Info(ctx, "Tất cả repositories, releases và commits đã được xử lý xong")
				c.crawlComplete = true
				return
			} else {
				c.Logger.Info(ctx, "Đã xử lý xong %d/%d repositories, cần thu thập thêm", repoCount, c.maxRepos)
				c.processMoreRepositories(ctx, db, int(c.maxRepos-repoCount))
				c.crawlComplete = true
				return
			}
		case <-ticker.C:
			pendingReleases := atomic.LoadInt32(&c.pendingRelease)
			pendingCommits := atomic.LoadInt32(&c.pendingCommit)
			repoCount := atomic.LoadInt32(&c.repoCount)

			c.Logger.Info(
				ctx,
				"Đã xử lý %d/%d repositories, %d releases, %d commits. Đang còn %d releases và %d commits đang xử lý",
				repoCount, c.maxRepos,
				atomic.LoadInt32(&c.releaseCount),
				atomic.LoadInt32(&c.commitCount),
				pendingReleases,
				pendingCommits,
			)

			// Chỉ hoàn thành khi đạt đủ 3 điều kiện:
			// 1. Đã xử lý đủ số repos cần thiết
			// 2. Không còn release nào đang chờ xử lý
			// 3. Không còn commit nào đang chờ xử lý
			if repoCount >= c.maxRepos &&
				pendingReleases == 0 &&
				pendingCommits == 0 &&
				!c.crawlComplete {
				c.Logger.Info(ctx, "Đã hoàn thành crawl đủ %d repositories và tất cả releases, commits", c.maxRepos)
				c.crawlComplete = true
			}
		}
	}
}

//
func (c *CrawlerV3) processMoreRepositories(ctx context.Context, db *gorm.DB, remainingCount int) {
	c.Logger.Info(ctx, "Bắt đầu xử lý thêm %d repositories để đạt đủ %d", remainingCount, c.maxRepos)
	var startIndex int
	c.allReposMutex.Lock()
	for i, repoSummary := range c.allRepos {
		if !c.isProcessed(repoSummary.ID) {
			startIndex = i
			break
		}
	}
	c.allReposMutex.Unlock()

	// Xử lý thêm repositories
	processed := 0
	for i := startIndex; i < len(c.allRepos) && processed < remainingCount; i++ {
		c.allReposMutex.Lock()
		if i >= len(c.allRepos) {
			c.allReposMutex.Unlock()
			break
		}
		repoSummary := c.allRepos[i]
		c.allReposMutex.Unlock()

		if c.isProcessed(repoSummary.ID) {
			continue
		}

		repoTx := db.Begin()
		if repoTx.Error != nil {
			c.Logger.Error(ctx, "Lỗi khi bắt đầu transaction: %v", repoTx.Error)
			continue
		}

		repoModel, isSkipped, err := c.crawlRepo(repoTx, repoSummary.APIRepo)
		if err != nil || isSkipped {
			repoTx.Rollback()
			if err != nil {
				c.Logger.Error(ctx, "Lỗi khi crawl repo %s/%s: %v", repoSummary.UserLogin, repoSummary.RepoName, err)
			}
			continue
		}

		if err := repoTx.Commit().Error; err != nil {
			c.Logger.Error(ctx, "Lỗi khi commit transaction: %v", err)
			continue
		}

		newCount := atomic.AddInt32(&c.repoCount, 1)
		processed++

		c.Logger.Info(
			ctx,
			"Bổ sung thêm: %d/%d - Đã xử lý %s/%s (ID: %d, Stars: %d)",
			newCount, c.maxRepos, repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount,
		)

		//
		c.backgroundWg.Add(1)
		go func(user, repoName string, repoID int) {
			defer c.backgroundWg.Done()
			releasesCtx := context.Background()
			c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoID)
		}(repoModel.User, repoModel.Name, repoModel.ID)
	}

	c.Logger.Info(ctx, "Đã xử lý thêm được %d/%d repositories cần thiết", processed, remainingCount)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
