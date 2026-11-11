// Package model holds the chatroom MySQL tables. Table, column and index names
// match the existing schema so AutoMigrate works on deployed databases.
package model

import "gorm.io/gorm"

// ChatroomInfo 聊天室信息。也以 JSON 形式缓存在 Redis，字段名不可随意修改。
type ChatroomInfo struct {
	Id           uint64     `gorm:"primaryKey;autoIncrement"`
	AppId        string     `gorm:"type:varchar(64);not null;uniqueIndex:UQE_chatroom_info_idx_app_room,priority:1"`
	ChatroomId   int64      `gorm:"not null;uniqueIndex:UQE_chatroom_info_idx_app_room,priority:2"`
	Name         string     `gorm:"type:mediumtext;not null"`
	Announcement string     `gorm:"type:mediumtext;not null"`
	OwnerUid     int64      `gorm:"not null;index:IDX_chatroom_info_owner_uid"`
	IsMuted      bool       `gorm:"not null"`
	Filter       StringList `gorm:"type:longtext;not null"`
	Ext          StringMap  `gorm:"type:longtext;not null"`
	IsOpen       bool       `gorm:"not null"`
	CreatedAt    Timestamp  `gorm:"type:char(64);not null"`
	UpdatedAt    Timestamp  `gorm:"type:char(64);not null"`
}

func (ChatroomInfo) TableName() string { return "chatroom_info" }

// ChatroomMemberStatusMemoryList 房间成员状态（在线、禁言、踢出、拉黑）。
type ChatroomMemberStatusMemoryList struct {
	Id             uint64    `gorm:"primaryKey;autoIncrement"`
	AppId          string    `gorm:"type:varchar(64);not null;uniqueIndex:UQE_chatroom_member_status_memory_list_idx_app_room_uid,priority:1;index:IDX_chatroom_member_status_memory_list_idx_app_room_online,priority:1;index:IDX_chatroom_member_status_memory_list_idx_room_app_block,priority:1"`
	ChatroomId     int64     `gorm:"not null;uniqueIndex:UQE_chatroom_member_status_memory_list_idx_app_room_uid,priority:2;index:IDX_chatroom_member_status_memory_list_idx_app_room_online,priority:2;index:IDX_chatroom_member_status_memory_list_idx_room_app_block,priority:2"`
	Uid            int64     `gorm:"not null;uniqueIndex:UQE_chatroom_member_status_memory_list_idx_app_room_uid,priority:3"`
	IsBlock        bool      `gorm:"not null;index:IDX_chatroom_member_status_memory_list_idx_room_app_block,priority:3"`
	IsMuted        bool      `gorm:"not null"`
	IsKicked       bool      `gorm:"not null"`
	MutedBeginAt   Timestamp `gorm:"type:char(64);not null"`
	MutedFinishAt  Timestamp `gorm:"type:char(64);not null"`
	KickedBeginAt  Timestamp `gorm:"type:char(64);not null"`
	KickedFinishAt Timestamp `gorm:"type:char(64);not null"`
	IsOnline       bool      `gorm:"not null;index:IDX_chatroom_member_status_memory_list_idx_app_room_online,priority:3"`
	CreatedAt      Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt      Timestamp `gorm:"type:char(64);not null"`
}

func (ChatroomMemberStatusMemoryList) TableName() string {
	return "chatroom_member_status_memory_list"
}

// ChatroomMemberSearchInfo 成员可搜索的 key/value。
type ChatroomMemberSearchInfo struct {
	Id         uint64    `gorm:"primaryKey;autoIncrement"`
	AppId      string    `gorm:"type:varchar(64);not null;index:IDX_chatroom_member_search_info_idx_app_room_uid,priority:1"`
	ChatroomId int64     `gorm:"not null;index:IDX_chatroom_member_search_info_idx_app_room_uid,priority:2"`
	Uid        int64     `gorm:"not null;index:IDX_chatroom_member_search_info_idx_app_room_uid,priority:3"`
	Nickname   string    `gorm:"type:varchar(255);not null"`
	Key        string    `gorm:"type:varchar(4096);not null"`
	Value      string    `gorm:"type:varchar(4096);not null"`
	CreatedAt  Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt  Timestamp `gorm:"type:char(64);not null"`
}

