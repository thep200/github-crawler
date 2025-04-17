package model

import (
	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Model struct {
	Config *cfg.Config `gorm:"-"`
	Logger log.Logger  `gorm:"-"`
	Mysql  *db.Mysql   `gorm:"-"`
}
