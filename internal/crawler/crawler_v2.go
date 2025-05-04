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
	processedReleaseKeys  map[string]bool // key format: "repoID_name"
	processedCommitHashes map[string]bool
	processedLock         sync.RWMutex
	// Thêm các kênh và waitgroups để quản lý worker pools tốt hơn
	repoWorkers    chan struct{}
	releaseWorkers chan struct{}
	commitWorkers  chan struct{}
	errorChan      chan error
}

func NewCrawlerV2(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV2, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)

	// Giá trị tối ưu cho các worker pools
	maxRepoWorkers := 5     // Số lượng repo có thể được xử lý đồng thời
	maxReleaseWorkers := 10 // Số lượng release có thể được xử lý đồng thời cho mỗi repo
	maxCommitWorkers := 20  // Số lượng commit có thể được xử lý đồng thời cho mỗi release

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
	}, nil
}

func (c *CrawlerV2) Crawl() bool {
	ctx := context.Background()
	startTime := time.Now()
	c.Logger.Info(ctx, "Bắt đầu crawl dữ liệu repository GitHub với phương pháp concurrency %s", startTime.Format(time.RFC3339))

	// Khởi tạo context có thể hủy bỏ để kiểm soát toàn bộ quá trình crawl
	crawlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Khởi tạo goroutine theo dõi lỗi
	go c.errorMonitor(crawlCtx)

	// Connect to database
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Không thể kết nối đến cơ sở dữ liệu: %v", err)
		return false
	}

	// Các biến theo dõi tiến trình
	var (
		page          = 1
		totalRepos    = 0
		skippedRepos  = 0
		totalReleases = 0
		totalCommits  = 0
		maxRepos      = 5000
		perPage       = 100
		apiCaller     = githubapi.NewCaller(c.Logger, c.Config, page, perPage)
		// Các biến kiểm soát GitHub Search API limits
		maxApiResults     = 1000
		emptyResultsCount = 0
		// Mutex để bảo vệ các biến counter
		counterLock sync.Mutex
		// WaitGroup cho toàn bộ quá trình
		mainWg sync.WaitGroup
	)

	// Channel để thông báo khi đã lấy đủ số lượng repos cần thiết
	done := make(chan bool)

	// Goroutine theo dõi số lượng repos đã xử lý
	go func() {
		defer close(done)
		for {
			select {
			case <-crawlCtx.Done():
				return
			default:
				counterLock.Lock()
				repoCount := totalRepos
				counterLock.Unlock()

				if repoCount >= maxRepos {
					done <- true
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	// Vòng lặp chính để lấy danh sách repos
	for totalRepos < maxRepos {
		if page > maxApiResults/perPage {
			c.Logger.Info(ctx, "Đã đạt giới hạn tìm kiếm của GitHub API (1000 kết quả)")
			break
		}

		// Áp dụng rate limiting
		c.applyRateLimit()

		// Cập nhật trang hiện tại
		apiCaller.Page = page
		apiCaller.PerPage = perPage

		// Gọi GitHub API
		repos, err := apiCaller.Call()
		if err != nil {
			if c.isRateLimitError(err) {
				c.Logger.Info(ctx, "Đạt giới hạn tốc độ gọi API, đợi 60 giây")
				time.Sleep(60 * time.Second)
				continue
			}
			c.Logger.Error(ctx, "Không thể gọi GitHub API: %v", err)
			return false
		}

		// Kiểm tra kết quả trống
		if len(repos) == 0 {
			emptyResultsCount++
			if emptyResultsCount >= 2 {
				c.Logger.Info(ctx, "Nhận được kết quả trống 2 lần liên tiếp, kết thúc thu thập")
				break
			}
			page++
			continue
		}
		emptyResultsCount = 0

		// Xử lý song song các repository
		for _, repo := range repos {
			// Kiểm tra đã đạt số lượng tối đa chưa
			counterLock.Lock()
			if totalRepos >= maxRepos {
				counterLock.Unlock()
				break
			}
			counterLock.Unlock()

			// Sử dụng semaphore để giới hạn số goroutines
			select {
			case <-done:
			case c.repoWorkers <- struct{}{}: // Lấy một slot trong worker pool
				mainWg.Add(1)
				go func(repo githubapi.GithubAPIResponse) {
					defer mainWg.Done()
					defer func() { <-c.repoWorkers }() // Trả lại slot

					// Tạo transaction riêng cho mỗi goroutine
					tx := db.Begin()
					if tx.Error != nil {
						c.errorChan <- tx.Error
						return
					}

					defer func() {
						if r := recover(); r != nil {
							tx.Rollback()
							c.errorChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý repo: %v", r)
						}
					}()

					// Xử lý repo
					repoModel, isSkipped, err := c.crawlRepo(crawlCtx, tx, repo)
					if err != nil {
						tx.Rollback()
						c.errorChan <- err
						return
					}

					if isSkipped {
						counterLock.Lock()
						skippedRepos++
						counterLock.Unlock()
						tx.Rollback() // Không cần commit nếu bỏ qua repo
						return
					}

					// Tạo user và repoName
					user := repo.Owner.Login
					repoName := repo.Name
					if user == "" {
						user, repoName = extractUserAndRepo(repo.FullName)
						if user == "" {
							user = "unknown"
						}
					}

					// Thu thập releases cho repository này
					_, relCount, comCount, err := c.crawlReleases(crawlCtx, tx, githubapi.NewCaller(c.Logger, c.Config, 1, 100), user, repoName, repoModel.ID)
					if err != nil {
						c.Logger.Warn(crawlCtx, "Lỗi khi crawl releases cho %s/%s: %v", user, repoName, err)
						// Vẫn commit repo đã tạo ngay cả khi có lỗi khi crawl releases
					}

					// Commit transaction
					if err := tx.Commit().Error; err != nil {
						c.errorChan <- err
						return
					}

					counterLock.Lock()
					totalRepos++
					totalReleases += relCount
					totalCommits += comCount
					counterLock.Unlock()

				}(repo)
			}
		}

		page++

		// Kiểm tra xem có nên tiếp tục không
		select {
		case <-done:
		default:
		}
	}

	// Đợi tất cả goroutines hoàn thành
	mainWg.Wait()

	// Đóng các channels
	close(c.errorChan)

	// Log kết quả
	c.logCrawlResults(ctx, startTime, totalRepos, totalReleases, totalCommits, skippedRepos)

	return true
}

// Theo dõi và xử lý lỗi từ các goroutines
func (c *CrawlerV2) errorMonitor(ctx context.Context) {
	for {
		select {
		case err, ok := <-c.errorChan:
			if !ok {
				return // channel đã đóng
			}
			if err != nil {
				c.Logger.Error(ctx, "Lỗi trong worker: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

// Check if a repository has been processed
func (c *CrawlerV2) isProcessed(repoID int64) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedRepoIDs[repoID]
}

// Add a processed repository ID to the tracking map
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

// Add a processed release to the tracking map
func (c *CrawlerV2) addProcessedRelease(repoID int, releaseName string) {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedReleaseKeys[key] = true
}

// Check if a commit has been processed
func (c *CrawlerV2) isCommitProcessed(commitHash string) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedCommitHashes[commitHash]
}

// Add a processed commit to the tracking map
func (c *CrawlerV2) addProcessedCommit(commitHash string) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedCommitHashes[commitHash] = true
}

// Rate limiting với backoff strategy
func (c *CrawlerV2) applyRateLimit() {
	attempts := 0
	maxAttempts := 5
	baseDelay := time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond

	for !c.rateLimiter.Allow() {
		attempts++
		if attempts > maxAttempts {
			// Nghỉ thời gian cố định sau nhiều lần thử
			time.Sleep(5 * time.Second)
			attempts = 0
		} else {
			// Exponential backoff
			delay := baseDelay * time.Duration(attempts)
			time.Sleep(delay)
		}
	}
}

// Phát hiện lỗi rate limit từ GitHub API
func (c *CrawlerV2) isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "403") ||
		strings.Contains(err.Error(), "rate limit") ||
		strings.Contains(err.Error(), "API rate limit exceeded")
}

// Crawl thông tin repository
func (c *CrawlerV2) crawlRepo(ctx context.Context, tx *gorm.DB, repo githubapi.GithubAPIResponse) (*model.Repo, bool, error) {
	// Extract username and repo name
	user := repo.Owner.Login
	repoName := repo.Name

	if user == "" {
		user, repoName = extractUserAndRepo(repo.FullName)
		if user == "" {
			user = "unknown"
		}
	}

	if c.isProcessed(repo.Id) {
		return nil, true, nil
	}

	repoModel := &model.Repo{
		ID:         int(repo.Id),
		User:       model.TruncateString(user, 250),
		Name:       model.TruncateString(repoName, 250),
		StarCount:  int(repo.StargazersCount),
		ForkCount:  int(repo.ForksCount),
		WatchCount: int(repo.WatchersCount),
		IssueCount: int(repo.OpenIssuesCount),
		Model: model.Model{
			Config: c.Config,
			Logger: c.Logger,
			Mysql:  c.Mysql,
		},
	}

	if err := tx.Create(repoModel).Error; err != nil {
		return nil, false, err
	}

	c.addProcessedID(repo.Id)
	return repoModel, false, nil
}

// Crawl releases cho một repository
func (c *CrawlerV2) crawlReleases(ctx context.Context, tx *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, int, int, error) {
	c.applyRateLimit()

	// Call API to get releases
	releases, err := apiCaller.CallReleases(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Rate limit đạt ngưỡng khi crawl releases, đợi 60 giây")
			time.Sleep(60 * time.Second)
			releases, err = apiCaller.CallReleases(user, repoName)
			if err != nil {
				return nil, 0, 0, err
			}
		} else {
			return nil, 0, 0, err
		}
	}

	if len(releases) == 0 {
		return releases, 0, 0, nil
	}

	releasesCount := 0
	commitsCount := 0

	// Sử dụng WaitGroup để đảm bảo tất cả releases được xử lý
	var wg sync.WaitGroup
	var mu sync.Mutex // Mutex để bảo vệ các biến đếm

	// Context có thể hủy để quản lý các goroutines
	releaseCtx, cancelRelease := context.WithCancel(ctx)
	defer cancelRelease()

	// Xử lý releases song song với giới hạn đồng thời
	for _, release := range releases {
		// Bỏ qua release đã xử lý
		if c.isReleaseProcessed(repoID, release.Name) {
			continue
		}

		// Lấy một slot từ release worker pool
		select {
		case <-releaseCtx.Done():
		case c.releaseWorkers <- struct{}{}:
			wg.Add(1)
			go func(release githubapi.ReleaseResponse) {
				defer wg.Done()
				defer func() { <-c.releaseWorkers }() // Trả lại slot

				// Tạo transaction mới cho mỗi release
				releaseTx := tx.Begin()
				if releaseTx.Error != nil {
					c.errorChan <- releaseTx.Error
					return
				}

				defer func() {
					if r := recover(); r != nil {
						releaseTx.Rollback()
						c.errorChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý release: %v", r)
					}
				}()

				// Lưu release và các commit liên quan
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
						c.errorChan <- err
					}
					return
				}

				c.addProcessedRelease(repoID, release.Name)

				// Thu thập commits cho release này
				_, commitCount, err := c.crawlCommits(releaseCtx, releaseTx, apiCaller, user, repoName, releaseModel.ID)
				if err != nil {
					c.Logger.Warn(releaseCtx, "Lỗi khi crawl commits cho release %s: %v", release.Name, err)
					// Vẫn commit release đã tạo
				}

				// Commit transaction
				if err := releaseTx.Commit().Error; err != nil {
					c.errorChan <- err
					return
				}

				// Cập nhật số lượng đã xử lý
				mu.Lock()
				releasesCount++
				commitsCount += commitCount
				mu.Unlock()

			}(release)
		}
	}

	// Đợi tất cả releases được xử lý
	wg.Wait()

	return releases, releasesCount, commitsCount, nil
}