func (ChatroomMemberSearchInfo) TableName() string { return "chatroom_member_search_info" }

// ChatroomMemberInfo 房间成员资料。也以 JSON 形式缓存在 Redis。
type ChatroomMemberInfo struct {
	Id                uint64    `gorm:"primaryKey;autoIncrement"`
	AppId             string    `gorm:"type:varchar(64);not null;uniqueIndex:UQE_chatroom_member_info_idx_app_room_uid,priority:1;index:IDX_chatroom_member_info_idx_app_room_robot,priority:1"`
	ChatroomId        int64     `gorm:"not null;uniqueIndex:UQE_chatroom_member_info_idx_app_room_uid,priority:2;index:IDX_chatroom_member_info_idx_app_room_robot,priority:2"`
	Uid               int64     `gorm:"not null;uniqueIndex:UQE_chatroom_member_info_idx_app_room_uid,priority:3"`
	Nickname          string    `gorm:"type:mediumtext;not null"`
	Avatar            string    `gorm:"type:mediumtext;not null"`
	CustomInfo        StringMap `gorm:"type:longtext;not null"`
	ChatroomRemoteExt StringMap `gorm:"type:longtext;not null"`
	SearchData        StringMap `gorm:"type:longtext;not null"`
	IsRobot           bool      `gorm:"not null;index:IDX_chatroom_member_info_idx_app_room_robot,priority:3"`
	CreatedAt         Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt         Timestamp `gorm:"type:char(64);not null;index:IDX_chatroom_member_info_updated_at"`
}

func (ChatroomMemberInfo) TableName() string { return "chatroom_member_info" }

// Role 应用内自定义角色。
type Role struct {
	Id           int64     `gorm:"primaryKey;autoIncrement"`
	AppId        string    `gorm:"type:varchar(64);not null;index:IDX_role_app_id;uniqueIndex:UQE_role_idx_app_role,priority:1"`
	RoleId       int64     `gorm:"not null;uniqueIndex:UQE_role_idx_app_role,priority:2"`
	Name         string    `gorm:"type:varchar(255);not null"`
	KeepOnClosed bool      `gorm:"not null;default:0"`
	CreatedAt    Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt    Timestamp `gorm:"type:char(64);not null"`
}

func (Role) TableName() string { return "role" }

// Permission 角色的权限配置。
type Permission struct {
	Id              uint64    `gorm:"primaryKey;autoIncrement"`
	AppId           string    `gorm:"type:varchar(64);not null;index:IDX_permission_idx_app_role,priority:1"`
	RoleId          int64     `gorm:"not null;index:IDX_permission_idx_app_role,priority:2"`
	ActionType      int32     `gorm:"not null;index:IDX_permission_action_type"`
	CanDoAction     bool      `gorm:"not null"`
	InitiativeLevel int32     `gorm:"not null"`
	PassivityLevel  int32     `gorm:"not null"`
	CreatedAt       Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt       Timestamp `gorm:"type:char(64);not null"`
}

func (Permission) TableName() string { return "permission" }

