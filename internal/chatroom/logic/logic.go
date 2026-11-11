// Package logic implements the chatroom business rules on top of MySQL,
// Redis and Kafka.
package logic

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/event"
)

// Settings are the tunables of the logic layer.
type Settings struct {
	// EnterTokenTTL is how long an enter token and the staged member info live.
	EnterTokenTTL time.Duration
	// OfflineLeaveAfter is how long a disconnected user stays in rooms.
	OfflineLeaveAfter time.Duration
}

// Deps are the infrastructure clients used by the logic layer.
type Deps struct {
	DB       *gorm.DB
	Redis    redis.UniversalClient
	Core     servicepb.CoreClient
	Events   *event.Producer
	Settings Settings
}

var (
	db       *gorm.DB
	rdb      redis.UniversalClient
	core     servicepb.CoreClient
	events   *event.Producer
	settings Settings
)

// Init must be called once before any other function of this package.
func Init(d Deps) {
	db = d.DB
	rdb = d.Redis
	core = d.Core
	events = d.Events
	settings = d.Settings
}

func isNil(err error) bool { return errors.Is(err, redis.Nil) }

// first loads at most one row into dest and reports whether one was found.
func first(q *gorm.DB, dest any) (bool, error) {
	res := q.Limit(1).Find(dest)
	return res.RowsAffected > 0, res.Error
}

func exists(q *gorm.DB, m any) (bool, error) {
	var n int64
	err := q.Model(m).Count(&n).Error
	return n > 0, err
}

// queryStrings runs a raw query and returns every row as column -> text.
func queryStrings(q *gorm.DB, query string, args ...any) ([]map[string]string, error) {
	rows, err := q.Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]string, len(cols))
		for i, c := range cols {
			row[c] = string(vals[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// bg returns a context for fire-and-forget work that must outlive a request.
func bg() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func parseBool(s string) bool {
	switch s {
	case "0", "false", "FALSE", "False":
		return false
	}
	return true
}

func nowMillis() int64 { return time.Now().UnixNano() / int64(time.Millisecond) }
