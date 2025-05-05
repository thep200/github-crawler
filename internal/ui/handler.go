package ui

import (
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/internal/model"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
	"gorm.io/gorm"
)

// UI Handler
type Handler struct {
	Logger  log.Logger
	Config  *cfg.Config
	MySQL   *db.Mysql
	RepoMd  *model.Repo
	db      *gorm.DB
	baseDir string
}

// Constructor
func NewHandler(logger log.Logger, config *cfg.Config, mysql *db.Mysql) (*Handler, error) {
	repoMd, _ := model.NewRepo(config, logger, mysql)

	db, err := mysql.Db()
	if err != nil {
		return nil, err
	}

	return &Handler{
		Logger:  logger,
		Config:  config,
		MySQL:   mysql,
		RepoMd:  repoMd,
		db:      db,
		baseDir: "internal/ui/static",
	}, nil
}

// Routing
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static file
	fileServer := http.FileServer(http.Dir(h.baseDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	// API
	mux.HandleFunc("/api/repos", h.getRepos)
	mux.HandleFunc("/api/releases", h.getReleases)
	mux.HandleFunc("/api/commits", h.getCommits)

	// Web UI router
	mux.HandleFunc("/home", h.showHomePage)
}

func (h *Handler) showHomePage(w http.ResponseWriter, r *http.Request) {
	// Get HTML from static directory
	templatePath := filepath.Join(h.baseDir, "index.html")
	temp, err := template.ParseFiles(templatePath)
	if err != nil {
		h.Logger.Error(r.Context(), "Failed to parse template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Render the template
	if err := temp.Execute(w, nil); err != nil {
		h.Logger.Error(r.Context(), "Failed to execute template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}
