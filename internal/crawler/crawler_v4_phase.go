package crawler

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thep200/github-crawler/cfg"
	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"gorm.io/gorm"
)

// Phase 1: Thu thập repositories từ GitHub API
func (c *CrawlerV4) collectRepositoriesPhase(ctx context.Context, db *gorm.DB) {
	// doneCh để thông báo khi đã hoàn thành phase 1
	doneCh := make(chan bool)
	earlyProcessCh := make(chan bool, 1) // Channel to trigger early processing

	// Monitor số lượng repo đã crawl được
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				c.allReposMutex.Lock()
				repoCount := len(c.allRepos)
				c.allReposMutex.Unlock()
				c.Logger.Info(ctx, "Đã thu thập %d repositories", repoCount)

				// Check if we have enough repos for early processing
				if repoCount >= 5000 && len(earlyProcessCh) == 0 {
					c.Logger.Info(ctx, "Đạt 5000 repositories, bắt đầu xử lý sớm")
					earlyProcessCh <- true

					// Start early processing in a goroutine
					go c.startEarlyProcessing(ctx, db)
				}
			}
		}
	}()

	// Time-based crawling
	c.startTimeWindowWorkers(ctx, db, doneCh)

	// Chờ một khoảng thời gian hoặc đến khi đủ số lượng repositories
	timeout := time.After(30 * time.Minute) // Timeout sau 30 phút
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			c.Logger.Info(ctx, "Đã hết thời gian thu thập repositories (30 phút)")
			close(doneCh)
			close(c.firstPhaseDone)
			return
		case <-ticker.C:
			c.allReposMutex.Lock()
			count := len(c.allRepos)
			c.allReposMutex.Unlock()

			if count >= 7000 {
				c.Logger.Info(ctx, "Đã thu thập đủ repositories")
				close(doneCh)
				close(c.firstPhaseDone)
				return
			}
		}
	}
}

// New function to start processing repositories early while still collecting more
func (c *CrawlerV4) startEarlyProcessing(ctx context.Context, db *gorm.DB) {
	c.allReposMutex.Lock()
	// Copy the first 5000 repos for processing
	var toProcess []RepositorySummary
	if len(c.allRepos) >= 5000 {
		// Sort by stars first
		sort.Slice(c.allRepos, func(i, j int) bool {
			return c.allRepos[i].Stars > c.allRepos[j].Stars
		})
		toProcess = make([]RepositorySummary, 5000)
		copy(toProcess, c.allRepos[:5000])
	} else {
		toProcess = make([]RepositorySummary, len(c.allRepos))
		copy(toProcess, c.allRepos)
	}
	c.allReposMutex.Unlock()

	c.Logger.Info(ctx, "Bắt đầu xử lý sớm %d repositories đầu tiên", len(toProcess))

	// Process these repositories concurrently
	repoSemaphore := make(chan struct{}, cap(c.repoWorkers))
	var wg sync.WaitGroup

	for i, repoSummary := range toProcess {
		repoSemaphore <- struct{}{}
		wg.Add(1)
		go func(summary RepositorySummary, index int) {
			defer wg.Done()
			defer func() { <-repoSemaphore }()

			// Xử lý panic nếu có
			defer func() {
				if r := recover(); r != nil {
					c.Logger.Error(ctx, "Panic when early processing repo %d: %v", summary.ID, r)
				}
			}()

			// Xử lý repository - chỉ gửi lên Kafka không xử lý releases/commits
			_, alreadyProcessed, err := c.crawlRepo(ctx, db, summary.APIRepo)
			if err != nil {
				c.Logger.Error(ctx, "Error early processing repo %d: %v", summary.ID, err)
				return
			}

			if alreadyProcessed {
				return
			}

			// Cập nhật counter
			count := atomic.AddInt32(&c.repoCount, 1)
			if count%100 == 0 {
				c.Logger.Info(ctx, "Đã xử lý sớm %d/%d repositories", count, len(toProcess))
			}
		}(repoSummary, i)
	}

	wg.Wait()
	c.Logger.Info(ctx, "Hoàn thành việc xử lý sớm %d repositories", atomic.LoadInt32(&c.repoCount))
}

