package db

import (
	"database/sql"
	"sync"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/thep200/github-crawler/cfg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var (
	initErr error
)

type Mysql struct {
	Config *cfg.Config
	once   sync.Once
	db     *gorm.DB
}

func NewMysql(config *cfg.Config) (*Mysql, error) {
	return &Mysql{
		Config: config,
	}, nil
}

func (m *Mysql) DSN() string {
	config := mysqlDriver.Config{
		User:                 m.Config.Mysql.Username,
		Passwd:               m.Config.Mysql.Password,
		DBName:               m.Config.Mysql.Database,
		Addr:                 m.Config.Mysql.Host + ":" + m.Config.Mysql.Port,
		Net:                  "tcp",
		ParseTime:            true,
		AllowNativePasswords: true,
	}
	return config.FormatDSN()
}

func (m *Mysql) Db() (*gorm.DB, error) {
	m.once.Do(func() {
		// Open connection
		var db *gorm.DB
		db, initErr = gorm.Open(mysql.Open(m.DSN()), &gorm.Config{})
		if initErr != nil {
			return
		}

		// Get sqlDB
		var sqlDB *sql.DB
		sqlDB, initErr = db.DB()
		if initErr != nil {
			return
		}

		// Setting connection pool
		sqlDB.SetMaxIdleConns(m.Config.Mysql.MaxIdleConnection)
		sqlDB.SetMaxOpenConns(m.Config.Mysql.MaxOpenConnection)
		sqlDB.SetConnMaxLifetime(time.Duration(m.Config.Mysql.MaxLifeTimeConnection) * time.Second)

		//
		m.db = db
	})
	return m.db, initErr
}

func (m *Mysql) Ping() error {
	db, err := m.Db()
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

func (m *Mysql) Close() error {
	if m.db != nil {
		sqlDB, err := m.db.DB()
		if err != nil {
			return err
		}
		sqlDB.Close()
	}
	return nil
}

func (m *Mysql) Migrate(models ...interface{}) error {
	db, err := m.Db()
	if err != nil {
		return err
	}
	return db.AutoMigrate(models...)
}