// UserLogicRole 用户在房间内拥有的角色。
type UserLogicRole struct {
	Id         uint64    `gorm:"primaryKey;autoIncrement"`
	AppId      string    `gorm:"type:varchar(64);not null;index:IDX_user_logic_role_idx_app_room_uid,priority:1;uniqueIndex:UQE_user_logic_role_idx_app_room_uid_role,priority:1;index:IDX_user_logic_role_idx_room_app_role,priority:1"`
	Uid        int64     `gorm:"not null;index:IDX_user_logic_role_idx_app_room_uid,priority:2;uniqueIndex:UQE_user_logic_role_idx_app_room_uid_role,priority:2"`
	ChatroomId int64     `gorm:"not null;index:IDX_user_logic_role_idx_app_room_uid,priority:3;uniqueIndex:UQE_user_logic_role_idx_app_room_uid_role,priority:3;index:IDX_user_logic_role_idx_room_app_role,priority:2"`
	RoleId     int64     `gorm:"not null;uniqueIndex:UQE_user_logic_role_idx_app_room_uid_role,priority:4;index:IDX_user_logic_role_idx_room_app_role,priority:3"`
	CreatedAt  Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt  Timestamp `gorm:"type:char(64);not null"`
}

func (UserLogicRole) TableName() string { return "user_logic_role" }

// UserChatroomSpendTimeRecord 用户在房时长（旧统计口径）。
type UserChatroomSpendTimeRecord struct {
	Id         uint64    `gorm:"primaryKey;autoIncrement"`
	AppId      string    `gorm:"type:varchar(64);not null;index:IDX_user_chatroom_spend_time_record_idx_app_room_uid,priority:1"`
	ChatroomId int64     `gorm:"not null;index:IDX_user_chatroom_spend_time_record_idx_app_room_uid,priority:2"`
	Uid        int64     `gorm:"not null;index:IDX_user_chatroom_spend_time_record_idx_app_room_uid,priority:3"`
	EnterTime  Timestamp `gorm:"type:char(64);not null"`
	LeaveTime  Timestamp `gorm:"type:char(64);not null"`
	CreatedAt  Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt  Timestamp `gorm:"type:char(64);not null"`
}

func (UserChatroomSpendTimeRecord) TableName() string { return "user_chatroom_spend_time_record" }

// UserChatroomMicSpendTimeRecord 用户在麦时长（旧统计口径）。
type UserChatroomMicSpendTimeRecord struct {
	Id         uint64    `gorm:"primaryKey;autoIncrement"`
	AppId      string    `gorm:"type:varchar(64);not null;index:IDX_user_chatroom_mic_spend_time_record_idx_app_room_uid_mic,priority:1"`
	ChatroomId int64     `gorm:"not null;index:IDX_user_chatroom_mic_spend_time_record_idx_app_room_uid_mic,priority:2"`
	Uid        int64     `gorm:"not null;index:IDX_user_chatroom_mic_spend_time_record_idx_app_room_uid_mic,priority:3"`
	MicIndex   int64     `gorm:"not null;index:IDX_user_chatroom_mic_spend_time_record_idx_app_room_uid_mic,priority:4"`
	EnterTime  Timestamp `gorm:"type:char(64);not null"`
	LeaveTime  Timestamp `gorm:"type:char(64);not null"`
	CreatedAt  Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt  Timestamp `gorm:"type:char(64);not null"`
}

func (UserChatroomMicSpendTimeRecord) TableName() string {
	return "user_chatroom_mic_spend_time_record"
}

// RobotInfo 机器人资料。
type RobotInfo struct {
	Id                uint64    `gorm:"primaryKey;autoIncrement"`
	AppId             string    `gorm:"type:varchar(64);not null;index:IDX_robot_info_idx_app_room_uid,priority:1;uniqueIndex:UQE_robot_info_uni_app_uid,priority:1"`
	Uid               int64     `gorm:"not null;index:IDX_robot_info_idx_app_room_uid,priority:2;uniqueIndex:UQE_robot_info_uni_app_uid,priority:2"`
	Nickname          string    `gorm:"type:mediumtext;not null"`
	Avatar            string    `gorm:"type:mediumtext;not null"`
	CustomInfo        StringMap `gorm:"type:longtext;not null"`
	ChatroomRemoteExt StringMap `gorm:"type:longtext;not null"`
	SearchData        StringMap `gorm:"type:longtext;not null"`
	CreatedAt         Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt         Timestamp `gorm:"type:char(64);not null"`
}

