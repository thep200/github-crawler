// filepath: /Users/thep200/Projects/Study/github-crawler/internal/crawler/crawler_v3.go
// Crawler version 3
// Crawler vượt qua giới hạn GitHub API bằng cách sử dụng chiến lược time-based query
// để crawl chính xác 5000 repositories với số sao cao nhất.

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

	// Counters for tracking progress
	repoCount     int32
	releaseCount  int32
	commitCount   int32
	maxRepos      int32
	pageWorkers   chan struct{}
	pageWaitGroup sync.WaitGroup

	// Time-based crawling
	timeWindows       []timeWindow
	currentWindowLock sync.Mutex
	currentWindowIdx  int

	// Thêm một mutex và slice để theo dõi tất cả repositories đã crawl
	allReposMutex sync.Mutex
	allRepos      []RepositorySummary
}

// Cấu trúc để định nghĩa một cửa sổ thời gian cho việc tìm kiếm
type timeWindow struct {
	startDate time.Time
	endDate   time.Time
	processed bool
}

// Cấu trúc lưu thông tin tóm tắt về repository để sắp xếp theo số sao
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

	// Cấu hình số lượng worker tối đa
	maxRepoWorkers := 10
	maxReleaseWorkers := 20
	maxCommitWorkers := 30
	maxPageWorkers := 15

	// Tạo các time windows cho việc tìm kiếm
	// Cần tạo đủ windows để bao quát toàn bộ dữ liệu cần crawl
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
		maxRepos:              5000, // Mục tiêu là 5000 repos
		timeWindows:           timeWindows,
		currentWindowIdx:      0,
		allReposMutex:         sync.Mutex{},
		allRepos:              make([]RepositorySummary, 0, 10000), // Dự kiến lưu trữ nhiều hơn để có thể sắp xếp
	}, nil
}

