package logic

import "fmt"

// All core keys share the {CORE_SERVICE} hash tag so the Lua scripts touching
// several of them stay within one Redis Cluster slot.
const (
	infoToLoginTokenFmt = "{CORE_SERVICE}:INFO_2_LOGIN_TOKEN:APP_ID:%s:UID:%d"
	loginTokenToInfoFmt = "{CORE_SERVICE}:LOGIN_TOKEN_2_INFO:LOGIN_TOKEN:%s"
	restrainUserFmt     = "{CORE_SERVICE}:RESTRAIN_USER:APP_ID:%s:UID:%d"
)

// LoginTokenKey maps (appID, uid) to the user's current login token.
func LoginTokenKey(appID string, uid int64) string {
	return fmt.Sprintf(infoToLoginTokenFmt, appID, uid)
}

// LoginTokenInfoKey maps a login token to "appID,uid".
func LoginTokenInfoKey(token string) string {
	return fmt.Sprintf(loginTokenToInfoFmt, token)
}

// RestrainUserKey exists while the user is restrained.
func RestrainUserKey(appID string, uid int64) string {
	return fmt.Sprintf(restrainUserFmt, appID, uid)
}
