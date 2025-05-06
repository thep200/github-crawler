package crawler

import (
	"context"

	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/internal/model"
	"gorm.io/gorm"
)

func (c *CrawlerV1) crawlReleases(ctx context.Context, db *gorm.DB, apiCaller *githubapi.Caller, user, repoName string, repoID int) ([]githubapi.ReleaseResponse, int, int, error) {
	c.applyRateLimit()

	// Call API to get releases
	releases, err := apiCaller.CallReleases(user, repoName)
	if err != nil {
		if c.isRateLimitError(err) {
			c.handleRateLimit(ctx, err)
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

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	c.addProcessedRelease(repoID, releaseName)

	return releaseModel, nil
}
