package db

import (
	"context"
	"fmt"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Mysql struct {
	Config *cfg.Config
	db     *gorm.DB
}

func NewMysql(config *cfg.Config) (*Mysql, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		config.Mysql.Username,
		config.Mysql.Password,
		config.Mysql.Host,
		config.Mysql.Port,
		config.Mysql.Database)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Set connection pool settings
	sqlDB.SetMaxIdleConns(config.Mysql.MaxIdleConnection)
	sqlDB.SetMaxOpenConns(config.Mysql.MaxOpenConnection)
	sqlDB.SetConnMaxLifetime(time.Duration(config.Mysql.MaxLifeTimeConnection) * time.Second)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Mysql{
		Config: config,
		db:     db,
	}, nil
}

func (m *Mysql) Db() (*gorm.DB, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}
	return m.db, nil
}

// Migrate runs auto migration for the provided models
func (m *Mysql) Migrate(models ...interface{}) error {
	if m.db == nil {
		return fmt.Errorf("database connection not initialized")
	}

	fmt.Println("Starting database migration...")
	for _, model := range models {
		if err := m.db.AutoMigrate(model); err != nil {
			return fmt.Errorf("failed to migrate model %T: %w", model, err)
		}
		fmt.Printf("Successfully migrated model: %T\n", model)
	}
	fmt.Println("Database migration completed successfully")

	return nil
}

// Close closes the database connection
func (m *Mysql) Close() error {
	if m.db == nil {
		return nil
	}

	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	return sqlDB.Close()
}
