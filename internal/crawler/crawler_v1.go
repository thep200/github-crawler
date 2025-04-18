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
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
	"gorm.io/gorm"
)

// RateLimiter là một cấu trúc đơn giản để giới hạn số lượng request một giây
type RateLimiter struct {
	requestTimes []time.Time
	maxRequests  int
	mu           sync.Mutex
}

// NewRateLimiter tạo một bộ giới hạn tốc độ mới với số lượng request tối đa cho phép mỗi giây
func NewRateLimiter(maxRequests int) *RateLimiter {
	return &RateLimiter{
		requestTimes: make([]time.Time, 0, maxRequests),
		maxRequests:  maxRequests,
	}
}

// Allow kiểm tra xem có thể thực hiện request mới hay không
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	oneSecondAgo := now.Add(-1 * time.Second)

	// Loại bỏ các request cũ hơn 1 giây
	validTimes := make([]time.Time, 0, len(r.requestTimes))
	for _, t := range r.requestTimes {
		if t.After(oneSecondAgo) {
			validTimes = append(validTimes, t)
		}
	}
	r.requestTimes = validTimes

	// Nếu số lượng request trong 1 giây vừa qua nhỏ hơn giới hạn,
	// thêm request mới và cho phép thực hiện
	if len(r.requestTimes) < r.maxRequests {
		r.requestTimes = append(r.requestTimes, now)
		return true
	}

	// Đã đạt giới hạn
	return false
}

