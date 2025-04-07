package log

import "context"

type Logger interface {
	Info(ctx context.Context, format string, args ...interface{})
	Alert(ctx context.Context, format string, args ...interface{})
	Error(ctx context.Context, format string, args ...interface{})
	Warn(ctx context.Context, format string, args ...interface{})
	Debug(ctx context.Context, format string, args ...interface{})
	Notice(ctx context.Context, format string, args ...interface{})
	Critical(ctx context.Context, format string, args ...interface{})
	Emergency(ctx context.Context, format string, args ...interface{})
}

func NewLogger(logger Logger) (Logger, error) {
	return logger, nil
}