// Khởi động các worker để crawl repository qua các khung thời gian
func (c *CrawlerV4) startTimeWindowWorkers(ctx context.Context, db *gorm.DB, doneCh chan bool) {
	maxConcurrentWindows := 2
	maxPagesPerWindow := 10
	perPage := 100

	for i := 0; i < maxConcurrentWindows; i++ {
		c.backgroundWg.Add(1)
		go func() {
			defer c.backgroundWg.Done()
			for {
				// Kiểm tra xem phase 1 đã hoàn thành chưa
				select {
				case <-doneCh:
					return
				default:
				}

				// Lấy window tiếp theo
				window := c.getNextTimeWindow()
				if window == nil {
					return
				}

				// Create a new WaitGroup for this window
				var windowWg sync.WaitGroup

				// Crawl các trang trong window
				for page := 1; page <= maxPagesPerWindow; page++ {
					select {
					case <-doneCh:
						return
					case c.pageWorkers <- struct{}{}:
						windowWg.Add(1)
						go func(pageNum int) {
							defer windowWg.Done()
							defer func() { <-c.pageWorkers }()
							c.crawlTimeWindowPage(ctx, db, pageNum, perPage, c.Config, doneCh)
						}(page)
					}
				}

				// Đợi tất cả trang trong window được crawl xong
				windowWg.Wait()
				window.processed = true
			}
		}()
	}
}

// Crawl một trang repositories trong một khung thời gian
func (c *CrawlerV4) crawlTimeWindowPage(ctx context.Context, db *gorm.DB, page, perPage int, config *cfg.Config, doneCh chan bool) {
	select {
	case <-doneCh:
		return
	default:
	}

	// Rate limiting
	c.applyRateLimit()

	// Lấy window hiện tại
	c.currentWindowLock.Lock()
	windowIdx := c.currentWindowIdx - 1
	if windowIdx < 0 || windowIdx >= len(c.timeWindows) {
		c.currentWindowLock.Unlock()
		return
	}
	window := c.timeWindows[windowIdx]
	c.currentWindowLock.Unlock()

	// Tạo URL với time-based query và phân trang
	url := c.getTimeBasedQueryURL(window)
	url = fmt.Sprintf("%s&page=%d&per_page=%d", url, page, perPage)

	// Call API với cơ chế retry không giới hạn số lần
	var repos []githubapi.GithubAPIResponse
	var err error

	// Create a copy of the config with our custom URL
	configCopy := *config
	configCopy.GithubApi.ApiUrl = url
	apiCaller := githubapi.NewCaller(c.Logger, &configCopy, page, perPage)

	// We'll keep retrying until we succeed or the context is cancelled
	for {
		select {
		case <-doneCh:
			return
		default:
		}

		c.applyRateLimit()
		repos, err = apiCaller.Call()
		if err == nil {
			// Success, break the retry loop
			break
		}

		if c.isRateLimitError(err) {
			c.Logger.Warn(ctx, "Hit rate limit while crawling page %d, waiting for rate limit to reset", page)
			c.handleRateLimit(ctx, err)
		} else {
			// For non-rate limit errors, wait and retry
			c.Logger.Error(ctx, "Failed to crawl page %d: %v. Retrying in 10 seconds...", page, err)
			time.Sleep(10 * time.Second)
		}
	}

	if len(repos) == 0 {
		c.Logger.Info(ctx, "No repositories found for page %d", page)
		return
	}

	// Thêm repositories vào bộ nhớ tạm thời
	c.allReposMutex.Lock()
	initialCount := len(c.allRepos)
	for _, repo := range repos {
		if repo.Id > 0 {
			c.allRepos = append(c.allRepos, RepositorySummary{
				ID:        repo.Id,
				Stars:     repo.StargazersCount,
				UserLogin: repo.Owner.Login,
				RepoName:  repo.Name,
				APIRepo:   repo,
			})
		}
	}

	totalCollected := len(c.allRepos)
	newRepos := totalCollected - initialCount
	c.allReposMutex.Unlock()
	c.Logger.Info(ctx, "Đã thu thập thêm %d repositories (tổng cộng: %d) từ trang %d", newRepos, totalCollected, page)

	// Crawl 20k repositories
	if totalCollected >= 20000 {
		close(doneCh)
	}
}

