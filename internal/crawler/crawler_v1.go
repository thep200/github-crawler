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
	"gorm.io/gorm"
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
				time.Sleep(5 * time.Second)
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

func (c *CrawlerV1) crawlRepo(db *gorm.DB, repo githubapi.GithubAPIResponse) (*model.Repo, bool, error) {
	// Extract username and repo name
	user := repo.Owner.Login
	repoName := repo.Name

	if user == "" {
		user, repoName = extractUserAndRepo(repo.FullName)
		if user == "" {
			user = "unknown"
		}
	}

	//
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

	// Start a transaction for this repository
	tx := db.Begin()
	if tx.Error != nil {
		return nil, false, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Create(repoModel).Error; err != nil {
		tx.Rollback()
		return nil, false, err
	}

	// Commit immediately after creating the repository
	if err := tx.Commit().Error; err != nil {
		return nil, false, err
	}

	//
	c.addProcessedID(repo.Id)

	return repoModel, false, nil
}

func (c *CrawlerV1) crawlReleases(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, int, int, error) {
	c.applyRateLimit()

	// Call API to get releases
	releases, err := apiCaller.CallReleases(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			time.Sleep(5 * time.Second)
			releases, err = apiCaller.CallReleases(user, repoName)
			if err != nil {
				return nil, 0, 0, err
			}
		} else {
			return nil, 0, 0, err
		}
	}

	releasesCount := 0
	commitsCount := 0

	// Process each release
	for _, release := range releases {
		releaseModel, err := c.crawlRelease(db, release, user, repoName, repoID)
		if err != nil {
			continue
		}

		if releaseModel != nil {
			releasesCount++

			// Process commits for this release
			_, count, err := c.crawlCommits(db, apiCaller, user, repoName, releaseModel.ID)
			if err != nil {
				continue
			}

			commitsCount += count
		}
	}

	return releases, releasesCount, commitsCount, nil
}

func (c *CrawlerV1) crawlRelease(db *gorm.DB, release githubapi.ReleaseResponse, user, repoName string, repoID int) (*model.Release, error) {
	releaseContent := release.Body
	releaseName := release.Name
	if releaseName == "" {
		releaseName = release.TagName
	}

	// Skip if this release has already been processed
	if c.isReleaseProcessed(repoID, releaseName) {
		return nil, nil
	}

	releaseModel := &model.Release{
		Content: model.TruncateString(releaseContent, 65000),
		RepoID:  repoID,
		Model: model.Model{
			Config: c.Config,
			Logger: c.Logger,
			Mysql:  c.Mysql,
		},
	}

	// Start a transaction for this release
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Create(releaseModel).Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	// Commit immediately after creating the release
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	// Mark release as processed
	c.addProcessedRelease(repoID, releaseName)

	return releaseModel, nil
}

func (c *CrawlerV1) crawlCommits(db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, int, error) {
	c.applyRateLimit()

	// Call API to get commits
	commits, err := apiCaller.CallCommits(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			time.Sleep(60 * time.Second)
			commits, err = apiCaller.CallCommits(user, repoName)
			if err != nil {
				return nil, 0, err
			}
		} else {
			return nil, 0, err
		}
	}

	commitsCount := 0

	// Process each commit
	for _, commit := range commits {
		if err := c.saveCommit(db, commit, releaseID); err != nil {
			if !strings.Contains(err.Error(), "Duplicate entry") {
				return commits, commitsCount, err
			}
			continue
		}

		commitsCount++
	}

	return commits, commitsCount, nil
}

func (c *CrawlerV1) saveCommit(db *gorm.DB, commit githubapi.CommitResponse, releaseID int) error {
	hashValue := model.TruncateString(commit.SHA, 250)

	//
	if c.isCommitProcessed(hashValue) {
		return nil
	}

	messageValue := model.TruncateString(commit.Commit.Message, 65000)

	commitModel := &model.Commit{
		Hash:      hashValue,
		Message:   messageValue,
		ReleaseID: releaseID,
		Model: model.Model{
			Config: c.Config,
			Logger: c.Logger,
			Mysql:  c.Mysql,
		},
	}

	// Start a transaction for this commit
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	err := tx.Create(commitModel).Error
	if err != nil {
		tx.Rollback()
		if !strings.Contains(err.Error(), "Duplicate entry") {
			return err
		}

		//
		c.addProcessedCommit(hashValue)
		return nil
	}

	// Commit immediately after creating the commit
	if err := tx.Commit().Error; err != nil {
		return err
	}

	//
	c.addProcessedCommit(hashValue)
	return nil
}

func (c *CrawlerV1) applyRateLimit() {
	for !c.rateLimiter.Allow() {
		time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
	}
}

func (c *CrawlerV1) isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit")
}

func (c *CrawlerV1) logCrawlResults(ctx context.Context, startTime time.Time, totalRepos, totalReleases, totalCommits, skippedRepos int) {
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL ====")
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
