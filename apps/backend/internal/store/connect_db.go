package store

import (
	"context"
	"fmt"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenMySQL opens a GORM MySQL handle with parseTime forced on, a five-minute
// connection lifetime, and the privacy-preserving databaseLogger, then pings it
// with a five-second timeout so startup fails before serving traffic.
func OpenMySQL(dsn string) (*gorm.DB, error) {
	normalized, err := normalizeMySQLDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(gormmysql.Open(normalized), &gorm.Config{Logger: databaseLogger{level: logger.Info}})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql database: %w", err)
	}
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close() // best-effort: the ping error is what the caller needs
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// normalizeMySQLDSN validates the DSN and forces parseTime=true, which GORM
// needs to scan DATETIME columns into time.Time.
func normalizeMySQLDSN(dsn string) (string, error) {
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("parse mysql dsn: %w", err)
	}
	cfg.ParseTime = true
	return cfg.FormatDSN(), nil
}
