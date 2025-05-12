package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/log"
)

// Producer handles Kafka message publishing
type Producer struct {
	Config *cfg.Config
	Logger log.Logger
	writer *kafka.Writer
}

// Message is the structure of messages sent to Kafka
type Message struct {
	Key   string
	Value interface{}
}

// NewProducer creates and returns a new Kafka Producer
func NewProducer(config *cfg.Config, logger log.Logger, topic string) *Producer {
	if len(config.Kafka.Brokers) == 0 {
		panic("no kafka brokers configured")
	}

	writer := &kafka.Writer{
		Addr:         kafka.TCP(config.Kafka.Brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
		RequiredAcks: kafka.RequireAll,
	}

	return &Producer{
		Config: config,
		Logger: logger,
		writer: writer,
	}
}

// Publish sends a message to the Kafka topic
func (p *Producer) Publish(ctx context.Context, key string, value interface{}) error {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	msg := kafka.Message{
		Key:   []byte(key),
		Value: jsonBytes,
		Time:  time.Now(),
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("failed to write message to kafka: %w", err)
	}

	return nil
}

// Close closes the Kafka writer
func (p *Producer) Close() error {
	return p.writer.Close()
}
