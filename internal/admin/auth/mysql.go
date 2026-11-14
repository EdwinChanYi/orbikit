package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const adminDatabase = "orbikit_admin"

// MySQL is a Store backed by the orbikit_admin database.
type MySQL struct {
	db *gorm.DB
}

type operator struct {
	ID           uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	PasswordHash []byte `gorm:"column:password_hash;type:varbinary(255);not null"`
}

func (operator) TableName() string { return "operators" }

type session struct {
	TokenHash []byte    `gorm:"column:token_hash;primaryKey;type:varbinary(64)"`
	ExpiresAt time.Time `gorm:"column:expires_at;type:datetime(6);not null"`
}

func (session) TableName() string { return "sessions" }

// OpenMySQL opens dsn, creates orbikit_admin when it is missing, and migrates tables.
func OpenMySQL(ctx context.Context, dsn string) (*MySQL, error) {
	db, err := connect(ctx, dsn)
	if err != nil {
		if !unknownDatabase(err) {
			return nil, err
		}
		if err := createAdminDatabase(ctx, dsn); err != nil {
			return nil, err
		}
		db, err = connect(ctx, dsn)
		if err != nil {
			return nil, err
		}
	}
	err = db.WithContext(ctx).
		Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4").
		AutoMigrate(&operator{}, &session{})
	if err != nil {
		closeDB(db)
		return nil, err
	}
	return &MySQL{db: db}, nil
}

// EnsureOperator inserts passwordHash only when the operators table is empty.
func (m *MySQL) EnsureOperator(ctx context.Context, passwordHash []byte) error {
	var n int64
	if err := m.db.WithContext(ctx).Model(&operator{}).Count(&n).Error; err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	row := operator{PasswordHash: append([]byte(nil), passwordHash...)}
	return m.db.WithContext(ctx).Create(&row).Error
}

// PasswordHash returns the stored operator hash.
func (m *MySQL) PasswordHash(ctx context.Context) ([]byte, error) {
	var row operator
	err := m.db.WithContext(ctx).Order("id").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoOperator
	}
	if err != nil {
		return nil, err
	}
	return row.PasswordHash, nil
}

// CreateSession inserts a session keyed by tokenHash.
func (m *MySQL) CreateSession(ctx context.Context, tokenHash []byte, expires time.Time) error {
	row := session{TokenHash: append([]byte(nil), tokenHash...), ExpiresAt: expires}
	return m.db.WithContext(ctx).Create(&row).Error
}

// SessionValid reports whether tokenHash exists and has not expired.
func (m *MySQL) SessionValid(ctx context.Context, tokenHash []byte, now time.Time) (bool, error) {
	var row session
	err := m.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !now.After(row.ExpiresAt), nil
}

// DeleteSession removes the session for tokenHash.
func (m *MySQL) DeleteSession(ctx context.Context, tokenHash []byte) error {
	return m.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&session{}).Error
}

func connect(ctx context.Context, dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		closeDB(db)
		return nil, err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		closeDB(db)
		return nil, err
	}
	return db, nil
}

func createAdminDatabase(ctx context.Context, dsn string) error {
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("parse mysql dsn: %w", err)
	}
	cfg.DBName = ""
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("mysql open: %w", err)
	}
	defer closeDB(db)
	stmt := "CREATE DATABASE IF NOT EXISTS `" + adminDatabase + "`"
	if err := db.WithContext(ctx).Exec(stmt).Error; err != nil {
		return fmt.Errorf("create database %s: %w", adminDatabase, err)
	}
	return nil
}

func unknownDatabase(err error) bool {
	var me *mysqldriver.MySQLError
	// 1049 is MySQL ER_BAD_DB_ERROR: unknown database.
	return errors.As(err, &me) && me.Number == 1049
}

func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	_ = sqlDB.Close()
}
