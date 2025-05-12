package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/kafka"
	"github.com/thep200/github-crawler/pkg/log"
)

func main() {
	// Parse command line arguments
	consumerType := flag.String("type", "", "Type of consumer to run (repo, release, commit)")
	flag.Parse()

	if *consumerType == "" {
		fmt.Println("Please specify a consumer type: -type=[repo|release|commit]")
		os.Exit(1)
	}

	// Load configuration
	loader, _ := cfg.NewViperLoader()
	config, err := loader.Load()
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	logger, _ := log.NewCslLogger()

	// Setup database
	mysql, _ := db.NewMysql(config)
	if err != nil {
		logger.Error(context.Background(), "Failed to connect to database: %v", err)
		os.Exit(1)
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create models
	repoModel, _ := model.NewRepo(config, logger, mysql)
	releaseModel, _ := model.NewRelease(config, logger, mysql)
	commitModel, _ := model.NewCommit(config, logger, mysql)

	// Setup signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start the appropriate consumer based on type
	switch *consumerType {
	case "repo":
		startRepoConsumer(ctx, config, logger, repoModel)
	case "release":
		startReleaseConsumer(ctx, config, logger, releaseModel)
	case "commit":
		startCommitConsumer(ctx, config, logger, commitModel)
	default:
		logger.Error(ctx, "Unknown consumer type: %s", *consumerType)
		os.Exit(1)
	}

	// Wait for termination signal
	<-sigCh
	logger.Info(ctx, "Received shutdown signal, gracefully shutting down...")
	cancel()
}

func startRepoConsumer(ctx context.Context, config *cfg.Config, logger log.Logger, repoModel *model.Repo) {
	consumer := kafka.NewConsumer(config, logger, config.Kafka.Producer.TopicRepo, "repo-consumer-group")

	// Channel for collecting messages in batches
	batchSize := 100
	batchTimeout := 5 * time.Second

	// Channel to collect messages for batch processing
	messages := make(chan model.RepoMessage, batchSize*2)

	// Batch processor
	go processBatchedRepos(ctx, messages, batchSize, batchTimeout, logger, repoModel)

	// Register handler for repo messages
	consumer.RegisterHandler("repo", func(data []byte) error {
		var repoMsg model.RepoMessage
		if err := json.Unmarshal(data, &repoMsg); err != nil {
			return fmt.Errorf("failed to unmarshal repo message: %w", err)
		}

		// Send to batch channel instead of processing individually
		select {
		case messages <- repoMsg:
			// Message added to batch
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})

	// Start consumer in a goroutine
	go func() {
		if err := consumer.Start(ctx); err != nil {
			logger.Error(ctx, "Repo consumer error: %v", err)
		}
	}()

	logger.Info(ctx, "Repository consumer started successfully")
}

// New function to process batches of repository messages
func processBatchedRepos(ctx context.Context, messages <-chan model.RepoMessage, batchSize int,
	batchTimeout time.Duration, logger log.Logger, repoModel *model.Repo) {

	var batch []model.RepoMessage
	timer := time.NewTimer(batchTimeout)

	for {
		select {
		case <-ctx.Done():
			// Process remaining messages before exiting
			if len(batch) > 0 {
				processSingleBatch(ctx, batch, logger, repoModel)
			}
			return

		case msg := <-messages:
			batch = append(batch, msg)

			// Process batch when it reaches the desired size
			if len(batch) >= batchSize {
				processSingleBatch(ctx, batch, logger, repoModel)
				batch = nil // Reset batch
				timer.Reset(batchTimeout)
			}

		case <-timer.C:
			// Process batch on timeout if there are any messages
			if len(batch) > 0 {
				processSingleBatch(ctx, batch, logger, repoModel)
				batch = nil // Reset batch
			}
			timer.Reset(batchTimeout)
		}
	}
}

// Process a single batch of repositories
func processSingleBatch(ctx context.Context, batch []model.RepoMessage, logger log.Logger, repoModel *model.Repo) {
	if len(batch) == 0 {
		return
	}

	logger.Info(ctx, "Processing batch of %d repositories", len(batch))

	// Use transactions for batch inserts
	err := repoModel.CreateBatch(batch)
	if err != nil {
		logger.Error(ctx, "Failed to save batch of repositories: %v", err)
	} else {
		logger.Info(ctx, "Successfully saved batch of %d repositories", len(batch))
	}
}

func startReleaseConsumer(ctx context.Context, config *cfg.Config, logger log.Logger, releaseModel *model.Release) {
	consumer := kafka.NewConsumer(config, logger, config.Kafka.Producer.TopicRelease, "release-consumer-group")

	// Register handler for release messages
	consumer.RegisterHandler("release", func(data []byte) error {
		var releaseMsg model.ReleaseMessage
		if err := json.Unmarshal(data, &releaseMsg); err != nil {
			return fmt.Errorf("failed to unmarshal release message: %w", err)
		}

		// Save release to database
		if err := releaseModel.Create(releaseMsg.Content, releaseMsg.RepoID); err != nil {
			return fmt.Errorf("failed to save release to database: %w", err)
		}

		return nil
	})

	// Start consumer in a goroutine
	go func() {
		if err := consumer.Start(ctx); err != nil {
			logger.Error(ctx, "Release consumer error: %v", err)
		}
	}()

	logger.Info(ctx, "Release consumer started successfully")
}

func startCommitConsumer(ctx context.Context, config *cfg.Config, logger log.Logger, commitModel *model.Commit) {
	consumer := kafka.NewConsumer(config, logger, config.Kafka.Producer.TopicCommit, "commit-consumer-group")

	// Register handler for commit messages
	consumer.RegisterHandler("commit", func(data []byte) error {
		var commitMsg model.CommitMessage
		if err := json.Unmarshal(data, &commitMsg); err != nil {
			return fmt.Errorf("failed to unmarshal commit message: %w", err)
		}

		// Save commit to database
		if err := commitModel.Create(commitMsg.Hash, commitMsg.Message, commitMsg.ReleaseID); err != nil {
			return fmt.Errorf("failed to save commit to database: %w", err)
		}

		return nil
	})

	// Start consumer in a goroutine
	go func() {
		if err := consumer.Start(ctx); err != nil {
			logger.Error(ctx, "Commit consumer error: %v", err)
		}
	}()

	logger.Info(ctx, "Commit consumer started successfully")
}
