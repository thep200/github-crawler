package crawler

import (
	"fmt"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

func FactoryCrawler(version string, logger log.Logger, config *cfg.Config, mysql *db.Mysql) (Crawler, error) {
	switch version {
	case "v1":
		return NewCrawlerV1(logger, config, mysql)
	case "v2":
		return NewCrawlerV2(logger, config, mysql)
	case "v3":
		return NewCrawlerV3(logger, config, mysql)
	default:
		return nil, fmt.Errorf("[ERROR] Unsupported crawler version: %s", version)
	}
}
