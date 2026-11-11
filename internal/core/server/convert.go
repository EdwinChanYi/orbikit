package server

import (
	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/core/store"
)

func appToPB(a *store.AppInfo) *servicepb.AppInfo {
	return &servicepb.AppInfo{
		Id:             a.Id,
		AppId:          a.AppId,
		AppSecret:      a.AppSecret,
		Name:           a.Name,
		Desc:           a.Desc,
		MsgCpUrl:       a.MsgCpUrl,
		TextCpUrl:      a.TextCpUrl,
		CustomCpUrl:    a.CustomCpUrl,
		Types:          a.Types,
		Qps:            a.Qps,
		TextQps:        a.TextQps,
		CustomQps:      a.CustomQps,
		MediaAppId:     a.MediaAppId,
		MediaAppSecret: a.MediaAppSecret,
		GdAppId:        a.GdAppId,
	}
}

func appUpdatedRequest(a *store.AppInfo, traceID string) *servicepb.AppInfoUpdatedRequest {
	return &servicepb.AppInfoUpdatedRequest{
		AppId:       a.AppId,
		AppSecret:   a.AppSecret,
		MsgCpUrl:    a.MsgCpUrl,
		TextCpUrl:   a.TextCpUrl,
		CustomCpUrl: a.CustomCpUrl,
		Types:       a.Types,
		Qps:         a.Qps,
		TextQps:     a.TextQps,
		CustomQps:   a.CustomQps,
		MsgTraceId:  traceID,
	}
}

func gdAppToPB(a *store.GdAppInfo) *servicepb.GdAppInfo {
	return &servicepb.GdAppInfo{
		Id:                         a.Id,
		AppId:                      a.AppId,
		AppSecret:                  a.AppSecret,
		Name:                       a.Name,
		Desc:                       a.Desc,
		MsgCpUrl:                   a.MsgCpUrl,
		TextCpUrl:                  a.TextCpUrl,
		CustomCpUrl:                a.CustomCpUrl,
		Types:                      a.Types,
		Qps:                        a.Qps,
		TextQps:                    a.TextQps,
		CustomQps:                  a.CustomQps,
		MediaAppId:                 a.MediaAppId,
		GdAppId:                    a.GdAppId,
		MatchLevel:                 a.MatchLevel,
		NoStrictTypes:              a.NoStrictTypes,
		NeedMatchBase:              a.NeedMatchBase,
		MarkStar:                   a.MarkStar,
		MarkStar1:                  a.MarkStar1,
		StarTypes1:                 a.StarTypes1,
		MarkStar2:                  a.MarkStar2,
		StarTypes2:                 a.StarTypes2,
		MsgLenMax:                  a.MsgLenMax,
		MsgDigitalCountMax:         a.MsgDigitalCountMax,
		BussinessNeedCheckYidunUrl: a.BussinessNeedCheckYidunUrl,
		TextCountExpire:            a.TextCountExpire,
		DupliTextCount:             a.DupliTextCount,
		IsBlackDupliUser:           a.IsBlackDupliUser,
		IsAddWordStore:             a.IsAddWordStore,
	}
}

func pcAppToPB(a *store.PcAppInfo) *servicepb.PcAppInfo {
	return &servicepb.PcAppInfo{
		Id:          a.Id,
		AppId:       a.AppId,
		AppSecret:   a.AppSecret,
		Name:        a.Name,
		Desc:        a.Desc,
		GdAppId:     a.GdAppId,
		BusinessUrl: a.BusinessUrl,
		ErrPass:     a.ErrPass,
	}
}

// normalizeDupli applies the anti-spam limits: a non-zero duplicate count is
// at least 3 and the counting window is never negative.
func normalizeDupli(dupliTextCount, textCountExpire int32) (int32, int32) {
	if dupliTextCount != 0 && dupliTextCount < 3 {
		dupliTextCount = 3
	}
	if textCountExpire < 0 {
		textCountExpire = 0
	}
	return dupliTextCount, textCountExpire
}
