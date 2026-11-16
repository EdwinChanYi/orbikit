package release

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// Operator is the only platform operator recorded on a task.
	Operator = "admin"
	// Timeout is how long a queued or running task may stay open.
	Timeout = 30 * time.Minute
)

var (
	// ErrBusy means another task is still queued or running.
	ErrBusy = errors.New("已有发布在进行")
	// ErrNotFound means the task id does not exist.
	ErrNotFound = errors.New("发布记录不存在")
)

// Task is one publish, rollback, or recreate.
type Task struct {
	ID         uint64     `json:"id"`
	Service    string     `json:"service"`
	Kind       string     `json:"kind"`
	GitRef     string     `json:"git_ref"`
	ImageID    string     `json:"image_id"`
	Status     string     `json:"status"`
	Log        string     `json:"log"`
	Error      string     `json:"error"`
	Operator   string     `json:"operator"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Store is the release task table.
type Store interface {
	Expire(ctx context.Context, now time.Time) error
	Active(ctx context.Context) (bool, error)
	Create(ctx context.Context, task Task) (Task, error)
	Get(ctx context.Context, id uint64) (Task, error)
	List(ctx context.Context, service string, limit int) ([]Task, error)
	LatestSuccess(ctx context.Context, service string) (Task, bool, error)
	MarkRunning(ctx context.Context, id uint64) (bool, error)
	SetImage(ctx context.Context, id uint64, imageID string) (bool, error)
	Finish(ctx context.Context, id uint64, status, imageID, logText, errText string, now time.Time) (bool, error)
}

// Memory is a process-local Store for tests and the handler's fake.
type Memory struct {
	mu    sync.Mutex
	next  uint64
	tasks []Task
}

// NewMemory returns an empty task table.
func NewMemory() *Memory {
	return &Memory{}
}

// Seed inserts a task directly, including one that is already terminal.
func (m *Memory) Seed(task Task) Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	task.ID = m.next
	if task.Operator == "" {
		task.Operator = Operator
	}
	if task.Status == "" {
		task.Status = StatusQueued
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	m.tasks = append(m.tasks, task)
	return task
}

// Expire marks tasks older than Timeout as failed.
func (m *Memory) Expire(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tasks {
		row := &m.tasks[i]
		if !active(row.Status) || now.Sub(row.CreatedAt) <= Timeout {
			continue
		}
		row.Status = StatusFailed
		row.Error = "超时"
		finished := now
		row.FinishedAt = &finished
	}
	return nil
}

// Active reports whether a task is queued or running.
func (m *Memory) Active(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.tasks {
		if active(row.Status) {
			return true, nil
		}
	}
	return false, nil
}

// Create inserts a queued task when no other task is still open.
func (m *Memory) Create(_ context.Context, task Task) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.tasks {
		if active(row.Status) {
			return Task{}, ErrBusy
		}
	}
	m.next++
	task.ID = m.next
	task.Status = StatusQueued
	task.Operator = Operator
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	m.tasks = append(m.tasks, task)
	return task, nil
}

// Get returns one task.
func (m *Memory) Get(_ context.Context, id uint64) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.find(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	return *row, nil
}

// List returns one service's tasks, newest id first.
func (m *Memory) List(_ context.Context, service string, limit int) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]Task, 0)
	for i := len(m.tasks) - 1; i >= 0; i-- {
		if m.tasks[i].Service != service {
			continue
		}
		out = append(out, m.tasks[i])
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// LatestSuccess returns the newest succeeded task that recorded an image.
func (m *Memory) LatestSuccess(_ context.Context, service string) (Task, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found Task
	ok := false
	for _, row := range m.tasks {
		if row.Service == service && row.Status == StatusSucceeded && row.ImageID != "" && (!ok || row.ID > found.ID) {
			found = row
			ok = true
		}
	}
	return found, ok, nil
}

// MarkRunning moves a still-open task to running.
func (m *Memory) MarkRunning(_ context.Context, id uint64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.find(id)
	if !ok || !active(row.Status) {
		return false, nil
	}
	row.Status = StatusRunning
	return true, nil
}

// SetImage records an image id while the task is still open.
func (m *Memory) SetImage(_ context.Context, id uint64, imageID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.find(id)
	if !ok || !active(row.Status) {
		return false, nil
	}
	row.ImageID = imageID
	return true, nil
}

// Finish closes a still-open task. A timed-out task stays failed.
func (m *Memory) Finish(_ context.Context, id uint64, status, imageID, logText, errText string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.find(id)
	if !ok || !active(row.Status) {
		return false, nil
	}
	row.Status = status
	if imageID != "" {
		row.ImageID = imageID
	}
	row.Log = Tail(logText)
	row.Error = errText
	finished := now
	row.FinishedAt = &finished
	return true, nil
}

func (m *Memory) find(id uint64) (*Task, bool) {
	for i := range m.tasks {
		if m.tasks[i].ID == id {
			return &m.tasks[i], true
		}
	}
	return nil, false
}

func active(status string) bool {
	return status == StatusQueued || status == StatusRunning
}
