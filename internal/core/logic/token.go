package logic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/core/randstr"
	"github.com/yourname/roomkit/pkg/log"
)

// KEYS[1] user -> token, KEYS[2] restrain flag; ARGV[1] token, ARGV[2] "appID,uid".
// Returns 0 when the user is restrained, 1 after issuing the token.
var applyLoginTokenScript = redis.NewScript(`
local isRestrainUser = redis.call("EXISTS", KEYS[2])
if isRestrainUser == 1 then
	return 0
end
local oldToken = redis.call("GET", KEYS[1])
if oldToken then
	redis.call("DEL", string.format("{CORE_SERVICE}:LOGIN_TOKEN_2_INFO:LOGIN_TOKEN:%s", oldToken))
end
redis.call("SET", KEYS[1], ARGV[1])
redis.call("SET", string.format("{CORE_SERVICE}:LOGIN_TOKEN_2_INFO:LOGIN_TOKEN:%s", ARGV[1]), ARGV[2])
return 1
`)

// KEYS[1] user -> token; ARGV[1] token to delete.
// Returns true when the user has a token (deleting it only if it matches), false otherwise.
var deleteLoginTokenScript = redis.NewScript(`
local oldToken = redis.call("GET", KEYS[1])
if oldToken then
	if oldToken ~= ARGV[1] then
		return true
	end
	redis.call("DEL", KEYS[1])
	redis.call("DEL", string.format("{CORE_SERVICE}:LOGIN_TOKEN_2_INFO:LOGIN_TOKEN:%s", oldToken))
	return true
end
return false
`)

// TokenService issues and verifies client login tokens.
type TokenService struct {
	rdb  redis.UniversalClient
	apps *AppService
}

// NewTokenService returns a TokenService.
func NewTokenService(rdb redis.UniversalClient, apps *AppService) *TokenService {
	return &TokenService{rdb: rdb, apps: apps}
}

// newLoginToken returns hex(sha256(random128 + "-" + appID + "-" + uid)).
func newLoginToken(appID string, uid int64) string {
	base := fmt.Sprintf("%s-%s-%d", randstr.String(128, randstr.Alnum), appID, uid)
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:])
}

func tokenInfoValue(appID string, uid int64) string {
	return fmt.Sprintf("%s,%d", appID, uid)
}

// parseTokenInfo parses "appID,uid". ok is false when the value is malformed.
func parseTokenInfo(v string) (appID string, uid int64, ok bool, err error) {
	parts := strings.Split(v, ",")
	if len(parts) < 2 {
		return "", 0, false, nil
	}
	uid, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, false, err
	}
	return parts[0], uid, true, nil
}

// Apply issues a new login token, replacing the user's previous one. token is
// empty when the app is not registered; restrained is true when the user is
// banned, in which case no token is stored.
func (s *TokenService) Apply(ctx context.Context, appID string, uid int64) (token string, restrained bool, err error) {
	if !s.apps.IsAppValid(ctx, appID) {
		return "", false, nil
	}
	token = newLoginToken(appID, uid)
	v, err := applyLoginTokenScript.Run(ctx, s.rdb,
		[]string{LoginTokenKey(appID, uid), RestrainUserKey(appID, uid)},
		token, tokenInfoValue(appID, uid)).Int64()
	if err != nil {
		return "", false, err
	}
	return token, v == 0, nil
}

// Info resolves a login token. appID is empty when the token is unknown or malformed.
func (s *TokenService) Info(ctx context.Context, token string) (appID string, uid int64, err error) {
	v, err := s.rdb.Get(ctx, LoginTokenInfoKey(token)).Result()
	if errors.Is(err, redis.Nil) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	appID, uid, ok, err := parseTokenInfo(v)
	if err != nil {
		log.L().Error("parse login token info", zap.String("token", token), zap.String("info", v), zap.Error(err))
		return "", 0, err
	}
	if !ok {
		log.L().Error("malformed login token info", zap.String("token", token), zap.String("info", v))
		return "", 0, nil
	}
	return appID, uid, nil
}

// Delete revokes the user's login token if it equals token.
func (s *TokenService) Delete(ctx context.Context, appID string, uid int64, token string) (servicepb.ErrorCode, error) {
	if !s.apps.IsAppValid(ctx, appID) {
		return servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS, nil
	}
	ok, err := deleteLoginTokenScript.Run(ctx, s.rdb, []string{LoginTokenKey(appID, uid)}, token).Bool()
	if errors.Is(err, redis.Nil) {
		ok, err = false, nil
	}
	if err != nil {
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	if !ok {
		return servicepb.ErrorCode_CORE_LOGIN_TOKEN_NOT_EXISTS, nil
	}
	return servicepb.ErrorCode_SUCCESS, nil
}
