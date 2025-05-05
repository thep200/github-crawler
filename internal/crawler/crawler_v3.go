// filepath: /Users/thep200/Projects/Study/github-crawler/internal/crawler/crawler_v3.go
// Crawler version 3
// Crawler vượt qua giới hạn GitHub API bằng cách sử dụng chiến lược time-based query
// với hai giai đoạn song song: ưu tiên thu thập 5000 repo trước, sau đó xử lý commits và releases

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

	// Phase control
	repoCollectionDone chan struct{} // Signal khi đã thu thập đủ repos
	secondPhaseDone    chan struct{} // Signal khi phase 2 hoàn thành
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
		repoCollectionDone:    make(chan struct{}),
		secondPhaseDone:       make(chan struct{}),
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
	c.Logger.Info(ctx, "Bắt đầu crawl dữ liệu repository GitHub với chiến lược hai giai đoạn %s", startTime.Format(time.RFC3339))

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

	// Giai đoạn 1: Thu thập thông tin về repositories
	c.Logger.Info(ctx, "===== GIAI ĐOẠN 1: THU THẬP THÔNG TIN REPOSITORIES =====")
	c.collectRepositoriesPhase(ctx, db)

	// Giai đoạn 2: Xử lý và lưu repositories, sau đó thu thập releases và commits
	c.Logger.Info(ctx, "===== GIAI ĐOẠN 2: XỬ LÝ REPOSITORIES/RELEASES/COMMITS =====")
	c.processRepositoriesPhase(ctx, db)

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
	// Kiểm tra xem đã thu thập đủ dữ liệu chưa
	select {
	case <-doneCh:
		return // Đã thu thập đủ dữ liệu, dừng xử lý
	default:
		// Tiếp tục xử lý
	}

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

	// Thu thập thông tin repositories để sau này sắp xếp và xử lý
	c.allReposMutex.Lock()
	initialCount := len(c.allRepos)

	for _, repo := range repos {
		// Chỉ thu thập thông tin, chưa xử lý repository
		if repo.StargazersCount < 100 {
			continue // Bỏ qua repositories có ít hơn 100 sao để tập trung vào những repo nổi bật
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

	// Kiểm tra xem đã thu thập đủ dữ liệu chưa
	if totalCollected >= 10000 { // Thu thập đủ dữ liệu để lọc 5000 repos tốt nhất
		select {
		case <-doneCh: // Đã đóng bởi goroutine khác
		default:
			close(doneCh) // Đóng channel để báo hiệu đã thu thập đủ dữ liệu
		}
	}
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
	c.Logger.Info(ctx, "Bắt đầu xử lý top repositories theo số sao trong giai đoạn 2")

	c.allReposMutex.Lock()
	// Sắp xếp repositories theo số sao giảm dần
	sort.Slice(c.allRepos, func(i, j int) bool {
		return c.allRepos[i].Stars > c.allRepos[j].Stars
	})

	// Giới hạn chỉ lấy 5000 repositories có số sao cao nhất
	topRepos := c.allRepos
	if len(topRepos) > int(c.maxRepos) {
		topRepos = topRepos[:c.maxRepos]
		c.Logger.Info(ctx, "Đã lọc xuống còn %d repositories có số sao cao nhất từ %d repositories đã thu thập",
			c.maxRepos, len(c.allRepos))
	} else {
		c.Logger.Info(ctx, "Có tổng cộng %d repositories, tất cả đều được xử lý", len(topRepos))
	}
	c.allReposMutex.Unlock()

	// Reset counter để đếm lại chính xác
	atomic.StoreInt32(&c.repoCount, 0)

	// Semaphore cho việc xử lý repo
	repoSemaphore := make(chan struct{}, cap(c.repoWorkers))

	// Tạo một worker pool để xử lý repo
	c.Logger.Info(ctx, "Khởi tạo %d worker để lưu repositories vào database", cap(c.repoWorkers))

	// Mảng lưu trữ các repo đã thử xử lý nhưng gặp lỗi để thử lại sau
	var failedRepos []githubapi.GithubAPIResponse
	var failedReposMutex sync.Mutex

	// Xử lý từng repository trong danh sách top
	for idx, repoSummary := range topRepos {
		select {
		case <-ctx.Done():
			c.Logger.Warn(ctx, "Xử lý repositories bị hủy bỏ do context đã đóng")
			return
		case repoSemaphore <- struct{}{}: // Áp dụng semaphore để giới hạn số lượng goroutine đồng thời
			c.backgroundWg.Add(1)
			go func(repo githubapi.GithubAPIResponse, index int) {
				defer c.backgroundWg.Done()
				defer func() { <-repoSemaphore }()

				// Tạo transaction mới
				repoTx := db.Begin()
				if repoTx.Error != nil {
					c.errorChan <- repoTx.Error
					// Thêm vào danh sách các repos cần thử lại
					failedReposMutex.Lock()
					failedRepos = append(failedRepos, repo)
					failedReposMutex.Unlock()
					return
				}

				defer func() {
					if r := recover(); r != nil {
						repoTx.Rollback()
						c.errorChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý repo: %v", r)
						// Thêm vào danh sách các repos cần thử lại
						failedReposMutex.Lock()
						failedRepos = append(failedRepos, repo)
						failedReposMutex.Unlock()
					}
				}()

				// Xử lý repository
				repoModel, isSkipped, err := c.crawlRepo(repoTx, repo)
				if err != nil {
					repoTx.Rollback()
					c.errorChan <- err
					// Thêm vào danh sách các repos cần thử lại
					failedReposMutex.Lock()
					failedRepos = append(failedRepos, repo)
					failedReposMutex.Unlock()
					return
				}

				if isSkipped {
					repoTx.Rollback()
					return
				}

				// Commit transaction
				if err := repoTx.Commit().Error; err != nil {
					c.errorChan <- err
					// Thêm vào danh sách các repos cần thử lại
					failedReposMutex.Lock()
					failedRepos = append(failedRepos, repo)
					failedReposMutex.Unlock()
					return
				}

				// Tăng counter
				newCount := atomic.AddInt32(&c.repoCount, 1)
				c.Logger.Info(ctx, "Tiến độ: %d/%d - Đã xử lý %s/%s (ID: %d, Stars: %d)",
					newCount, len(topRepos), repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount)

				// Xử lý releases và commits trong một goroutine riêng
				if index < 2000 { // Chỉ xử lý releases và commits cho 2000 repo đầu tiên với số sao cao nhất
					c.backgroundWg.Add(1)
					go func(user, repoName string, repoID int) {
						defer c.backgroundWg.Done()
						releasesCtx := context.Background()
						c.crawlReleasesAndCommitsAsync(releasesCtx, db, user, repoName, repoID)
					}(repoModel.User, repoModel.Name, repoModel.ID)
				}
			}(repoSummary.APIRepo, idx)
		}
	}

	// Đợi semaphore empty trước khi tiếp tục
	for i := 0; i < cap(repoSemaphore); i++ {
		repoSemaphore <- struct{}{}
	}

	// Thử lại các repos đã thất bại (tối đa 3 lần)
	if len(failedRepos) > 0 {
		c.Logger.Info(ctx, "Có %d repositories xử lý thất bại, đang thử lại", len(failedRepos))

		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			if len(failedRepos) == 0 {
				break
			}

			c.Logger.Info(ctx, "Lần thử lại thứ %d cho %d repositories", retry+1, len(failedRepos))

			// Tạo bản sao của danh sách repos thất bại và reset
			currentFailedRepos := failedRepos
			failedRepos = make([]githubapi.GithubAPIResponse, 0)

			// Xử lý từng repository trong danh sách các repos thất bại
			for _, repo := range currentFailedRepos {
				repoTx := db.Begin()
				if repoTx.Error != nil {
					continue
				}

				repoModel, isSkipped, err := c.crawlRepo(repoTx, repo)
				if err != nil || isSkipped {
					repoTx.Rollback()
					continue
				}

				// Commit transaction
				if err := repoTx.Commit().Error; err != nil {
					continue
				}

				// Tăng counter
				newCount := atomic.AddInt32(&c.repoCount, 1)
				c.Logger.Info(ctx, "Thử lại thành công: %d/%d - Đã xử lý %s/%s (ID: %d, Stars: %d)",
					newCount, c.maxRepos, repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount)
			}
		}
	}

	// Kiểm tra xem đã xử lý đủ số lượng repositories theo yêu cầu chưa
	finalCount := atomic.LoadInt32(&c.repoCount)
	if finalCount < c.maxRepos {
		c.Logger.Warn(ctx, "Chú ý: Chỉ xử lý được %d/%d repositories yêu cầu", finalCount, c.maxRepos)

		// Tìm thêm repositories từ danh sách đã thu thập nếu có thể
		if len(c.allRepos) > int(c.maxRepos) {
			remainingNeeded := int(c.maxRepos - finalCount)
			additionalRepos := c.allRepos[c.maxRepos:min(len(c.allRepos), int(c.maxRepos)+remainingNeeded)]

			c.Logger.Info(ctx, "Xử lý thêm %d repositories để đạt target", len(additionalRepos))

			for _, repoSummary := range additionalRepos {
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
				c.Logger.Info(ctx, "Bổ sung: %d/%d - Đã xử lý %s/%s (ID: %d, Stars: %d)",
					newCount, c.maxRepos, repoModel.User, repoModel.Name, repoModel.ID, repoModel.StarCount)

				if newCount >= c.maxRepos {
					break
				}
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

// Phase 1: Thu thập thông tin repositories
func (c *CrawlerV3) collectRepositoriesPhase(ctx context.Context, db *gorm.DB) {
	// Tạo kênh doneCh để thông báo khi đã thu thập đủ thông tin
	doneCh := make(chan bool)

	// Khởi chạy một goroutine kiểm tra số lượng repositories đã thu thập được
	go func() {
		for {
			c.allReposMutex.Lock()
			repoCount := len(c.allRepos)
			c.allReposMutex.Unlock()

			if repoCount >= 10000 { // Thu thập nhiều hơn 5000 để có thể lọc
				c.Logger.Info(ctx, "Đã thu thập đủ thông tin %d repositories, kết thúc giai đoạn 1", repoCount)
				close(doneCh)
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()

	// Khởi chạy các worker thu thập repositories
	c.startTimeWindowWorkers(ctx, db, doneCh)

	// Đợi các worker hoàn thành hoặc hết thời gian
	waitCh := make(chan struct{})
	go func() {
		c.pageWaitGroup.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		c.Logger.Info(ctx, "Tất cả các worker thu thập repositories đã hoàn thành")
	case <-time.After(30 * time.Minute): // Giới hạn thời gian cho giai đoạn 1
		c.Logger.Info(ctx, "Hết thời gian cho giai đoạn thu thập repositories")
	}

	// Hiển thị thông tin về repositories đã thu thập được
	c.allReposMutex.Lock()
	repoCount := len(c.allRepos)
	c.allReposMutex.Unlock()
	c.Logger.Info(ctx, "Giai đoạn 1 kết thúc: Đã thu thập thông tin về %d repositories", repoCount)

	// Thông báo giai đoạn 1 đã hoàn thành
	close(c.repoCollectionDone)
}

// Phase 2: Xử lý repositories và thu thập releases và commits
func (c *CrawlerV3) processRepositoriesPhase(ctx context.Context, db *gorm.DB) {
	// Sắp xếp và xử lý các repositories hàng đầu
	c.processTopRepositories(ctx, db)

	// Đặt một thời gian tối đa cho quá trình xử lý
	processTimeout := 60 * time.Minute

	// Tạo một kênh để thông báo khi đã hoàn thành
	waitCh := make(chan struct{})

	// Chờ tất cả các worker xử lý repositories hoàn thành
	go func() {
		c.backgroundWg.Wait()
		close(waitCh)
		// Đánh dấu giai đoạn 2 đã hoàn thành
		close(c.secondPhaseDone)
	}()

	// Định kỳ báo cáo tiến độ
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-waitCh:
			c.Logger.Info(ctx, "Giai đoạn 2 đã hoàn thành: Đã xử lý tất cả repositories và dữ liệu liên quan")
			return
		case <-time.After(processTimeout):
			c.Logger.Warn(ctx, "Hết thời gian cho giai đoạn 2 (%v), kết thúc quá trình", processTimeout)
			return
		case <-ticker.C:
			// Báo cáo tiến độ hiện tại
			c.Logger.Info(ctx, "Tiến độ giai đoạn 2: Đã xử lý %d repositories, %d releases, %d commits",
				atomic.LoadInt32(&c.repoCount),
				atomic.LoadInt32(&c.releaseCount),
				atomic.LoadInt32(&c.commitCount))
		}
	}
}

// helper function for integer minimum comparison
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
