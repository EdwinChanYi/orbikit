package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/EdwinChanYi/orbikit/internal/core/randstr"
)

const tableOptions = "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"

// Store wraps the core database.
type Store struct {
	db *gorm.DB
}

// New returns a Store backed by db.
func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

// AutoMigrate creates or updates all core tables.
func (s *Store) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).
		Set("gorm:table_options", tableOptions).
		AutoMigrate(&RestrainUser{}, &AppInfo{}, &GdAppInfo{}, &PcAppInfo{})
}

// takeOne loads a single row into out. It returns (false, nil) when no row matches.
func (s *Store) takeOne(ctx context.Context, out any, query string, args ...any) (bool, error) {
	err := s.db.WithContext(ctx).Where(query, args...).Take(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) updateByID(ctx context.Context, model any, id uint64, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(model).Where("id = ?", id).Updates(cols).Error
}

// ---------------------------------------------------------------------------
// 聊天室应用

// CreateAppParams holds the caller supplied fields of a new chatroom app.
type CreateAppParams struct {
	Name           string
	Desc           string
	MsgCpURL       string
	Types          string
	MediaAppID     string
	MediaAppSecret string
	GdAppID        string
	QPS            int64
}

// CreateApp inserts a chatroom app with freshly generated credentials. The
// message copy URL and QPS apply to every message category initially; zero
// QPS falls back to the column default.
func (s *Store) CreateApp(ctx context.Context, p CreateAppParams) (*AppInfo, error) {
	app := &AppInfo{
		AppId:          randstr.String(32, randstr.Alnum),
		AppSecret:      randstr.String(64, randstr.Alnum),
		Name:           p.Name,
		Desc:           p.Desc,
		MsgCpUrl:       p.MsgCpURL,
		TextCpUrl:      p.MsgCpURL,
		CustomCpUrl:    p.MsgCpURL,
		Types:          p.Types,
		Qps:            p.QPS,
		TextQps:        p.QPS,
		CustomQps:      p.QPS,
		MediaAppId:     p.MediaAppID,
		MediaAppSecret: p.MediaAppSecret,
		GdAppId:        p.GdAppID,
	}
	if err := s.db.WithContext(ctx).Create(app).Error; err != nil {
		return nil, err
	}
	return app, nil
}

// HasApp reports whether a chatroom app with appID exists.
func (s *Store) HasApp(ctx context.Context, appID string) (bool, error) {
	return s.takeOne(ctx, &AppInfo{}, "app_id = ?", appID)
}

// ListApps returns all chatroom apps.
func (s *Store) ListApps(ctx context.Context) ([]*AppInfo, error) {
	var apps []*AppInfo
	if err := s.db.WithContext(ctx).Find(&apps).Error; err != nil {
		return nil, err
	}
	return apps, nil
}

// AppByAppID returns the app or nil when it does not exist.
func (s *Store) AppByAppID(ctx context.Context, appID string) (*AppInfo, error) {
	app := &AppInfo{}
	if ok, err := s.takeOne(ctx, app, "app_id = ?", appID); !ok {
		return nil, err
	}
	return app, nil
}

// AppByID returns the app or nil when it does not exist.
func (s *Store) AppByID(ctx context.Context, id uint64) (*AppInfo, error) {
	app := &AppInfo{}
	if ok, err := s.takeOne(ctx, app, "id = ?", id); !ok {
		return nil, err
	}
	return app, nil
}

// UpdateAppParams holds the editable fields of a chatroom app.
type UpdateAppParams struct {
	RefreshSecret  bool
	Name           string
	Desc           string
	MsgCpURL       string
	TextCpURL      string
	CustomCpURL    string
	Types          string
	MediaAppID     string
	MediaAppSecret string
	GdAppID        string
	QPS            int64
	TextQPS        int64
	CustomQPS      int64
}

// appUpdateColumns builds the column set for UpdateApp. URLs, names and media
// fields are always overwritten; QPS limits only change when QPS > 0, in which
// case zero text/custom limits inherit QPS; types only change when non-empty.
func appUpdateColumns(p UpdateAppParams) map[string]any {
	cols := map[string]any{
		"name":             p.Name,
		"desc":             p.Desc,
		"msg_cp_url":       p.MsgCpURL,
		"text_cp_url":      p.TextCpURL,
		"custom_cp_url":    p.CustomCpURL,
		"media_app_id":     p.MediaAppID,
		"media_app_secret": p.MediaAppSecret,
		"gd_app_id":        p.GdAppID,
	}
	if p.RefreshSecret {
		cols["app_secret"] = randstr.String(64, randstr.Alnum)
	}
	if p.QPS > 0 {
		textQPS, customQPS := p.TextQPS, p.CustomQPS
		if textQPS == 0 {
			textQPS = p.QPS
		}
		if customQPS == 0 {
			customQPS = p.QPS
		}
		cols["qps"] = p.QPS
		cols["text_qps"] = textQPS
		cols["custom_qps"] = customQPS
	}
	if p.Types != "" {
		cols["types"] = p.Types
	}
	return cols
}

// UpdateApp updates the chatroom app with primary key id.
func (s *Store) UpdateApp(ctx context.Context, id uint64, p UpdateAppParams) error {
	return s.updateByID(ctx, &AppInfo{}, id, appUpdateColumns(p))
}

// ---------------------------------------------------------------------------
// 反垃圾应用配置

// GdAppParams holds the anti-spam configuration fields shared by create and update.
type GdAppParams struct {
	Name                       string
	Desc                       string
	Types                      string
	GdAppID                    string
	NoStrictTypes              string
	StarTypes1                 string
	StarTypes2                 string
	BusinessModerationCheckUrl string
	MatchLevel                 uint32
	MsgLenMax                  int32
	MsgDigitalCountMax         int32
	DupliTextCount             int32
	TextCountExpire            int32
	NeedMatchBase              bool
	MarkStar                   bool
	MarkStar1                  bool
	MarkStar2                  bool
	IsBlackDupliUser           bool
	IsAddWordStore             bool
}

// CreateGdApp inserts an anti-spam app config with generated credentials.
// Zero values of columns that declare a default are stored as that default.
func (s *Store) CreateGdApp(ctx context.Context, p GdAppParams) (*GdAppInfo, error) {
	app := &GdAppInfo{
		AppId:                      randstr.String(32, randstr.LowerAlnum),
		AppSecret:                  randstr.String(64, randstr.Alnum),
		Name:                       p.Name,
		Desc:                       p.Desc,
		GdAppId:                    p.GdAppID,
		MatchLevel:                 p.MatchLevel,
		NoStrictTypes:              p.NoStrictTypes,
		NeedMatchBase:              p.NeedMatchBase,
		MarkStar:                   p.MarkStar,
		MarkStar1:                  p.MarkStar1,
		StarTypes1:                 p.StarTypes1,
		MarkStar2:                  p.MarkStar2,
		StarTypes2:                 p.StarTypes2,
		MsgLenMax:                  p.MsgLenMax,
		MsgDigitalCountMax:         p.MsgDigitalCountMax,
		BusinessModerationCheckUrl: p.BusinessModerationCheckUrl,
		DupliTextCount:             p.DupliTextCount,
		TextCountExpire:            p.TextCountExpire,
		IsBlackDupliUser:           p.IsBlackDupliUser,
		IsAddWordStore:             p.IsAddWordStore,
	}
	if err := s.db.WithContext(ctx).Create(app).Error; err != nil {
		return nil, err
	}
	return app, nil
}

// gdAppUpdateColumns builds the column set for UpdateGdApp. Name, desc and
// types are only changed when non-empty; every other field is overwritten.
func gdAppUpdateColumns(p GdAppParams) map[string]any {
	cols := map[string]any{
		"gd_app_id":                     p.GdAppID,
		"match_level":                   p.MatchLevel,
		"need_match_base":               p.NeedMatchBase,
		"no_strict_types":               p.NoStrictTypes,
		"mark_star":                     p.MarkStar,
		"mark_star1":                    p.MarkStar1,
		"star_types1":                   p.StarTypes1,
		"mark_star2":                    p.MarkStar2,
		"star_types2":                   p.StarTypes2,
		"msg_len_max":                   p.MsgLenMax,
		"msg_digital_count_max":         p.MsgDigitalCountMax,
		"business_moderation_check_url": p.BusinessModerationCheckUrl,
		"dupli_text_count":              p.DupliTextCount,
		"text_count_expire":             p.TextCountExpire,
		"is_black_dupli_user":           p.IsBlackDupliUser,
		"is_add_word_store":             p.IsAddWordStore,
	}
	if p.Name != "" {
		cols["name"] = p.Name
	}
	if p.Desc != "" {
		cols["desc"] = p.Desc
	}
	if p.Types != "" {
		cols["types"] = p.Types
	}
	return cols
}

// UpdateGdApp updates the anti-spam app config with primary key id.
func (s *Store) UpdateGdApp(ctx context.Context, id uint64, p GdAppParams) error {
	return s.updateByID(ctx, &GdAppInfo{}, id, gdAppUpdateColumns(p))
}

// GdAppByID returns the anti-spam app config or nil when it does not exist.
func (s *Store) GdAppByID(ctx context.Context, id uint64) (*GdAppInfo, error) {
	app := &GdAppInfo{}
	if ok, err := s.takeOne(ctx, app, "id = ?", id); !ok {
		return nil, err
	}
	return app, nil
}

// GdAppByAppID returns the anti-spam app config or nil when it does not exist.
func (s *Store) GdAppByAppID(ctx context.Context, appID string) (*GdAppInfo, error) {
	app := &GdAppInfo{}
	if ok, err := s.takeOne(ctx, app, "app_id = ?", appID); !ok {
		return nil, err
	}
	return app, nil
}

// ---------------------------------------------------------------------------
// 私聊应用

// CreatePcAppParams holds the fields of a new private chat app. When both
// AppKey and AppSecret are given they are used as the credentials instead of
// generated ones.
type CreatePcAppParams struct {
	Name        string
	Desc        string
	GdAppID     string
	AppKey      string
	AppSecret   string
	BusinessURL string
	ErrPass     bool
}

// CreatePcApp inserts a private chat app.
func (s *Store) CreatePcApp(ctx context.Context, p CreatePcAppParams) (*PcAppInfo, error) {
	appID, secret := p.AppKey, p.AppSecret
	if appID == "" || secret == "" {
		appID = randstr.String(32, randstr.Alnum)
		secret = randstr.String(64, randstr.Alnum)
	}
	app := &PcAppInfo{
		AppId:       appID,
		AppSecret:   secret,
		Name:        p.Name,
		Desc:        p.Desc,
		GdAppId:     p.GdAppID,
		BusinessUrl: p.BusinessURL,
		ErrPass:     p.ErrPass,
	}
	if err := s.db.WithContext(ctx).Create(app).Error; err != nil {
		return nil, err
	}
	return app, nil
}

// UpdatePcAppParams holds the editable fields of a private chat app.
type UpdatePcAppParams struct {
	RefreshSecret bool
	ErrPass       bool
	Name          string
	Desc          string
	BusinessURL   string
}

// UpdatePcApp updates the private chat app with primary key id.
func (s *Store) UpdatePcApp(ctx context.Context, id uint64, p UpdatePcAppParams) error {
	cols := map[string]any{
		"name":         p.Name,
		"desc":         p.Desc,
		"business_url": p.BusinessURL,
		"err_pass":     p.ErrPass,
	}
	if p.RefreshSecret {
		cols["app_secret"] = randstr.String(64, randstr.Alnum)
	}
	return s.updateByID(ctx, &PcAppInfo{}, id, cols)
}

// BindPcFilterApp binds an anti-spam app to the private chat app with primary key id.
func (s *Store) BindPcFilterApp(ctx context.Context, id uint64, gdAppID string) error {
	return s.updateByID(ctx, &PcAppInfo{}, id, map[string]any{"gd_app_id": gdAppID})
}

// PcAppByID returns the private chat app or nil when it does not exist.
func (s *Store) PcAppByID(ctx context.Context, id uint64) (*PcAppInfo, error) {
	app := &PcAppInfo{}
	if ok, err := s.takeOne(ctx, app, "id = ?", id); !ok {
		return nil, err
	}
	return app, nil
}

// PcAppByAppID returns the private chat app or nil when it does not exist.
func (s *Store) PcAppByAppID(ctx context.Context, appID string) (*PcAppInfo, error) {
	app := &PcAppInfo{}
	if ok, err := s.takeOne(ctx, app, "app_id = ?", appID); !ok {
		return nil, err
	}
	return app, nil
}

// ---------------------------------------------------------------------------
// 封禁用户

// AddRestrainUser records a restrained user; it is a no-op when already recorded.
func (s *Store) AddRestrainUser(ctx context.Context, appID string, uid int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&RestrainUser{}).
			Where("app_id = ? AND uid = ?", appID, uid).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		return tx.Create(&RestrainUser{AppId: appID, Uid: uid, IsRestrain: true}).Error
	})
}

// DelRestrainUser removes a restrained user record.
func (s *Store) DelRestrainUser(ctx context.Context, appID string, uid int64) error {
	return s.db.WithContext(ctx).
		Where("app_id = ? AND uid = ?", appID, uid).
		Delete(&RestrainUser{}).Error
}