func (RobotInfo) TableName() string { return "robot_info" }

// UserAtRoomDurationRecord 用户在房记录，时间为毫秒时间戳。
type UserAtRoomDurationRecord struct {
	Id          uint64    `gorm:"primaryKey;autoIncrement"`
	AppId       string    `gorm:"type:varchar(64);not null;default:'';index:IDX_user_at_room_duration_record_idx_app_uid_room,priority:1;index:IDX_user_at_room_duration_record_idx_app_uid,priority:1;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_enter,priority:1;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_leave,priority:1"`
	Uid         int64     `gorm:"not null;default:0;index:IDX_user_at_room_duration_record_idx_app_uid_room,priority:2;index:IDX_user_at_room_duration_record_idx_app_uid,priority:2;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_enter,priority:2;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_leave,priority:2"`
	ChatroomId  int64     `gorm:"not null;default:0;index:IDX_user_at_room_duration_record_idx_app_uid_room,priority:3;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_enter,priority:3;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_leave,priority:3"`
	LeaveReason int32     `gorm:"type:tinyint(3);not null;default:0"`
	EnterTime   int64     `gorm:"not null;default:0;index:IDX_user_at_room_duration_record_idx_enter;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_enter,priority:4"`
	LeaveTime   int64     `gorm:"not null;default:0;uniqueIndex:UQE_user_at_room_duration_record_uqe_app_uid_room_leave,priority:4"`
	CreatedAt   Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt   Timestamp `gorm:"type:char(64);not null"`
}

func (UserAtRoomDurationRecord) TableName() string { return "user_at_room_duration_record" }

// UserAtMicDurationRecord 用户在麦记录，时间为毫秒时间戳。
type UserAtMicDurationRecord struct {
	Id         uint64    `gorm:"primaryKey;autoIncrement"`
	AppId      string    `gorm:"type:varchar(64);not null;default:'';index:IDX_user_at_mic_duration_record_idx_app_uid_room,priority:1;index:IDX_user_at_mic_duration_record_idx_app_uid,priority:1;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_up,priority:1;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_down,priority:1"`
	Uid        int64     `gorm:"not null;default:0;index:IDX_user_at_mic_duration_record_idx_app_uid_room,priority:2;index:IDX_user_at_mic_duration_record_idx_app_uid,priority:2;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_up,priority:2;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_down,priority:2"`
	ChatroomId int64     `gorm:"not null;default:0;index:IDX_user_at_mic_duration_record_idx_app_uid_room,priority:3;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_up,priority:3;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_down,priority:3"`
	UpIndex    int32     `gorm:"type:tinyint(3);not null;default:-1"`
	DownIndex  int32     `gorm:"type:tinyint(3);not null;default:-1"`
	DownReason int32     `gorm:"type:tinyint(3);not null;default:0"`
	UpTime     int64     `gorm:"not null;default:0;index:IDX_user_at_mic_duration_record_idx_up;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_up,priority:4"`
	DownTime   int64     `gorm:"not null;default:0;uniqueIndex:UQE_user_at_mic_duration_record_uqe_app_uid_room_down,priority:4"`
	CreatedAt  Timestamp `gorm:"type:char(64);not null"`
	UpdatedAt  Timestamp `gorm:"type:char(64);not null"`
}

func (UserAtMicDurationRecord) TableName() string { return "user_at_mic_duration_record" }

// AutoMigrate creates missing tables, columns and indexes.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&ChatroomInfo{},
		&ChatroomMemberStatusMemoryList{},
		&Role{},
		&Permission{},
		&UserLogicRole{},
		&ChatroomMemberSearchInfo{},
		&ChatroomMemberInfo{},
		&UserChatroomSpendTimeRecord{},
		&UserChatroomMicSpendTimeRecord{},
		&RobotInfo{},
		&UserAtRoomDurationRecord{},
		&UserAtMicDurationRecord{},
	)
}
