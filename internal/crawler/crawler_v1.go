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
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
	"gorm.io/gorm"
)

type CrawlerV1 struct {
	Logger    log.Logger
	Config    *cfg.Config
	Mysql     *db.Mysql
	RepoMd    *model.Repo
	ReleaseMd *model.Release
	CommitMd  *model.Commit
}

func NewCrawlerV1(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV1, error) {
	repoMd, err := model.NewRepo(config, logger, mysql)
	if err != nil {
		return nil, fmt.Errorf("failed to create repo model: %w", err)
	}

	releaseMd, err := model.NewRelease(config, logger, mysql)
	if err != nil {
		return nil, fmt.Errorf("failed to create release model: %w", err)
	}

	commitMd, err := model.NewCommit(config, logger, mysql)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit model: %w", err)
	}

	return &CrawlerV1{
		Logger:    logger,
		Config:    config,
		Mysql:     mysql,
		RepoMd:    repoMd,
		ReleaseMd: releaseMd,
		CommitMd:  commitMd,
	}, nil
}

func (c *CrawlerV1) Crawl() bool {
	ctx := context.Background()
	c.Logger.Info(ctx, "Bắt đầu quá trình crawler dữ liệu repository GitHub")

	// Khởi tạo kết nối database
	db, err := c.Mysql.Db()
	if err != nil {
		c.Logger.Error(ctx, "Không thể kết nối đến database: %v", err)
		return false
	}

	// Bắt đầu transaction
	tx := db.Begin()
	if tx.Error != nil {
		c.Logger.Error(ctx, "Không thể bắt đầu transaction: %v", tx.Error)
		return false
	}

	// Hoàn tác transaction trong trường hợp có lỗi
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			c.Logger.Error(ctx, "Phục hồi từ panic: %v", r)
		}
	}()

	page := 1
	totalRepos := 0
	skippedRepos := 0
	maxRepos := 5000
	perPage := 100 // Số lượng tối đa cho phép bởi GitHub API
	startTime := time.Now()

	// GitHub Search API có giới hạn 1000 kết quả cho mỗi truy vấn tìm kiếm
	maxApiResults := 1000

	// Theo dõi kết quả trống để phát hiện khi đã hết dữ liệu
	emptyResultsCount := 0

	for totalRepos < maxRepos {
		// Dừng nếu đã đạt đến giới hạn tìm kiếm của GitHub
		if page > maxApiResults/perPage {
			c.Logger.Info(ctx, "Đã đạt đến giới hạn API tìm kiếm của GitHub là %d kết quả", maxApiResults)
			break
		}

		// Gọi GitHub API để lấy thông tin repository
		caller := githubapi.NewCaller(c.Logger, c.Config, page, perPage)
		repos, err := caller.Call()
		if err != nil {
			// Xử lý giới hạn tốc độ - nếu bị giới hạn, đợi và thử lại
			if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
				c.Logger.Warn(ctx, "Bị giới hạn tốc độ, đợi 60 giây trước khi thử lại...")
				time.Sleep(60 * time.Second)
				continue
			}

			tx.Rollback()
			c.Logger.Error(ctx, "Không thể gọi GitHub API: %v", err)
			return false
		}

		// Dừng nếu không còn repository nào
		if len(repos) == 0 {
			emptyResultsCount++

			// Nếu nhận kết quả trống hai lần liên tiếp, có thể đã hoàn thành
			if emptyResultsCount >= 2 {
				c.Logger.Info(ctx, "Không còn repository nào để lấy sau nhiều kết quả trống")
				break
			}

			c.Logger.Info(ctx, "Không có repository nào được trả về cho trang %d, thử trang tiếp theo", page)
			page++
			continue
		}

		// Đặt lại bộ đếm kết quả trống vì đã nhận được dữ liệu
		emptyResultsCount = 0

		c.Logger.Info(ctx, "Xử lý trang %d với %d repository (tổng cộng đến nay: %d)",
			page, len(repos), totalRepos)

		// Xử lý từng repository
		for _, repo := range repos {
			if totalRepos >= maxRepos {
				break
			}

			// Lấy tên đăng nhập của chủ sở hữu
			user := repo.Owner.Login
			if user == "" {
				user, _ = extractUserAndRepo(repo.FullName)
				if user == "" {
					user = "unknown"
				}
			}

			// Kiểm tra xem repository đã tồn tại chưa
			var existingRepo model.Repo
			result := tx.Where("id = ?", repo.Id).First(&existingRepo)

			if result.Error == nil {
				// Repository đã tồn tại
				skippedRepos++
				continue
			} else if result.Error != gorm.ErrRecordNotFound {
				// Lỗi không mong đợi
				tx.Rollback()
				c.Logger.Error(ctx, "Lỗi khi kiểm tra repository đã tồn tại: %v", result.Error)
				return false
			}

			// Lưu repository
			repoModel := &model.Repo{
				ID:         int(repo.Id),
				User:       user,
				Name:       repo.Name,
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
				c.Logger.Error(ctx, "Không thể lưu repository: %v", err)
				return false
			}

			// Tạo một phiên bản release cho repository
			releaseModel := &model.Release{
				Content: fmt.Sprintf("Release cho repository %s", repo.Name),
				RepoID:  int(repo.Id),
				Model: model.Model{
					Config: c.Config,
					Logger: c.Logger,
					Mysql:  c.Mysql,
				},
			}

			if err := tx.Create(releaseModel).Error; err != nil {
				tx.Rollback()
				c.Logger.Error(ctx, "Không thể lưu release: %v", err)
				return false
			}

			// Tạo một commit cho release
			commitModel := &model.Commit{
				Hash:      fmt.Sprintf("commit-%d", repo.Id),
				Message:   fmt.Sprintf("Commit đầu tiên cho repository %s", repo.Name),
				ReleaseID: int(releaseModel.ID),
				Model: model.Model{
					Config: c.Config,
					Logger: c.Logger,
					Mysql:  c.Mysql,
				},
			}

			if err := tx.Create(commitModel).Error; err != nil {
				tx.Rollback()
				c.Logger.Error(ctx, "Không thể lưu commit: %v", err)
				return false
			}

			totalRepos++
			// Chỉ ghi log thông tin thiết yếu
			c.Logger.Info(ctx, "Đã crawl [%d/%d]: %s/%s (Stars: %d, Forks: %d, Watchers: %d, Issues: %d)",
				totalRepos, maxRepos, user, repo.Name, repo.StargazersCount, repo.ForksCount,
				repo.WatchersCount, repo.OpenIssuesCount)
		}

		page++

		// Commit sớm mỗi 5 trang để tránh transaction chạy quá lâu
		if page%5 == 0 {
			if err := tx.Commit().Error; err != nil {
				c.Logger.Error(ctx, "Không thể commit transaction: %v", err)
				return false
			}

			// Bắt đầu transaction mới
			tx = db.Begin()
			if tx.Error != nil {
				c.Logger.Error(ctx, "Không thể bắt đầu transaction mới: %v", tx.Error)
				return false
			}

			// Thêm độ trễ nhỏ để tránh đạt đến giới hạn tốc độ API
			// Sử dụng độ trễ dài hơn cho các yêu cầu đã xác thực
			if c.Config.GithubApi.AccessToken != "" {
				time.Sleep(2 * time.Second)
			} else {
				// Sử dụng độ trễ dài hơn cho các yêu cầu chưa xác thực
				time.Sleep(5 * time.Second)
			}
		}
	}

	// Commit transaction cuối cùng
	if err := tx.Commit().Error; err != nil {
		c.Logger.Error(ctx, "Không thể commit transaction: %v", err)
		return false
	}

	duration := time.Since(startTime)

	// Cung cấp thông báo hoàn thành chi tiết hơn
	if totalRepos < 1000 {
		c.Logger.Info(ctx, "Hoàn tất crawl: %d repository đã lưu, %d đã bỏ qua, mất %v",
			totalRepos, skippedRepos, duration)
		c.Logger.Info(ctx, "LƯU Ý: GitHub Search API giới hạn kết quả tối đa 1000 mục cho mỗi truy vấn tìm kiếm.")
	} else {
		c.Logger.Info(ctx, "Hoàn tất crawl: %d repository đã lưu, %d đã bỏ qua, mất %v",
			totalRepos, skippedRepos, duration)
	}

	return true
}

// extractUserAndRepo trích xuất tên người dùng và tên repository từ tên đầy đủ
// Mong đợi định dạng như "user/repo" hoặc chỉ "repo"
func extractUserAndRepo(fullName string) (string, string) {
	parts := strings.Split(fullName, "/")
	if len(parts) > 1 {
		return parts[0], parts[1]
	}
	return "unknown", fullName
}