// Tạo các khoảng thời gian để query GitHub API với ưu tiên cho repo mới và nhiều sao
func generateTimeWindows() []timeWindow {
	windows := []timeWindow{
		// Khoảng thời gian đầu tiên: repos rất mới và rất phổ biến
		{
			startDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Now(),
			processed: false,
		},
		// Khoảng thời gian thứ hai: 2023 - repos phổ biến gần đây
		{
			startDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		// Khoảng thời gian thứ ba: 2022
		{
			startDate: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		// Khoảng thời gian thứ tư: 2021
		{
			startDate: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		// Khoảng thời gian thứ năm: 2019-2020
		{
			startDate: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		// Khoảng thời gian thứ sáu: 2016-2018
		{
			startDate: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		// Khoảng thời gian thứ bảy: 2012-2015
		{
			startDate: time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
		// Khoảng thời gian thứ tám: Cũ nhất (trước 2012)
		{
			startDate: time.Date(2007, 1, 1, 0, 0, 0, 0, time.UTC),
			endDate:   time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
			processed: false,
		},
	}
	return windows
}

// Lấy URL truy vấn với thông số thời gian cụ thể
func (c *CrawlerV3) getTimeBasedQueryURL(window timeWindow) string {
	baseUrl := "https://api.github.com/search/repositories"
	startDate := window.startDate.Format("2006-01-02")
	endDate := window.endDate.Format("2006-01-02")

	// Tạo query với điều kiện thời gian
	// q=stars:>1+created:{start_date}..{end_date}&sort=stars&order=desc
	query := fmt.Sprintf("?q=stars:>100+created:%s..%s&sort=stars&order=desc", startDate, endDate)

	return baseUrl + query
}

// Lấy time window tiếp theo để xử lý
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
	c.Logger.Info(ctx, "Bắt đầu crawl dữ liệu repository GitHub với chiến lược time-based query %s", startTime.Format(time.RFC3339))

	// Tạo context với khả năng hủy
	crawlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Khởi chạy goroutine giám sát lỗi
	go c.errorMonitor(crawlCtx)

	// Kết nối database
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Không thể kết nối đến database: %v", err)
		return false
	}

	// Kênh để thông báo hoàn thành
	doneCh := make(chan bool)

	// Goroutine kiểm tra số lượng repo đã crawl
	go func() {
		for {
			select {
			case <-crawlCtx.Done():
				close(doneCh)
				return
			default:
				if atomic.LoadInt32(&c.repoCount) >= c.maxRepos {
					c.Logger.Info(ctx, "Đã đạt mục tiêu %d repositories, dừng quá trình crawl", c.maxRepos)
					close(doneCh)
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	// Khởi chạy các worker xử lý time windows
	c.startTimeWindowWorkers(crawlCtx, db, doneCh)

	// Đợi các workers hoàn thành crawl
	select {
	case <-time.After(90 * time.Minute): // Giảm timeout xuống để xử lý sớm hơn
		c.Logger.Info(ctx, "Timeout reached, finalizing crawl process")
	case <-doneCh:
		c.Logger.Info(ctx, "Crawl process completed")
	}

	// Đợi các tiến trình nền hoàn tất
	waitCh := make(chan struct{})
	go func() {
		c.pageWaitGroup.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		c.Logger.Info(ctx, "All page crawling tasks completed")
	case <-time.After(5 * time.Minute):
		c.Logger.Warn(ctx, "Timed out waiting for page tasks, processing collected repositories")
	}

	// Xử lý 5000 repos có số sao cao nhất từ danh sách đã crawl
	c.processTopRepositories(ctx, db)

	// Đợi tiến trình xử lý repository hoàn tất
	finalWaitCh := make(chan struct{})
	go func() {
		c.backgroundWg.Wait()
		close(finalWaitCh)
	}()

	select {
	case <-finalWaitCh:
		c.Logger.Info(ctx, "All repository processing tasks completed")
	case <-time.After(15 * time.Minute):
		c.Logger.Warn(ctx, "Timed out waiting for repository processing, finalizing")
	}

	// Ghi log kết quả
	close(c.errorChan)
	c.logCrawlResults(ctx, startTime)
	return true
}

func (c *CrawlerV3) startTimeWindowWorkers(ctx context.Context, db *gorm.DB, doneCh chan bool) {
	// Số lượng time windows xử lý đồng thời
	maxConcurrentWindows := 2

	// Số lượng trang tối đa cho mỗi time window
	maxPagesPerWindow := 10

	// Số lượng kết quả trên mỗi trang
	perPage := 100

	for i := 0; i < maxConcurrentWindows; i++ {
		c.pageWaitGroup.Add(1)
		go func(workerID int) {
			defer c.pageWaitGroup.Done()

			for {
				// Lấy time window tiếp theo
				window := c.getNextTimeWindow()
				if window == nil {
					return
				}

				c.Logger.Info(ctx, "Worker %d bắt đầu xử lý time window từ %s đến %s",
					workerID, window.startDate.Format("2006-01-02"), window.endDate.Format("2006-01-02"))

				// Thực hiện crawl cho time window này
				url := c.getTimeBasedQueryURL(*window)

				// Override URL trong config
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
							if currentPage > 10 { // GitHub chỉ cho phép tối đa 10 trang
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
	// Áp dụng rate limiting
	c.applyRateLimit()

	// Tạo caller mới với URL time-based
	apiCaller := githubapi.NewCaller(c.Logger, config, page, perPage)

	// Gọi API
	repos, err := apiCaller.Call()
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Warn(ctx, "Rate limit hit, sleeping for 60 seconds: %v", err)
			time.Sleep(60 * time.Second)
			repos, err = apiCaller.Call()
			if err != nil {
				c.Logger.Error(ctx, "Error after rate limit wait: %v", err)
				return
			}
		} else {
			c.Logger.Error(ctx, "Error calling GitHub API: %v", err)
			return
		}
	}

	// Không có kết quả
	if len(repos) == 0 {
		c.Logger.Info(ctx, "No repositories found for page %d", page)
		return
	}

	c.Logger.Info(ctx, "Found %d repositories on page %d", len(repos), page)

	// Thu thập thông tin repositories để sắp xếp sau này
	c.allReposMutex.Lock()
	for _, repo := range repos {
		// Chỉ thu thập thông tin, chưa xử lý
		c.allRepos = append(c.allRepos, RepositorySummary{
			ID:        repo.Id,
			Stars:     repo.StargazersCount,
			UserLogin: repo.Owner.Login,
			RepoName:  repo.Name,
			APIRepo:   repo,
		})
	}

	totalCollected := len(c.allRepos)
	c.allReposMutex.Unlock()

	c.Logger.Info(ctx, "Đã thu thập thông tin %d repositories", totalCollected)
}

func (c *CrawlerV3) crawlReleasesAndCommitsAsync(ctx context.Context, db *gorm.DB, user, repoName string, repoID int) {
	// Giới hạn thời gian tối đa cho mỗi repository
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// Tạo caller mới cho releases
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, 1, 100)

	// Xử lý releases và commits
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

	// Đợi tất cả goroutines hoàn thành
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

// Check if a release has been processed
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
	return strings.Contains(err.Error(), "403") ||
		strings.Contains(err.Error(), "rate limit") ||
		strings.Contains(err.Error(), "đạt giới hạn API")
}

func (c *CrawlerV3) handleRateLimit(ctx context.Context, err error) {
	if c.isRateLimitError(err) {
		waitMinutes := c.Config.GithubApi.RateLimitResetMin
		if waitMinutes <= 0 {
			waitMinutes = 60 // Mặc định 60 phút nếu không có cấu hình
		}

		// Lấy thời gian reset cụ thể nếu có
		var resetTime time.Time
		var resetTimeStr string
		if strings.Contains(err.Error(), "thời gian reset:") {
			parts := strings.Split(err.Error(), "thời gian reset:")
			if len(parts) > 1 {
				resetTimeStr = strings.TrimSpace(parts[1])
				parsedTime, parseErr := time.Parse(time.RFC3339, resetTimeStr)
				if parseErr == nil {
					resetTime = parsedTime
				}
			}
		}

		// Tính toán thời gian chờ
		waitTime := time.Duration(waitMinutes) * time.Minute
		if !resetTime.IsZero() {
			// Nếu có thời gian reset cụ thể, sử dụng nó
			now := time.Now()
			calculatedWaitTime := resetTime.Sub(now)
			if calculatedWaitTime > 0 {
				waitTime = calculatedWaitTime
			}
		}

		c.Logger.Warn(ctx, "🚫 Rate limit của GitHub API đạt ngưỡng. Chờ %v để tiếp tục (đến %s)",
			waitTime.Round(time.Second), time.Now().Add(waitTime).Format(time.RFC3339))

		time.Sleep(waitTime)

		c.Logger.Info(ctx, "✅ Đã hết thời gian chờ rate limit, tiếp tục crawl")
	}
}

func (c *CrawlerV3) processTopRepositories(ctx context.Context, db *gorm.DB) {
	c.Logger.Info(ctx, "Bắt đầu xử lý top repositories theo số sao")

	c.allReposMutex.Lock()
	// Sắp xếp repositories theo số sao giảm dần
	sort.Slice(c.allRepos, func(i, j int) bool {
		return c.allRepos[i].Stars > c.allRepos[j].Stars
	})

	// Giới hạn chỉ lấy 5000 repositories có số sao cao nhất
	topRepos := c.allRepos
	if len(topRepos) > 5000 {
		topRepos = topRepos[:5000]
		c.Logger.Info(ctx, "Đã lọc xuống còn 5000 repositories có số sao cao nhất từ %d repositories", len(c.allRepos))
	} else {
		c.Logger.Info(ctx, "Có tổng cộng %d repositories, tất cả đều được xử lý", len(topRepos))
	}
	c.allReposMutex.Unlock()

	// Reset counter để đếm lại chính xác
	atomic.StoreInt32(&c.repoCount, 0)

	// Xử lý từng repository trong danh sách top
	for _, repoSummary := range topRepos {
		// Thêm vào worker để xử lý tiếp repo đã được lọc
		c.backgroundWg.Add(1)
		go func(repo githubapi.GithubAPIResponse) {
			defer c.backgroundWg.Done()

			// Tạo transaction mới
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

			// Xử lý repository
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
			newCount := atomic.AddInt32(&c.repoCount, 1)
			c.Logger.Info(ctx, "Xử lý repository thứ %d/%d (ID: %d, Stars: %d, Name: %s/%s)",
				newCount, len(topRepos), repoModel.ID, repoModel.StarCount, repoModel.User, repoModel.Name)

			// Thu thập thêm dữ liệu về releases và commits
			c.backgroundWg.Add(1)
			go func(user, repoName string, repoID int) {
				defer c.backgroundWg.Done()
				releasesCtx := context.Background()
				c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoID)
			}(repoModel.User, repoModel.Name, repoModel.ID)

		}(repoSummary.APIRepo)
	}
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
