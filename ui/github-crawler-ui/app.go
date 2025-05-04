package main

import (
	"context"
	"fmt"

	"github.com/thep200/github-crawler/api"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx           context.Context
	crawler       *api.CrawlerAPI
	initError     string
	isInitialized bool
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		crawler:       api.NewCrawlerAPI(),
		isInitialized: false,
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize the crawler API
	err := a.crawler.Initialize(ctx)
	if err != nil {
		a.initError = fmt.Sprintf("Failed to initialize crawler: %v", err)
		fmt.Println(a.initError) // In ra console để debug
		runtime.LogErrorf(ctx, "Initialization error: %v", err)
		// Không return ở đây, chúng ta vẫn muốn UI hiển thị thông báo lỗi
	} else {
		a.isInitialized = true
		runtime.LogInfo(ctx, "Crawler initialized successfully")
	}
}

// StartCrawling starts the crawling process with the specified version
func (a *App) StartCrawling(version string) string {
	if !a.isInitialized {
		return fmt.Sprintf("Error: Crawler is not initialized. %s", a.initError)
	}

	result, err := a.crawler.StartCrawling(version)
	if err != nil {
		errMsg := fmt.Sprintf("Error starting crawler: %v", err)
		runtime.LogErrorf(a.ctx, errMsg)
		return errMsg
	}

	runtime.LogInfof(a.ctx, "Started crawling with version %s", version)
	return result
}

// StopCrawling attempts to stop the crawling process
func (a *App) StopCrawling() string {
	if !a.isInitialized {
		return fmt.Sprintf("Error: Crawler is not initialized. %s", a.initError)
	}

	result, err := a.crawler.StopCrawling()
	if err != nil {
		errMsg := fmt.Sprintf("Error stopping crawler: %v", err)
		runtime.LogErrorf(a.ctx, errMsg)
		return errMsg
	}

	runtime.LogInfo(a.ctx, "Stopped crawling")
	return result
}

// GetCrawlStats returns the current crawling statistics
func (a *App) GetCrawlStats() *api.CrawlStats {
	if !a.isInitialized {
		return &api.CrawlStats{
			IsRunning: false,
			LastError: fmt.Sprintf("Crawler is not initialized. %s", a.initError),
		}
	}

	stats, err := a.crawler.GetCrawlStats()
	if err != nil {
		errMsg := fmt.Sprintf("Error getting stats: %v", err)
		runtime.LogErrorf(a.ctx, errMsg)
		return &api.CrawlStats{
			LastError: errMsg,
		}
	}

	return stats
}

// GetDbStatus checks the database connection status
func (a *App) GetDbStatus() string {
	if !a.isInitialized {
		return fmt.Sprintf("Error: Crawler is not initialized. %s", a.initError)
	}

	status, err := a.crawler.GetDatabaseStatus()
	if err != nil {
		errMsg := fmt.Sprintf("Database error: %v", err)
		runtime.LogErrorf(a.ctx, errMsg)
		return errMsg
	}

	return status
}

// GetInitStatus returns crawler initialization status and any error message
func (a *App) GetInitStatus() map[string]interface{} {
	return map[string]interface{}{
		"initialized": a.isInitialized,
		"error":       a.initError,
	}
}
