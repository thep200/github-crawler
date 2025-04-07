// Crawler version 1
//  Crawler without any configuration just to using the default api from github

package crawler

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	githubapi "github.com/thep200/github-crawler/internal/github_api"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type CrawlerV1 struct {
	Logger log.Logger
	Config *cfg.Config
	Mysql  *db.Mysql
}

func NewCrawlerV1(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*CrawlerV1, error) {
	return &CrawlerV1{
		Logger: logger,
		Config: config,
		Mysql:  mysql,
	}, nil
}

func (c *CrawlerV1) Crawler() bool {
	ctx := context.Background()
	c.Logger.Info(ctx, "Starting Github star crawler version 1")
	caller := githubapi.NewCaller(c.Logger, c.Config, 1, 100)
	if _, err := caller.Call(); err != nil {
		return false
	}
	return true
}
