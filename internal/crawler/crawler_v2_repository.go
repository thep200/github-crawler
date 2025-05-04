package crawler

import (
	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

func (c *CrawlerV2) crawlRepo(tx *gorm.DB, repo githubapi.GithubAPIResponse) (*model.Repo, bool, error) {
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
