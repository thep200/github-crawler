package kafka

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/log"
)

// Consumer handles Kafka message consumption
type Consumer struct {
	Config   *cfg.Config
	Logger   log.Logger
	reader   *kafka.Reader
	handlers map[string]func([]byte) error
}

// NewConsumer creates and returns a new Kafka Consumer
func NewConsumer(config *cfg.Config, logger log.Logger, topic, groupID string) *Consumer {
	if len(config.Kafka.Brokers) == 0 {
		panic("no kafka brokers configured")
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        config.Kafka.Brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       10e3,        // 10KB
		MaxBytes:       10e6,        // 10MB
		MaxWait:        time.Second, // Maximum amount of time to wait for new data
		StartOffset:    kafka.FirstOffset,
		RetentionTime:  7 * 24 * time.Hour, // 1 week
		CommitInterval: time.Second,        // Flush commits to Kafka every second
	})

	return &Consumer{
		Config:   config,
		Logger:   logger,
		reader:   reader,
		handlers: make(map[string]func([]byte) error),
	}
}

// RegisterHandler registers a message handler for a specific message key
func (c *Consumer) RegisterHandler(key string, handler func([]byte) error) {
	c.handlers[key] = handler
}

// Start begins consuming messages from the Kafka topic
func (c *Consumer) Start(ctx context.Context) error {
	c.Logger.Info(ctx, "Starting Kafka consumer for topic: %s", c.reader.Config().Topic)

	for {
		select {
		case <-ctx.Done():
			return c.reader.Close()
		default:
			message, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if err == context.Canceled || err == context.DeadlineExceeded {
					return nil
				}
				c.Logger.Error(ctx, "Error reading message: %v", err)
				continue
			}

			// Handle the message
			key := string(message.Key)
			if handler, exists := c.handlers[key]; exists {
				if err := handler(message.Value); err != nil {
					c.Logger.Error(ctx, "Error handling message with key %s: %v", key, err)
				} else {
					c.Logger.Info(ctx, "Successfully processed message with key: %s", key)
				}
			} else {
				c.Logger.Warn(ctx, "No handler registered for message with key: %s", key)
			}
		}
	}
}

// Close closes the Kafka reader
func (c *Consumer) Close() error {
	return c.reader.Close()
}
