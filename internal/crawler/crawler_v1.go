// Crawler version 1
// Crawler không có bất kỳ cấu hình đặc biệt nào, chỉ sử dụng API mặc định từ GitHub

package crawler

import (
	"context"
	"fmt"
	"strings"
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
	Logger      log.Logger
	Config      *cfg.Config
	Mysql       *db.Mysql
	RepoMd      *model.Repo
	ReleaseMd   *model.Release
	CommitMd    *model.Commit
	rateLimiter *limiter.RateLimiter
}

func NewCrawlerV1(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV1, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	commitMd, _ := model.NewCommit(config, logger, mysql)
	rateLimiter := limiter.NewRateLimiter(config.GithubApi.RequestsPerSecond)
	return &CrawlerV1{
		Logger:      logger,
		Config:      config,
		Mysql:       mysql,
		RepoMd:      repoMd,
		ReleaseMd:   releaseMd,
		CommitMd:    commitMd,
		rateLimiter: rateLimiter,
	}, nil
}

func (c *CrawlerV1) Crawl() bool {
	ctx := context.Background()
	startTime := time.Now()
	c.Logger.Info(ctx, "Start crawl data repository GitHub %s", startTime.Format(time.RFC3339))

	//
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

	// GitHub Search API có limit 1000 kết quả cho mỗi truy vấn tìm kiếm
	maxApiResults := 1000

	// Theo dõi kết quả null để biết khi nào đạt tới giới hạn
	emptyResultsCount := 0

	for totalRepos < maxRepos {
		// Dừng nếu đã đạt tới giới hạn
		if page > maxApiResults/perPage {
			break
		}

		// Rate limiter
		for !c.rateLimiter.Allow() {
			time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
		}

		// Call GitHub API
		apiCaller.Page = page
		apiCaller.PerPage = perPage
		repos, err := apiCaller.Call()
		if err != nil {
			// Nếu bị dính rate limit thì đợi rồi gọi lại
			if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
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

		// Data repository
		for _, repo := range repos {
			if totalRepos >= maxRepos {
				break
			}

			// Username data and repo Name
			user := repo.Owner.Login
			repoName := repo.Name

			if user == "" {
				user, repoName = extractUserAndRepo(repo.FullName)
				if user == "" {
					user = "unknown"
				}
			}

			// Check duplicate repository
			var existingRepo model.Repo
			result := tx.Where("id = ?", repo.Id).First(&existingRepo)

			if result.Error == nil {
				skippedRepos++
				continue
			} else if result.Error != gorm.ErrRecordNotFound {
				tx.Rollback()
				return false
			}

			// Save repository
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
				tx.Rollback()
				return false
			}

			//
			if totalRepos > 0 && totalRepos%10 == 0 {
				if err := tx.Commit().Error; err != nil {
					return false
				}
				tx = db.Begin()
				if tx.Error != nil {
					return false
				}
			}

			// Call API để lấy data releases
			for !c.rateLimiter.Allow() {
				time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
			}

			releases, err := apiCaller.CallReleases(user, repoName)
			if err != nil {
				if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
					time.Sleep(5 * time.Second)
					releases, err = apiCaller.CallReleases(user, repoName)
					if err != nil {
						continue
					}
				} else {
					continue
				}
			}

			// Release data
			for _, release := range releases {
				releaseContent := release.Body
				if releaseContent == "" {
					releaseContent = fmt.Sprintf("Release %s for %s/%s", release.TagName, user, repoName)
				}

				releaseModel := &model.Release{
					Content: model.TruncateString(releaseContent, 65000),
					RepoID:  int(repo.Id),
					Model: model.Model{
						Config: c.Config,
						Logger: c.Logger,
						Mysql:  c.Mysql,
					},
				}

				if err := tx.Create(releaseModel).Error; err != nil {
					tx.Rollback()
					return false
				}

				totalReleases++

				// Call API để lấy data commit commits
				for !c.rateLimiter.Allow() {
					time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
				}

				commits, err := apiCaller.CallCommits(user, repoName)
				if err != nil {
					if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
						time.Sleep(60 * time.Second)
						commits, err = apiCaller.CallCommits(user, repoName)
						if err != nil {
							continue
						}
					} else {
						continue
					}
				}

				// Commit data
				for _, commit := range commits {
					hashValue := model.TruncateString(commit.SHA, 250)
					messageValue := model.TruncateString(commit.Commit.Message, 65000)

					//
					commitModel := &model.Commit{
						Hash:      hashValue,
						Message:   messageValue,
						ReleaseID: int(releaseModel.ID),
						Model: model.Model{
							Config: c.Config,
							Logger: c.Logger,
							Mysql:  c.Mysql,
						},
					}

					if err := tx.Create(commitModel).Error; err != nil {
						if strings.Contains(err.Error(), "Duplicate entry") {
							continue
						}
						tx.Rollback()
						return false
					}

					totalCommits++
				}
			}

			totalRepos++
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

	//
	if tx != nil {
		if err := tx.Commit().Error; err != nil {
			return false
		}
	}

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	//
	c.Logger.Info(ctx, "==== KẾT QUẢ CRAWL ====")
	c.Logger.Info(ctx, "Thời gian bắt đầu: %s", startTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Thời gian kết thúc: %s", endTime.Format(time.RFC3339))
	c.Logger.Info(ctx, "Tổng thời gian thực hiện: %v", duration)
	c.Logger.Info(ctx, "Tổng số repository đã crawl: %d", totalRepos)
	c.Logger.Info(ctx, "Tổng số releases đã crawl: %d", totalReleases)
	c.Logger.Info(ctx, "Tổng số commits đã crawl: %d", totalCommits)
	c.Logger.Info(ctx, "Tổng số repository bỏ qua (đã tồn tại): %d", skippedRepos)

	return true
}

// Lấy username và tên repository từ tên đầy đủ
func extractUserAndRepo(fullName string) (string, string) {
	parts := strings.Split(fullName, "/")
	if len(parts) > 1 {
		return parts[0], parts[1]
	}
	return "unknown", fullName
}
