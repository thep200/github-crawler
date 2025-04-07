package log

import (
	"log"
	"context"
)

type CslLogger struct {}

func NewCslLogger() (*CslLogger, error) {
	return &CslLogger{}, nil
}

func (l *CslLogger) Info(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[INFO] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Alert(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[ALERT] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Error(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[ERROR] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Warn(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[WARN] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Debug(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[DEBUG] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Critical(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[CRITICAL] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Emergency(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[EMERGENCY] " + format
	log.Printf(newFormat, args...)
}

func (l *CslLogger) Notice(ctx context.Context, format string, args ...interface{}) {
	newFormat := "[NOTICE] " + format
	log.Printf(newFormat, args...)
}

