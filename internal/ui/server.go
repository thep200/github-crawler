package ui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

// Server represents the UI web server
type Server struct {
	Logger log.Logger
	Config *cfg.Config
	MySQL  *db.Mysql
	server *http.Server
	port   int
}

// NewServer creates a new UI server
func NewServer(logger log.Logger, config *cfg.Config, mysql *db.Mysql, port int) (*Server, error) {
	return &Server{
		Logger: logger,
		Config: config,
		MySQL:  mysql,
		port:   port,
	}, nil
}

// Start initializes and starts the HTTP server
func (s *Server) Start() error {
	handler, err := NewHandler(s.Logger, s.Config, s.MySQL)
	if err != nil {
		return fmt.Errorf("failed to create UI handler: %w", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.Logger.Info(context.Background(), "Starting UI server on port %d", s.port)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %w", err)
	}

	return nil
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		s.Logger.Info(ctx, "Shutting down UI server")
		return s.server.Shutdown(ctx)
	}
	return nil
}
