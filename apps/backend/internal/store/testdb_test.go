package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openSQLiteTestDB creates an isolated in-memory SQLite database.
func openSQLiteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:schema-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	return db
}

// openMySQLTestDB creates and later drops a dedicated database so integration tests never alter operator data.
func openMySQLTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUACK_TEST_MYSQL_DSN is not configured")
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse QUACK_TEST_MYSQL_DSN: %v", err)
	}
	cfg.ParseTime = true
	databaseName := fmt.Sprintf("quack_schema_%d", time.Now().UTC().UnixNano())
	adminCfg := *cfg
	adminCfg.DBName = ""
	adminDB, err := gorm.Open(gormmysql.Open(adminCfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	if err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error; err != nil {
		t.Fatalf("create isolated MySQL test database: %v", err)
	}
	t.Cleanup(func() {
		if err := adminDB.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`").Error; err != nil {
			t.Errorf("drop isolated MySQL test database: %v", err)
		}
		if sqlDB, err := adminDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	testCfg := *cfg
	testCfg.DBName = databaseName
	db, err := OpenMySQL(testCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open isolated MySQL test database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
