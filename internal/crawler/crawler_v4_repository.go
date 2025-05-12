package crawler

import (
	"context"

	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

func (c *CrawlerV4) crawlRepo(ctx context.Context, tx *gorm.DB, repo githubapi.GithubAPIResponse) (int, bool, error) {
	// Extract username and repo name
	user := repo.Owner.Login
	repoName := repo.Name

	if user == "" {
		user, repoName = extractUserAndRepo(repo.FullName)
		if user == "" {
			user = "unknown"
		}
	}

	// Kiểm tra xem repository đã được xử lý hay chưa
	if c.isProcessed(repo.Id) {
		return int(repo.Id), true, nil
	}

	// Tạo message để gửi vào Kafka
	repoMsg := model.RepoMessage{
		ID:         int(repo.Id),
		User:       model.TruncateString(user, 250),
		Name:       model.TruncateString(repoName, 250),
		StarCount:  int(repo.StargazersCount),
		ForkCount:  int(repo.ForksCount),
		WatchCount: int(repo.WatchersCount),
		IssueCount: int(repo.OpenIssuesCount),
	}

	// Gửi message vào Kafka
	if err := c.repoProducer.Publish(ctx, "repo", repoMsg); err != nil {
		return 0, false, err
	}

	c.addProcessedID(repo.Id)
	return int(repo.Id), false, nil
}