type CrawlerV1 struct {
	Logger      log.Logger
	Config      *cfg.Config
	Mysql       *db.Mysql
	RepoMd      *model.Repo
	ReleaseMd   *model.Release
	CommitMd    *model.Commit
	rateLimiter *RateLimiter
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

	// Tạo rate limiter từ cấu hình
	rateLimiter := NewRateLimiter(config.GithubApi.RequestsPerSecond)

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
	c.Logger.Info(ctx, "Bắt đầu quá trình crawler dữ liệu repository GitHub vào %s", startTime.Format(time.RFC3339))

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
	totalReleases := 0
	totalCommits := 0
	maxRepos := 5000 // Tăng giới hạn lên 5000 repos
	perPage := 100   // Tăng số lượng repo mỗi trang lên 100 để tăng tốc crawler
	apiCaller := githubapi.NewCaller(c.Logger, c.Config, page, perPage)

	// GitHub Search API có giới hạn 1000 kết quả cho mỗi truy vấn tìm kiếm
	maxApiResults := 1000

	// Theo dõi kết quả trống để phát hiện khi đã hết dữ liệu
	emptyResultsCount := 0

	for totalRepos < maxRepos {
		// Dừng nếu đã đạt đến giới hạn tìm kiếm của GitHub
		if page > maxApiResults/perPage {
			break
		}

		// Kiểm tra rate limiter
		for !c.rateLimiter.Allow() {
			// Đợi một khoảng thời gian được cấu hình trước khi thử lại
			time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
		}

		// Gọi GitHub API để lấy thông tin repository
		apiCaller.Page = page
		apiCaller.PerPage = perPage
		repos, err := apiCaller.Call()
		if err != nil {
			// Xử lý giới hạn tốc độ - nếu bị giới hạn, đợi và thử lại
			if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
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
				break
			}

			page++
			continue
		}

		// Đặt lại bộ đếm kết quả trống vì đã nhận được dữ liệu
		emptyResultsCount = 0

		// Xử lý từng repository
		for _, repo := range repos {
			if totalRepos >= maxRepos {
				break
			}

			// Lấy tên đăng nhập của chủ sở hữu và tên repo
			user := repo.Owner.Login
			repoName := repo.Name

			if user == "" {
				user, repoName = extractUserAndRepo(repo.FullName)
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
				c.Logger.Error(ctx, "Không thể lưu repository: %v", err)
				return false
			}

			// Giảm độ thường xuyên của commit để tránh transaction quá lớn
			if totalRepos > 0 && totalRepos%10 == 0 {
				if err := tx.Commit().Error; err != nil {
					c.Logger.Error(ctx, "Không thể commit transaction: %v", err)
					return false
				}
				tx = db.Begin()
				if tx.Error != nil {
					c.Logger.Error(ctx, "Không thể bắt đầu transaction mới: %v", tx.Error)
					return false
				}
			}

			// Gọi API để lấy thông tin releases
			for !c.rateLimiter.Allow() {
				time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
			}

			releases, err := apiCaller.CallReleases(user, repoName)
			if err != nil {
				if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
					time.Sleep(60 * time.Second)
					releases, err = apiCaller.CallReleases(user, repoName)
					if err != nil {
						c.Logger.Error(ctx, "Không thể lấy dữ liệu releases sau khi đợi: %v", err)
						// Tiếp tục với repository tiếp theo thay vì thất bại hoàn toàn
						continue
					}
				} else if strings.Contains(err.Error(), "404") {
					// Repository không có releases, tạo một release mặc định
					releases = []githubapi.ReleaseResponse{
						{
							ID:        0,
							Name:      "Default Release",
							TagName:   "v0.0.0",
							Body:      "Auto-generated default release",
							CreatedAt: time.Now(),
						},
					}
				} else {
					c.Logger.Error(ctx, "Không thể lấy dữ liệu releases: %v", err)
					// Tiếp tục với repository tiếp theo thay vì thất bại hoàn toàn
					continue
				}
			}

			// Nếu không có releases, tạo một release mặc định
			if len(releases) == 0 {
				releases = []githubapi.ReleaseResponse{
					{
						ID:        0,
						Name:      "Default Release",
						TagName:   "v0.0.0",
						Body:      "Auto-generated default release",
						CreatedAt: time.Now(),
					},
				}
			}

			// Xử lý từng release
			for _, release := range releases {
				releaseContent := release.Body
				if releaseContent == "" {
					releaseContent = fmt.Sprintf("Release %s for %s/%s", release.TagName, user, repoName)
				}

				// Cắt nội dung để tránh lỗi
				releaseContent = model.TruncateString(releaseContent, 65000)

				// Tạo release trong database
				releaseModel := &model.Release{
					Content: releaseContent,
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

				totalReleases++

				// Gọi API để lấy thông tin commits
				for !c.rateLimiter.Allow() {
					time.Sleep(time.Duration(c.Config.GithubApi.ThrottleDelay) * time.Millisecond)
				}

				commits, err := apiCaller.CallCommits(user, repoName)
				if err != nil {
					if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "rate limit") {
						time.Sleep(60 * time.Second)
						commits, err = apiCaller.CallCommits(user, repoName)
						if err != nil {
							c.Logger.Error(ctx, "Không thể lấy dữ liệu commits sau khi đợi: %v", err)
							// Tiếp tục với release tiếp theo thay vì thất bại hoàn toàn
							continue
						}
					} else {
						c.Logger.Error(ctx, "Không thể lấy dữ liệu commits: %v", err)
						// Tiếp tục với release tiếp theo thay vì thất bại hoàn toàn
						continue
					}
				}

				// Nếu không có commits, tạo một commit mặc định
				if len(commits) == 0 {
					commits = []githubapi.CommitResponse{
						{
							SHA: fmt.Sprintf("default-commit-%d", repo.Id),
							Commit: githubapi.CommitDetail{
								Message: "Auto-generated default commit",
								Author: githubapi.CommitAuthor{
									Name:  "System",
									Email: "system@example.com",
									Date:  time.Now(),
								},
							},
						},
					}
				}

				// Xử lý từng commit
				for _, commit := range commits {
					// Cắt nội dung để tránh lỗi
					hashValue := model.TruncateString(commit.SHA, 250)
					messageValue := model.TruncateString(commit.Commit.Message, 65000)

					// Tạo commit trong database
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
						// Bỏ qua lỗi trùng lặp commit (có thể cùng commit xuất hiện trong nhiều releases)
						if strings.Contains(err.Error(), "Duplicate entry") {
							continue
						}

						tx.Rollback()
						c.Logger.Error(ctx, "Không thể lưu commit: %v", err)
						return false
					}

					totalCommits++
				}
			}

			totalRepos++
		}

		page++

		// Commit sớm mỗi trang để tránh transaction chạy quá lâu
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
	}

	// Commit transaction cuối cùng
	if tx != nil {
		if err := tx.Commit().Error; err != nil {
			c.Logger.Error(ctx, "Không thể commit transaction cuối cùng: %v", err)
			return false
		}
	}

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	// Chỉ hiển thị thông tin tổng kết
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

// Trích xuất tên người dùng và tên repository từ tên đầy đủ
func extractUserAndRepo(fullName string) (string, string) {
	parts := strings.Split(fullName, "/")
	if len(parts) > 1 {
		return parts[0], parts[1]
	}
	return "unknown", fullName
}
