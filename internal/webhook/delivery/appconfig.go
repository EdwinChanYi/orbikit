// Package delivery turns chatroom broadcasts into signed HTTP callbacks to the
// URLs each app registered in the core service.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

// ErrAppNotFound is returned by an AppSource when the app is not registered.
var ErrAppNotFound = errors.New("app not found")

// AppConfig is an app's message copy configuration.
type AppConfig struct {
	Secret      string
	MsgCpURL    string // 房间事件及其他类型消息
	TextCpURL   string // 文本消息
	CustomCpURL string // 自定义消息
	Types       []int32
	QPS         int64 // 房间事件 QPS 限制
	TextQPS     int64
	CustomQPS   int64
}

// ParseTypes parses the comma separated ChatroomMsgType list, skipping invalid items.
func ParseTypes(s string) []int32 {
	var types []int32
	for _, item := range strings.Split(s, ",") {
		if t, err := strconv.Atoi(strings.TrimSpace(item)); err == nil {
			types = append(types, int32(t))
		}
	}
	return types
}

// Enabled reports whether messages of type t should be copied.
func (c *AppConfig) Enabled(t servicepb.ChatroomMsgType) bool {
	for _, v := range c.Types {
		if v == int32(servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_ALL) || v == int32(t) {
			return true
		}
	}
	return false
}

// Route returns the callback URL and QPS limit for messages of type t.
// QPS <= 0 means unlimited.
func (c *AppConfig) Route(t servicepb.ChatroomMsgType) (url string, qps int64) {
	switch t {
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT:
		return c.TextCpURL, c.TextQPS
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM:
		return c.CustomCpURL, c.CustomQPS
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_EVENT:
		return c.MsgCpURL, c.QPS
	default:
		return c.MsgCpURL, 0
	}
}

// AppSource loads app configuration from the core service.
type AppSource interface {
	AppConfig(ctx context.Context, appID string) (*AppConfig, error)
}

// CoreSource implements AppSource with Core.GetAppSecret.
type CoreSource struct {
	Client  servicepb.CoreClient
	Timeout time.Duration
}

// AppConfig implements AppSource.
func (s *CoreSource) AppConfig(ctx context.Context, appID string) (*AppConfig, error) {
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	r, err := s.Client.GetAppSecret(ctx, &servicepb.GetAppSecretRequest{
		AppId:      appID,
		MsgTraceId: fmt.Sprintf("%016d", rand.Int64N(1e16)),
	})
	if err != nil {
		return nil, fmt.Errorf("core GetAppSecret: %w", err)
	}
	switch code := r.GetRet().GetErrorCode(); code {
	case servicepb.ErrorCode_SUCCESS:
	case servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS:
		return nil, ErrAppNotFound
	default:
		return nil, fmt.Errorf("core GetAppSecret: %s %s", code, r.GetRet().GetErrorMsg())
	}
	return &AppConfig{
		Secret:      r.GetSecret(),
		MsgCpURL:    r.GetMsgCpUrl(),
		TextCpURL:   r.GetTextCpUrl(),
		CustomCpURL: r.GetCustomCpUrl(),
		Types:       ParseTypes(r.GetTypes()),
		QPS:         r.GetQps(),
		TextQPS:     r.GetTextQps(),
		CustomQPS:   r.GetCustomQps(),
	}, nil
}

const appCacheStripes = 64

// AppCache caches app configuration. Found apps are kept for ttl, unknown apps
// for negativeTTL; transient lookup errors are not cached.
type AppCache struct {
	src         AppSource
	ttl         time.Duration
	negativeTTL time.Duration
	now         func() time.Time

	mu      sync.RWMutex
	entries map[string]appEntry
	locks   [appCacheStripes]sync.Mutex
}

type appEntry struct {
	cfg      *AppConfig
	expireAt time.Time
}

// NewAppCache returns an AppCache backed by src.
func NewAppCache(src AppSource, ttl, negativeTTL time.Duration) *AppCache {
	return &AppCache{
		src:         src,
		ttl:         ttl,
		negativeTTL: negativeTTL,
		now:         time.Now,
		entries:     make(map[string]appEntry),
	}
}

func (c *AppCache) lookup(appID string) (*AppConfig, bool) {
	c.mu.RLock()
	e, ok := c.entries[appID]
	c.mu.RUnlock()
	if !ok || !c.now().Before(e.expireAt) {
		return nil, false
	}
	return e.cfg, true
}

func (c *AppCache) store(appID string, cfg *AppConfig, ttl time.Duration) {
	c.mu.Lock()
	c.entries[appID] = appEntry{cfg: cfg, expireAt: c.now().Add(ttl)}
	c.mu.Unlock()
}

// Get returns the app's configuration, or nil when the app is not registered.
func (c *AppCache) Get(ctx context.Context, appID string) (*AppConfig, error) {
	if cfg, ok := c.lookup(appID); ok {
		return cfg, nil
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(appID))
	l := &c.locks[h.Sum32()%appCacheStripes]
	l.Lock()
	defer l.Unlock()
	if cfg, ok := c.lookup(appID); ok {
		return cfg, nil
	}

	cfg, err := c.src.AppConfig(ctx, appID)
	switch {
	case errors.Is(err, ErrAppNotFound):
		c.store(appID, nil, c.negativeTTL)
		return nil, nil
	case err != nil:
		return nil, err
	}
	c.store(appID, cfg, c.ttl)
	return cfg, nil
}

// Put replaces the cached configuration, e.g. after an AppInfoUpdated push.
func (c *AppCache) Put(appID string, cfg *AppConfig) {
	c.store(appID, cfg, c.ttl)
}