// Crawl commits cho một release
func (c *CrawlerV2) crawlCommits(ctx context.Context, tx *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, int, error) {
	c.applyRateLimit()

	// Gọi API để lấy commits
	commits, err := apiCaller.CallCommits(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.Logger.Info(ctx, "Rate limit đạt ngưỡng khi crawl commits, đợi 60 giây")
			time.Sleep(60 * time.Second)
			commits, err = apiCaller.CallCommits(user, repoName)
			if err != nil {
				return nil, 0, err
			}
		} else {
			return nil, 0, err
		}
	}

	if len(commits) == 0 {
		return commits, 0, nil
	}

	commitsCount := 0
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Context có thể hủy để quản lý các goroutines
	commitCtx, cancelCommit := context.WithCancel(ctx)
	defer cancelCommit()

	// Xử lý commits song song với giới hạn đồng thời
	for _, commit := range commits {
		// Bỏ qua commit đã xử lý
		if c.isCommitProcessed(commit.SHA) {
			continue
		}

		// Lấy một slot từ commit worker pool
		select {
		case <-commitCtx.Done():
		case c.commitWorkers <- struct{}{}:
			wg.Add(1)
			go func(commit githubapi.CommitResponse) {
				defer wg.Done()
				defer func() { <-c.commitWorkers }() // Trả lại slot

				defer func() {
					if r := recover(); r != nil {
						c.errorChan <- fmt.Errorf("panic xảy ra trong goroutine xử lý commit: %v", r)
					}
				}()

				// Lưu commit
				commitModel := &model.Commit{
					Hash:      model.TruncateString(commit.SHA, 250),
					Message:   model.TruncateString(commit.Commit.Message, 65000),
					ReleaseID: releaseID,
					Model: model.Model{
						Config: c.Config,
						Logger: c.Logger,
						Mysql:  c.Mysql,
					},
				}

				if err := tx.Create(commitModel).Error; err != nil {
					if !strings.Contains(err.Error(), "Duplicate entry") {
						c.errorChan <- err
						return
					}
				}

				c.addProcessedCommit(commit.SHA)

				mu.Lock()
				commitsCount++
				mu.Unlock()
			}(commit)
		}
	}

	// Đợi tất cả commits được xử lý
	wg.Wait()

	return commits, commitsCount, nil
}

// Ghi log kết quả crawl
func (c *CrawlerV2) logCrawlResults(ctx context.Context, startTime time.Time, totalRepos, totalReleases, totalCommits, skippedRepos int) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL V2 ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
	c.Logger.Info(ctx, "Tổng số repository đã crawl: %d", totalRepos)
	c.Logger.Info(ctx, "Tổng số releases đã crawl: %d", totalReleases)
	c.Logger.Info(ctx, "Tổng số commits đã crawl: %d", totalCommits)
	c.Logger.Info(ctx, "Tổng số repository bỏ qua (đã tồn tại): %d", skippedRepos)
}
