// Crawler version 1
// Crawler không có bất kỳ cấu hình đặc biệt nào, chỉ sử dụng API mặc định từ GitHub

package crawler

import (
	"context"
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
	Logger        log.Logger
	Config        *cfg.Config
	Mysql         *db.Mysql
	RepoMd        *model.Repo
	ReleaseMd     *model.Release
	CommitMd      *model.Commit
	rateLimiter   *limiter.RateLimiter
	processedIDs  []int64
	processedLock sync.RWMutex
}

func NewCrawlerV1(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV1, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)
	return &CrawlerV1{
		Logger:        logger,
		Config:        config,
		Mysql:         mysql,
		RepoMd:        repoMd,
		ReleaseMd:     releaseMd,
		CommitMd:      commitMd,
		rateLimiter:   rateLimiter,
		processedIDs:  make([]int64, 0, 5000),
		processedLock: sync.RWMutex{},
	}, nil
}

//
func (c *CrawlerV1) isProcessed(repoID int64) bool {
	c.processedLock.RLock()
	defer c.processedLock.RUnlock()

	for _, id := range c.processedIDs {
		if id == repoID {
			return true
		}
	}
	return false
}

//
func (c *CrawlerV1) addProcessedID(repoID int64) {
	c.processedLock.Lock()
	defer c.processedLock.Unlock()
	c.processedIDs = append(c.processedIDs, repoID)
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

	//
	tx := db.Begin()
	if tx.Error != nil {
		return false
	}

	//
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

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
			tx.Rollback()
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
			repoModel, isSkipped, err := c.crawlRepo(tx, repo)
			if err != nil {
				tx.Rollback()
				return false
			}
			if isSkipped {
				skippedRepos++
				continue
			}
			totalRepos++
			if totalRepos > 0 && totalRepos%10 == 0 {
				if err := tx.Commit().Error; err != nil {
					return false
				}
				tx = db.Begin()
				if tx.Error != nil {
					return false
				}
			}

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
			_, releasesCount, commitsCount, err := c.crawlReleases(ctx, tx, apiCaller, user, repoName, repoModel.ID)
			if err != nil {
				continue
			}

			totalReleases += releasesCount
			totalCommits += commitsCount
		}

		page++

		//
		if err := tx.Commit().Error; err != nil {
			return false
		}

		//
		tx = db.Begin()
		if tx.Error != nil {
			return false
		}
	}

	// Final commit
	if tx != nil {
		if err := tx.Commit().Error; err != nil {
			return false
		}
	}

	// Log results
	c.logCrawlResults(ctx, startTime, totalRepos, totalReleases, totalCommits, skippedRepos)

	return true
}

func (c *CrawlerV1) crawlRepo(tx *gorm.DB, repo githubapi.GithubAPIResponse) (*model.Repo, bool, error) {
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

	if err := tx.Create(repoModel).Error; err != nil {
		return nil, false, err
	}

	// Add newly processed ID to our in-memory list
	c.addProcessedID(repo.Id)

	return repoModel, false, nil
}

func (c *CrawlerV1) crawlReleases(ctx context.Context, tx *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, int, int, error) {
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
		releaseModel, err := c.crawlRelease(tx, release, user, repoName, repoID)
		if err != nil {
			continue
		}

		releasesCount++

		// Process commits for this release
		_, count, err := c.crawlCommits(tx, apiCaller, user, repoName, releaseModel.ID)
		if err != nil {
			continue
		}

		commitsCount += count
	}

	return releases, releasesCount, commitsCount, nil
}

func (c *CrawlerV1) crawlRelease(tx *gorm.DB, release githubapi.ReleaseResponse, user, repoName string, repoID int) (*model.Release, error) {
	releaseContent := release.Body
	releaseModel := &model.Release{
		Content: model.TruncateString(releaseContent, 65000),
		RepoID:  repoID,
		Model: model.Model{
			Config: c.Config,
			Logger: c.Logger,
			Mysql:  c.Mysql,
		},
	}

	if err := tx.Create(releaseModel).Error; err != nil {
		return nil, err
	}

	return releaseModel, nil
}

func (c *CrawlerV1) crawlCommits(tx *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, releaseID int) ([]githubapi.CommitResponse, int, error) {
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
		if err := c.saveCommit(tx, commit, releaseID); err != nil {
			if !strings.Contains(err.Error(), "Duplicate entry") {
				return commits, commitsCount, err
			}
			continue
		}

		commitsCount++
	}

	return commits, commitsCount, nil
}

func (c *CrawlerV1) saveCommit(tx *gorm.DB, commit githubapi.CommitResponse, releaseID int) error {
	hashValue := model.TruncateString(commit.SHA, 250)
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

	return tx.Create(commitModel).Error
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
