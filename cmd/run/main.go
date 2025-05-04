package main

import (
	"context"
	"flag"

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
	// Parse parameter from command line
	version := flag.String("version", "", "Version of crawler to use")
	flag.Parse()
	if *version == "" {
		panic("Version is required. Use -version flag to specify the version, like -version=v1")
	}

	// Dependency injection
	ctx := context.Background()
	loader, _ := cfg.NewViperLoader()
	config, _ := loader.Load()
	mysql, _ := db.NewMysql(config)
	logger, _ := log.NewCslLogger()
	commitMd, _ := model.NewCommit(config, logger, mysql)
	repoMd, _ := model.NewRepo(config, logger, mysql)
	releaseMd, _ := model.NewRelease(config, logger, mysql)
	crawler, _ := crawler.FactoryCrawler("v1", logger, config, mysql)

	// Migrate database
	mysql.Migrate(commitMd, repoMd, releaseMd)

	// Crawl
	logger.Info(ctx, "Starting Github star crawler")
	handler := NewHandler(crawler, logger)
	if handler.Crawler.Crawl() {
		logger.Info(ctx, "Successfully!")
	} else {
		logger.Error(ctx, "Failed!")
	}
}