// Phase 2: Xử lý repositories đã thu thập, crawl releases và commits
func (c *CrawlerV4) processRepositoriesPhase(ctx context.Context, db *gorm.DB) {
	// Sort repositories theo số sao giảm dần
	c.allReposMutex.Lock()
	sort.Slice(c.allRepos, func(i, j int) bool {
		return c.allRepos[i].Stars > c.allRepos[j].Stars
	})

	// Giới hạn số repositories cần xử lý
	toProcess := c.allRepos
	if len(toProcess) > int(c.maxRepos) {
		toProcess = toProcess[:c.maxRepos]
	}
	c.Logger.Info(ctx, "Sẽ xử lý %d repositories (từ tổng số %d đã thu thập)", len(toProcess), len(c.allRepos))
	c.allReposMutex.Unlock()

	// Xử lý repositories
	c.processTopRepositories(ctx, db)

	// Đóng channel khi hoàn thành
	close(c.secondPhaseDone)
	c.crawlComplete = true
}

// Xử lý các repositories có số sao cao nhất
func (c *CrawlerV4) processTopRepositories(ctx context.Context, db *gorm.DB) {
	// Chỉ lấy repositories có số sao cao nhất để xử lý
	c.allReposMutex.Lock()
	toProcess := c.allRepos
	if len(toProcess) > int(c.maxRepos) {
		toProcess = toProcess[:c.maxRepos]
	}
	totalToProcess := len(toProcess)
	c.allReposMutex.Unlock()

	// Reset counter
	atomic.StoreInt32(&c.repoCount, 0)

	// Semaphore cho worker xử lý repositories
	repoSemaphore := make(chan struct{}, cap(c.repoWorkers))
	var wg sync.WaitGroup

	// Process repositories until we reach the target count
	for i, repoSummary := range toProcess {
		repoSemaphore <- struct{}{}
		wg.Add(1)
		go func(summary RepositorySummary, index int) {
			defer wg.Done()
			defer func() { <-repoSemaphore }()

			repo := summary.APIRepo

			// Xử lý panic nếu có
			defer func() {
				if r := recover(); r != nil {
					c.Logger.Error(ctx, "Panic when processing repo %d: %v", repo.Id, r)
				}
			}()

			// Tạo một transaction mới
			txCtx := context.Background()

			// Xử lý repository
			repoID, alreadyProcessed, err := c.crawlRepo(txCtx, db, repo)
			if err != nil {
				c.Logger.Error(ctx, "Error processing repo %d: %v", repo.Id, err)
				return
			}

			if alreadyProcessed {
				c.Logger.Info(ctx, "Repository %d (%s/%s) đã được xử lý trước đó", repo.Id, summary.UserLogin, summary.RepoName)
				return
			}

			// Cập nhật counter
			count := atomic.AddInt32(&c.repoCount, 1)
			c.Logger.Info(
				ctx,
				"Đã xử lý %d/%d repositories, repo hiện tại: %s/%s (#%d/%d)",
				count, totalToProcess, summary.UserLogin, summary.RepoName, index+1, totalToProcess,
			)

			// Async crawl releases và commits
			c.crawlReleasesAndCommitsAsync(txCtx, db, summary.UserLogin, summary.RepoName, repoID)
		}(repoSummary, i)
	}

	// Đợi tất cả worker xử lý xong
	wg.Wait()
	c.Logger.Info(ctx, "Hoàn thành việc xử lý %d repositories", atomic.LoadInt32(&c.repoCount))
}

// Crawl releases và commits cho một repository
func (c *CrawlerV4) crawlReleasesAndCommitsAsync(ctx context.Context, db *gorm.DB, user, repoName string, repoID int) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
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
			c.Logger.Warn(timeoutCtx, "Error crawling releases for %s/%s: %v", user, repoName, err)
		} else {
			c.Logger.Info(timeoutCtx, "Processed %d releases for %s/%s", len(releases), user, repoName)
		}
	}()

	wg.Wait()
}
