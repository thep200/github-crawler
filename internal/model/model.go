package model

import (
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/db"
	"github.com/thep200/github-crawler/pkg/log"
)

type Model struct {
	Config    *cfg.Config    `gorm:"-"`
	Logger    log.Logger     `gorm:"-"`
	Mysql     *db.Mysql      `gorm:"-"`
	ID        uint           `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}
