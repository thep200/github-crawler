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

func (c *CrawlerV1) Crawl() bool {
	ctx := context.Background()
	c.Logger.Info(ctx, "Starting Github star crawler version 1")
	total := make([]map[string]interface{}, 0, 5000)
	page := 1
	for {
		if len(total) < 5000 {
			c.Logger.Info(ctx, "Number of total: %d", len(total))
			caller := githubapi.NewCaller(c.Logger, c.Config, page, 100)
			res, err := caller.Call()
			if err != nil {
				return false
			}
			for _, i := range res {
				c.Logger.Info(ctx, "ID: %d, Name: %s, Stars: %d\n", i.Id, i.Name, i.StargazersCount)
				total = append(total, map[string]interface{}{
					"id":    i.Id,
					"name":  i.Name,
					"stars": i.StargazersCount,
				})
			}
		} else {
			break
		}
		page++
	}

	return true
}
