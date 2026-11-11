// Package logic implements core service business rules on top of the store and Redis.
package logic

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/core/store"
	"github.com/yourname/roomkit/pkg/log"
)

const (
	// failureTTL is how long a lookup failure or a missing app is remembered
	// before the database is queried again.
	failureTTL = time.Minute
	// loadTimeout bounds a cache fill query.
	loadTimeout = 5 * time.Second
)

// loadContext detaches a cache fill from the caller's cancellation: the
// result is shared with other callers and a failure is cached.
func loadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
}

// AppStore is the persistence used by AppService.
type AppStore interface {
	HasApp(ctx context.Context, appID string) (bool, error)
	ListApps(ctx context.Context) ([]*store.AppInfo, error)
	AppByAppID(ctx context.Context, appID string) (*store.AppInfo, error)
	GdAppByAppID(ctx context.Context, appID string) (*store.GdAppInfo, error)
	PcAppByAppID(ctx context.Context, appID string) (*store.PcAppInfo, error)
}

// AppService serves cached app lookups by app id. Cached entries expire after
// the configured TTL so that changes made by other instances become visible.
type AppService struct {
	store  AppStore
	ttl    time.Duration
	valid  *ttlCache[bool]
	apps   *ttlCache[*store.AppInfo]
	gdApps *ttlCache[*store.GdAppInfo]
	pcApps *ttlCache[*store.PcAppInfo]
}

// NewAppService returns an AppService caching lookups for ttl.
func NewAppService(st AppStore, ttl time.Duration) *AppService {
	return &AppService{
		store:  st,
		ttl:    ttl,
		valid:  newTTLCache[bool](),
		apps:   newTTLCache[*store.AppInfo](),
		gdApps: newTTLCache[*store.GdAppInfo](),
		pcApps: newTTLCache[*store.PcAppInfo](),
	}
}

// IsAppValid reports whether the chatroom app is registered.
func (s *AppService) IsAppValid(ctx context.Context, appID string) bool {
	return s.valid.GetOrLoad(appID, func() (bool, time.Duration) {
		ctx, cancel := loadContext(ctx)
		defer cancel()
		ok, err := s.store.HasApp(ctx, appID)
		if err != nil {
			log.L().Error("query app existence", zap.String("app_id", appID), zap.Error(err))
			return false, failureTTL
		}
		return ok, s.ttl
	})
}

// App returns the chatroom app or nil when it does not exist or cannot be loaded.
func (s *AppService) App(ctx context.Context, appID string) *store.AppInfo {
	return getOrLoad(ctx, s.apps, appID, s.ttl, "app", s.store.AppByAppID)
}

// GdApp returns the anti-spam app config or nil.
func (s *AppService) GdApp(ctx context.Context, appID string) *store.GdAppInfo {
	return getOrLoad(ctx, s.gdApps, appID, s.ttl, "gd app", s.store.GdAppByAppID)
}

// PcApp returns the private chat app or nil.
func (s *AppService) PcApp(ctx context.Context, appID string) *store.PcAppInfo {
	return getOrLoad(ctx, s.pcApps, appID, s.ttl, "pc app", s.store.PcAppByAppID)
}

func getOrLoad[V any](ctx context.Context, c *ttlCache[*V], appID string, ttl time.Duration,
	kind string, load func(context.Context, string) (*V, error)) *V {
	return c.GetOrLoad(appID, func() (*V, time.Duration) {
		ctx, cancel := loadContext(ctx)
		defer cancel()
		v, err := load(ctx, appID)
		if err != nil {
			log.L().Error("load "+kind, zap.String("app_id", appID), zap.Error(err))
			return nil, failureTTL
		}
		if v == nil {
			log.L().Warn(kind+" not exists", zap.String("app_id", appID))
			return nil, failureTTL
		}
		return v, ttl
	})
}

// AllAppQPS lists the message copy QPS limits of every chatroom app and
// refreshes the app cache with the loaded rows. It returns nil on failure.
func (s *AppService) AllAppQPS(ctx context.Context) []*servicepb.AppMsgCpQPS {
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		log.L().Error("list apps", zap.Error(err))
		return nil
	}
	ret := make([]*servicepb.AppMsgCpQPS, 0, len(apps))
	for _, a := range apps {
		s.apps.Set(a.AppId, a, s.ttl)
		ret = append(ret, &servicepb.AppMsgCpQPS{
			AppId:     a.AppId,
			Qps:       a.Qps,
			TextQps:   a.TextQps,
			CustomQps: a.CustomQps,
		})
	}
	return ret
}

// InvalidateApp drops cached chatroom app data for appID.
func (s *AppService) InvalidateApp(appID string) {
	s.valid.Delete(appID)
	s.apps.Delete(appID)
}

// InvalidateGdApp drops the cached anti-spam app config for appID.
func (s *AppService) InvalidateGdApp(appID string) { s.gdApps.Delete(appID) }

// InvalidatePcApp drops the cached private chat app for appID.
func (s *AppService) InvalidatePcApp(appID string) { s.pcApps.Delete(appID) }
