package logic

import (
	"encoding/json"

	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

// SetAppCustomInfoKeyFilter replaces the app-wide custom info key list.
// An empty list means member reads return no custom info keys.
func SetAppCustomInfoKeyFilter(ctx *reqctx.Context, appID string, keys []string) error {
	if keys == nil {
		keys = []string{}
	}
	body, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, rediskey.AppCustomInfoKeyFilter(appID), body, 0).Err()
}

func appCustomInfoKeyFilter(ctx *reqctx.Context, appID string) ([]string, bool, error) {
	data, err := rdb.Get(ctx, rediskey.AppCustomInfoKeyFilter(appID)).Result()
	if isNil(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var keys []string
	if err := json.Unmarshal([]byte(data), &keys); err != nil {
		return nil, false, err
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, true, nil
}

// FilterCustomInfo keeps only keys when a filter is configured.
// A missing filter leaves info unchanged.
func FilterCustomInfo(info map[string]string, keys []string, configured bool) map[string]string {
	if !configured {
		return info
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := info[key]; ok {
			out[key] = value
		}
	}
	return out
}
