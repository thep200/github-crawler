// Crawler version 1
// Crawler không có bất kỳ cấu hình đặc biệt nào, chỉ sử dụng API mặc định từ GitHub

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
)

type CrawlerV1 struct {
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
}

func NewCrawlerV1(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV1, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)
	return &CrawlerV1{
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
	}, nil
}

// Check if a repository has been processed
func (c *CrawlerV1) isProcessed(repoID int64) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedRepoIDs[repoID]
}

// Add a processed repository ID to the tracking map
func (c *CrawlerV1) addProcessedID(repoID int64) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedRepoIDs[repoID] = true
}

// Check if a release has been processed
func (c *CrawlerV1) isReleaseProcessed(repoID int, releaseName string) bool {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedReleaseKeys[key]
}

// Add a processed release to the tracking map
func (c *CrawlerV1) addProcessedRelease(repoID int, releaseName string) {
	key := fmt.Sprintf("%d_%s", repoID, releaseName)
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedReleaseKeys[key] = true
}

// Check if a commit has been processed
func (c *CrawlerV1) isCommitProcessed(commitHash string) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()
	return c.processedCommitHashes[commitHash]
}

// Add a processed commit to the tracking map
func (c *CrawlerV1) addProcessedCommit(commitHash string) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedCommitHashes[commitHash] = true
}

func (c *CrawlerV1) Crawl() bool {
	ctx := context.Background()
	startTime := time.Now()
	c.Logger.Info(ctx, "Start crawl data repository GitHub %s", startTime.Format(time.RFC3339))

	// Connect to database
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Cannot connect to database: %v", err)
		return false
	}

	page := 1
	totalRepos := 0
	skippedRepos := 0
	totalReleases := 0
	totalCommits := 0
	maxRepos := 5000
	perPage := 100
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, page, perPage)

	// GitHub Search API limit
	maxApiResults := 1000
	emptyResultsCount := 0

	for totalRepos < maxRepos {
		if page > maxApiResults/perPage {
			break
		}
		c.applyRateLimit()

		//
		apiCaller.Page = page
		apiCaller.PerPage = perPage
		repos, err := apiCaller.Call()
		if err != nil {
			if c.isRateLimitError(err) {
				c.handleRateLimit(ctx, err)
				continue
			}
			c.Logger.Error(ctx, "Cannot call GitHub API: %v", err)
			return false
		}

		//
		if len(repos) == 0 {
			emptyResultsCount++
			if emptyResultsCount >= 2 {
				break
			}
			page++
			continue
		}
		emptyResultsCount = 0

		// Process each repository
		for _, repo := range repos {
			if totalRepos >= maxRepos {
				break
			}
			repoModel, isSkipped, err := c.crawlRepo(db, repo)
			if err != nil {
				c.Logger.Error(ctx, "Error crawling repo: %v", err)
				continue
			}
			if isSkipped {
				skippedRepos++
				continue
			}
			totalRepos++

			//
			user := repo.Owner.Login
			repoName := repo.Name
			if user == "" {
				user, repoName = extractUserAndRepo(repo.FullName)
				if user == "" {
					user = "unknown"
				}
			}

			//
			_, releasesCount, commitsCount, err := c.crawlReleases(ctx, db, apiCaller, user, repoName, repoModel.ID)
			if err != nil {
				continue
			}

			totalReleases += releasesCount
			totalCommits += commitsCount
		}

		page++
	}

	// Log results
	c.logCrawlResults(ctx, startTime, totalRepos, totalReleases, totalCommits, skippedRepos)

	return true
}

func (c *CrawlerV1) applyRateLimit() {
	for !c.rateLimiter.Allow() {
		time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
	}
}

func (c *CrawlerV1) isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "403") ||
		strings.Contains(err.Error(), "rate limit") ||
		strings.Contains(err.Error(), "đạt giới hạn API")
}

func (c *CrawlerV1) handleRateLimit(ctx context.Context, err error) {
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

func (c *CrawlerV1) logCrawlResults(ctx context.Context, startTime time.Time, totalRepos, totalReleases, totalCommits, skippedRepos int) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL V1 ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
	c.Logger.Info(ctx, "Tổng số repository đã crawl: %d", totalRepos)
	c.Logger.Info(ctx, "Tổng số releases đã crawl: %d", totalReleases)
	c.Logger.Info(ctx, "Tổng số commits đã crawl: %d", totalCommits)
	c.Logger.Info(ctx, "Tổng số repository bỏ qua (đã tồn tại): %d", skippedRepos)
}

// Lấy username và tên repository từ tên đầy đủ
func extractUserAndRepo(fullName string) (string, string) {
	parts := strings.Split(fullName, "/")
	if len(parts) > 1 {
		return parts[0], parts[1]
	}
	return "unknown", fullName
}
