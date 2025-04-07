package main

import (
	"context"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/crawler"
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
	loader, _ := cfg.NewMockLoader()
	config, _ := loader.Load()
	mysql, _ := db.NewMysql(config)
	logger, _ := log.NewCslLogger()
	crawler, _ := crawler.NewCrawlerV1(logger, config, mysql)

	//
	logger.Info(ctx, "Starting Github star crawler")
	handler := NewHandler(crawler, logger)
	if handler.Crawler.Crawler() {
		logger.Info(ctx, "Successfully!")
	} else {
		logger.Error(ctx, "Failed!")
	}
}
