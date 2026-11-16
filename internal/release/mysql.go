package release

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MySQL stores release tasks in orbikit_admin.
type MySQL struct {
	db *gorm.DB
}

type releaseRow struct {
	ID         uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	Service    string     `gorm:"column:service;size:32;not null"`
	Kind       string     `gorm:"column:kind;size:16;not null"`
	GitRef     string     `gorm:"column:git_ref;size:128"`
	ImageID    string     `gorm:"column:image_id;size:128"`
	Status     string     `gorm:"column:status;size:16;not null;index"`
	Log        string     `gorm:"column:log;type:mediumtext"`
	Error      string     `gorm:"column:error;size:255"`
	Operator   string     `gorm:"column:operator;size:32;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;type:datetime(6);not null"`
	FinishedAt *time.Time `gorm:"column:finished_at;type:datetime(6)"`
}

func (releaseRow) TableName() string { return "releases" }

// OpenMySQL migrates the releases table. The database is created by the operator store.
func OpenMySQL(ctx context.Context, dsn string) (*MySQL, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	err = db.WithContext(ctx).
		Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4").
		AutoMigrate(&releaseRow{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &MySQL{db: db}, nil
}

// Expire marks open tasks older than Timeout as failed.
func (s *MySQL) Expire(ctx context.Context, now time.Time) error {
	return s.db.WithContext(ctx).Model(&releaseRow{}).
		Where("status IN ? AND created_at < ?", []string{StatusQueued, StatusRunning}, now.Add(-Timeout)).
		Updates(map[string]any{"status": StatusFailed, "error": "超时", "finished_at": now}).Error
}

// Active reports whether a task is still open.
func (s *MySQL) Active(ctx context.Context) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&releaseRow{}).
		Where("status IN ?", []string{StatusQueued, StatusRunning}).
		Count(&n).Error
	return n > 0, err
}

// Create inserts one task when none are open.
func (s *MySQL) Create(ctx context.Context, task Task) (Task, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&releaseRow{}).Where("status IN ?", []string{StatusQueued, StatusRunning}).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrBusy
		}
		if task.CreatedAt.IsZero() {
			task.CreatedAt = time.Now()
		}
		row := releaseRow{
			Service: task.Service, Kind: task.Kind, GitRef: task.GitRef, ImageID: task.ImageID,
			Status: StatusQueued, Operator: Operator, CreatedAt: task.CreatedAt,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		task.ID = row.ID
		task.Status = StatusQueued
		task.Operator = Operator
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

// Get returns one task.
func (s *MySQL) Get(ctx context.Context, id uint64) (Task, error) {
	var row releaseRow
	err := s.db.WithContext(ctx).First(&row, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	return row.task(), nil
}

// List returns one service's newest tasks.
func (s *MySQL) List(ctx context.Context, service string, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []releaseRow
	err := s.db.WithContext(ctx).Where("service = ?", service).Order("id DESC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Task, len(rows))
	for i, row := range rows {
		out[i] = row.task()
	}
	return out, nil
}

// LatestSuccess returns the newest succeeded image for the service.
func (s *MySQL) LatestSuccess(ctx context.Context, service string) (Task, bool, error) {
	var row releaseRow
	err := s.db.WithContext(ctx).
		Where("service = ? AND status = ? AND image_id <> ''", service, StatusSucceeded).
		Order("id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	return row.task(), true, nil
}

// MarkRunning moves an open task to running.
func (s *MySQL) MarkRunning(ctx context.Context, id uint64) (bool, error) {
	res := s.db.WithContext(ctx).Model(&releaseRow{}).
		Where("id = ? AND status IN ?", id, []string{StatusQueued, StatusRunning}).
		Update("status", StatusRunning)
	return res.RowsAffected > 0, res.Error
}

// SetImage records an image id on an open task.
func (s *MySQL) SetImage(ctx context.Context, id uint64, imageID string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&releaseRow{}).
		Where("id = ? AND status IN ?", id, []string{StatusQueued, StatusRunning}).
		Update("image_id", imageID)
	return res.RowsAffected > 0, res.Error
}

// Finish closes an open task and keeps a previously stored image when imageID is empty.
func (s *MySQL) Finish(ctx context.Context, id uint64, status, imageID, logText, errText string, now time.Time) (bool, error) {
	values := map[string]any{
		"status":      status,
		"log":         Tail(logText),
		"error":       errText,
		"finished_at": now,
	}
	if imageID != "" {
		values["image_id"] = imageID
	}
	res := s.db.WithContext(ctx).Model(&releaseRow{}).
		Where("id = ? AND status IN ?", id, []string{StatusQueued, StatusRunning}).
		Updates(values)
	return res.RowsAffected > 0, res.Error
}

func (row releaseRow) task() Task {
	return Task{
		ID: row.ID, Service: row.Service, Kind: row.Kind, GitRef: row.GitRef, ImageID: row.ImageID,
		Status: row.Status, Log: row.Log, Error: row.Error, Operator: row.Operator,
		CreatedAt: row.CreatedAt, FinishedAt: row.FinishedAt,
	}
}
