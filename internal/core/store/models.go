// Package store persists core service data in MySQL.
package store

import "time"

// AppInfo 聊天室应用信息表
type AppInfo struct {
	Id             uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	AppId          string    `gorm:"column:app_id;type:varchar(64);not null;default:'';uniqueIndex:uix_app_infos_app_id"`
	AppSecret      string    `gorm:"column:app_secret;type:varchar(128);not null;default:''"`
	Name           string    `gorm:"column:name;type:varchar(255);not null;default:''"`
	MsgCpUrl       string    `gorm:"column:msg_cp_url;type:varchar(255);not null;default:''"`
	TextCpUrl      string    `gorm:"column:text_cp_url;type:varchar(255);not null;default:''"`
	CustomCpUrl    string    `gorm:"column:custom_cp_url;type:varchar(255);not null;default:''"`
	Desc           string    `gorm:"column:desc;type:varchar(255);not null;default:''"`
	Types          string    `gorm:"column:types;type:varchar(255);not null;default:''"`
	Qps            int64     `gorm:"column:qps;type:bigint;not null;default:1000"`
	TextQps        int64     `gorm:"column:text_qps;type:bigint;not null;default:1000"`
	CustomQps      int64     `gorm:"column:custom_qps;type:bigint;not null;default:1000"`
	MediaAppId     string    `gorm:"column:media_app_id;type:varchar(64);not null;default:''"`
	MediaAppSecret string    `gorm:"column:media_app_secret;type:varchar(128);not null;default:''"`
	GdAppId        string    `gorm:"column:gd_app_id;type:varchar(64);not null;default:''"`
	CreatedAt      time.Time `gorm:"column:created_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt      time.Time `gorm:"column:updated_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
}

func (AppInfo) TableName() string { return "app_infos" }

// GdAppInfo 反垃圾应用信息表（仅保存配置）
type GdAppInfo struct {
	Id                         uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	AppId                      string    `gorm:"column:app_id;type:varchar(64);not null;default:''"`
	AppSecret                  string    `gorm:"column:app_secret;type:varchar(128);not null;default:''"`
	Name                       string    `gorm:"column:name;type:varchar(255);not null;default:''"`
	MsgCpUrl                   string    `gorm:"column:msg_cp_url;type:varchar(255);not null;default:''"`
	TextCpUrl                  string    `gorm:"column:text_cp_url;type:varchar(255);not null;default:''"`
	CustomCpUrl                string    `gorm:"column:custom_cp_url;type:varchar(255);not null;default:''"`
	Desc                       string    `gorm:"column:desc;type:varchar(255);not null;default:''"`
	Types                      string    `gorm:"column:types;type:varchar(255);not null;default:''"`
	Qps                        int64     `gorm:"column:qps;type:bigint;not null;default:1000"`
	TextQps                    int64     `gorm:"column:text_qps;type:bigint;not null;default:1000"`
	CustomQps                  int64     `gorm:"column:custom_qps;type:bigint;not null;default:1000"`
	MatchLevel                 uint32    `gorm:"column:match_level;type:int(10) unsigned;not null;default:0"`
	NoStrictTypes              string    `gorm:"column:no_strict_types;type:varchar(255);not null;default:''"`
	NeedMatchBase              bool      `gorm:"column:need_match_base;type:tinyint(1);default:1"`
	MarkStar                   bool      `gorm:"column:mark_star;type:tinyint(1);default:1"`
	MarkStar1                  bool      `gorm:"column:mark_star1;type:tinyint(1);default:1"`
	StarTypes1                 string    `gorm:"column:star_types1;type:varchar(255);not null;default:''"`
	MarkStar2                  bool      `gorm:"column:mark_star2;type:tinyint(1);default:1"`
	StarTypes2                 string    `gorm:"column:star_types2;type:varchar(255);not null;default:''"`
	MsgLenMax                  int32     `gorm:"column:msg_len_max;type:int(10);not null;default:0"`
	MsgDigitalCountMax         int32     `gorm:"column:msg_digital_count_max;type:int(10);not null;default:6"`
	BussinessNeedCheckYidunUrl string    `gorm:"column:bussiness_need_check_yidun_url;type:varchar(255);not null;default:''"`
	MediaAppId                 string    `gorm:"column:media_app_id;type:varchar(64);not null;default:''"`
	GdAppId                    string    `gorm:"column:gd_app_id;type:varchar(64);not null;default:''"`
	TextCountExpire            int32     `gorm:"column:text_count_expire;type:int(10) unsigned;not null;default:0"`
	DupliTextCount             int32     `gorm:"column:dupli_text_count;type:int(10) unsigned;not null;default:0"`
	IsBlackDupliUser           bool      `gorm:"column:is_black_dupli_user;type:tinyint(1);default:0"`
	IsAddWordStore             bool      `gorm:"column:is_add_word_store;type:tinyint(1);default:0"`
	CreatedAt                  time.Time `gorm:"column:created_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt                  time.Time `gorm:"column:updated_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
}

func (GdAppInfo) TableName() string { return "gd_app_infos" }

// PcAppInfo 私聊应用信息表
type PcAppInfo struct {
	Id          uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	AppId       string    `gorm:"column:app_id;type:varchar(64);not null;default:''"`
	AppSecret   string    `gorm:"column:app_secret;type:varchar(128);not null;default:''"`
	Name        string    `gorm:"column:name;type:varchar(255);not null;default:''"`
	Desc        string    `gorm:"column:desc;type:varchar(255);not null;default:''"`
	GdAppId     string    `gorm:"column:gd_app_id;type:varchar(64);not null;default:''"`
	BusinessUrl string    `gorm:"column:business_url;type:varchar(1024);not null;default:''"`
	ErrPass     bool      `gorm:"column:err_pass;type:tinyint;not null;default:1"`
	CreatedAt   time.Time `gorm:"column:created_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time `gorm:"column:updated_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
}

func (PcAppInfo) TableName() string { return "pc_app_infos" }

// RestrainUser 封禁用户表
type RestrainUser struct {
	Id         uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	AppId      string    `gorm:"column:app_id;type:varchar(64);not null;uniqueIndex:appid_uid_uidx"`
	Uid        int64     `gorm:"column:uid;not null;uniqueIndex:appid_uid_uidx"`
	IsRestrain bool      `gorm:"column:is_restrain;not null"`
	CreateAt   time.Time `gorm:"column:create_at;type:datetime;not null;default:CURRENT_TIMESTAMP"`
}

func (RestrainUser) TableName() string { return "restrain_users" }
