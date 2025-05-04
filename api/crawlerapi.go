// Package api cung cấp các API public để tương tác với GitHub crawler
package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/crawler"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

// CrawlStats chứa thống kê về quá trình crawling
type CrawlStats struct {
	Version         string    `json:"version"`
	IsRunning       bool      `json:"isRunning"`
	StartTime       time.Time `json:"startTime"`
	Duration        string    `json:"duration"`
	ReposCrawled    int       `json:"reposCrawled"`
	ReleasesCrawled int       `json:"releasesCrawled"`
	CommitsCrawled  int       `json:"commitsCrawled"`
	LastError       string    `json:"lastError"`
}

// CrawlerAPI cung cấp các API để tương tác với GitHub Crawler
type CrawlerAPI struct {
	ctx           context.Context
	config        *cfg.Config
	logger        log.Logger
	mysql         *db.Mysql
	crawlerV1     crawler.Crawler
	crawlerV2     crawler.Crawler
	crawling      bool
	crawlStatsMu  sync.RWMutex
	crawlStats    *CrawlStats
	stopCrawlChan chan struct{}
}

// NewCrawlerAPI tạo một instance mới của CrawlerAPI
func NewCrawlerAPI() *CrawlerAPI {
	return &CrawlerAPI{
		crawlStats:    &CrawlStats{},
		stopCrawlChan: make(chan struct{}),
	}
}

// Initialize khởi tạo các thành phần cần thiết cho crawler
func (a *CrawlerAPI) Initialize(ctx context.Context) error {
	a.ctx = ctx

	var err error

	// Load configuration
	loader, _ := cfg.NewViperLoader()
	a.config, err = loader.Load()
	if err != nil {
		a.logger, _ = log.NewCslLogger()
		a.logger.Error(a.ctx, "Failed to load configuration: %v", err)
		return err
	}

	// Set up logger
	a.logger, err = log.NewCslLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Set up database
	a.mysql, err = db.NewMysql(a.config)
	if err != nil {
		a.logger.Error(a.ctx, "Failed to connect to database: %v", err)
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Initialize crawlers
	a.crawlerV1, err = crawler.NewCrawlerV1(a.logger, a.config, a.mysql)
	if err != nil {
		a.logger.Error(a.ctx, "Failed to create crawler V1: %v", err)
		// Không return ở đây, chúng ta vẫn có thể thử khởi tạo V2
	}

	a.crawlerV2, err = crawler.NewCrawlerV2(a.logger, a.config, a.mysql)
	if err != nil {
		a.logger.Error(a.ctx, "Failed to create crawler V2: %v", err)
		// Không return ở đây
	}

	// Kiểm tra xem có ít nhất một crawler được khởi tạo thành công không
	if a.crawlerV1 == nil && a.crawlerV2 == nil {
		return errors.New("failed to initialize any crawler")
	}

	// Migrate database tables
	return a.migrateDatabase()
}

// migrateDatabase đảm bảo các bảng cần thiết tồn tại
func (a *CrawlerAPI) migrateDatabase() error {
	if a.mysql == nil {
		return errors.New("database connection not initialized")
	}

	repoMd, err := model.NewRepo(a.config, a.logger, a.mysql)
	if err != nil {
		return fmt.Errorf("failed to create repo model: %w", err)
	}

	releaseMd, err := model.NewRelease(a.config, a.logger, a.mysql)
	if err != nil {
		return fmt.Errorf("failed to create release model: %w", err)
	}

	commitMd, err := model.NewCommit(a.config, a.logger, a.mysql)
	if err != nil {
		return fmt.Errorf("failed to create commit model: %w", err)
	}

	return a.mysql.Migrate(repoMd, releaseMd, commitMd)
}

// StartCrawling bắt đầu quá trình crawling với phiên bản được chỉ định
func (a *CrawlerAPI) StartCrawling(version string) (string, error) {
	// Check if already crawling
	a.crawlStatsMu.RLock()
	isCrawling := a.crawling
	a.crawlStatsMu.RUnlock()

	if isCrawling {
		return "Crawling is already in progress", nil
	}

	// Kiểm tra crawler được chọn có tồn tại không
	var selectedCrawler crawler.Crawler
	switch version {
	case "v1":
		if a.crawlerV1 == nil {
			return "", errors.New("crawler V1 is not initialized")
		}
		selectedCrawler = a.crawlerV1
	case "v2":
		if a.crawlerV2 == nil {
			return "", errors.New("crawler V2 is not initialized")
		}
		selectedCrawler = a.crawlerV2
	default:
		return "", errors.New("invalid crawler version: " + version)
	}

	// Create new stats
	a.crawlStatsMu.Lock()
	a.crawling = true
	a.crawlStats = &CrawlStats{
		Version:   version,
		IsRunning: true,
		StartTime: time.Now(),
	}
	a.crawlStatsMu.Unlock()

	// Start crawling in a goroutine
	go func(c crawler.Crawler) {
		success := c.Crawl()

		a.updateCrawlStats(func(stats *CrawlStats) {
			stats.IsRunning = false
			if !success {
				stats.LastError = "Crawling failed"
			}
		})

		a.crawlStatsMu.Lock()
		a.crawling = false
		a.crawlStatsMu.Unlock()
	}(selectedCrawler)

	return "Started crawling with version " + version, nil
}

// StopCrawling dừng quá trình crawling
func (a *CrawlerAPI) StopCrawling() (string, error) {
	a.crawlStatsMu.RLock()
	isCrawling := a.crawling
	a.crawlStatsMu.RUnlock()

	if !isCrawling {
		return "No crawling is in progress", nil
	}

	// Signal to stop crawling
	select {
	case a.stopCrawlChan <- struct{}{}:
	default:
	}

	return "Stopping crawling process (may take some time to complete)", nil
}

// GetCrawlStats trả về thống kê về quá trình crawling
func (a *CrawlerAPI) GetCrawlStats() (*CrawlStats, error) {
	a.crawlStatsMu.RLock()
	defer a.crawlStatsMu.RUnlock()

	// Nếu crawlStats là nil, khởi tạo một đối tượng trống
	if a.crawlStats == nil {
		return &CrawlStats{}, nil
	}

	// Calculate duration if crawling is running
	stats := *a.crawlStats
	if stats.IsRunning {
		stats.Duration = time.Since(stats.StartTime).String()
	}

	return &stats, nil
}

// updateCrawlStats cập nhật thống kê về quá trình crawling một cách an toàn
func (a *CrawlerAPI) updateCrawlStats(updateFn func(*CrawlStats)) {
	a.crawlStatsMu.Lock()
	defer a.crawlStatsMu.Unlock()

	if a.crawlStats == nil {
		a.crawlStats = &CrawlStats{}
	}

	updateFn(a.crawlStats)
}

// GetDatabaseStatus kiểm tra trạng thái kết nối cơ sở dữ liệu
func (a *CrawlerAPI) GetDatabaseStatus() (string, error) {
	if a.mysql == nil {
		return "Database not initialized", nil
	}

	db, err := a.mysql.Db()
	if err != nil {
		return "Database error: " + err.Error(), err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return "Database error: " + err.Error(), err
	}

	if err := sqlDB.Ping(); err != nil {
		return "Database not connected: " + err.Error(), err
	}

	return "Database connected", nil
}
