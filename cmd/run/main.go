package main

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/crawler"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Handler struct {
	Crawler crawler.Crawler
	Logger  log.Logger
}

func NewHandler(crawler crawler.Crawler, logger log.Logger) *Handler {
	return &Handler{
		Crawler: crawler,
		Logger:  logger,
	}
}

func main() {
	ctx := context.Background()
	// loader, _ := cfg.NewMockLoader()
	loader, _ := cfg.NewViperLoader()
	config, _ := loader.Load()
	mysql, _ := db.NewMysql(config)
	logger, _ := log.NewCslLogger()
	commitMd, _ := model.NewCommit(config, logger, mysql)
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	crawler, _ := crawler.NewCrawlerV1(logger, config, mysql)

	// Migrate database
	mysql.Migrate(commitMd, repoMd, releaseMd)

	//
	logger.Info(ctx, "Starting Github star crawler")
	handler := NewHandler(crawler, logger)
	if handler.Crawler.Crawl() {
		logger.Info(ctx, "Successfully!")
	} else {
		logger.Error(ctx, "Failed!")
	}
}
