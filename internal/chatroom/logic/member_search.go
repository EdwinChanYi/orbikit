package logic

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/model"
)

const defaultMemberLimit = 20

// MemberDetail is a member row joined with its status flags and roles.
type MemberDetail struct {
	model.ChatroomMemberInfo
	IsBlock     bool
	IsMuted     bool
	IsKicked    bool
	IsOnline    bool
	MutedBegin  time.Time
	MutedEnd    time.Time
	KickedBegin time.Time
	KickedEnd   time.Time
	RoleId      []int32
}

// Proto converts the row to the member info returned to clients.
func (m *MemberDetail) Proto() *servicepb.ChatroomMemberInfo {
	return &servicepb.ChatroomMemberInfo{
		Uid:               m.Uid,
		Nickname:          m.Nickname,
		Avatar:            m.Avatar,
		CustomInfo:        m.CustomInfo,
		ChatroomRemoteExt: m.ChatroomRemoteExt,
		IsInBlockList:     m.IsBlock,
		IsInKickList:      m.IsKicked,
		IsInMuteList:      m.IsMuted,
		IsOnline:          m.IsOnline,
		IsReboot:          m.IsRobot,
		EnterTime:         m.UpdatedAt.UnixNano() / int64(time.Millisecond),
		SearchKeys:        m.SearchData,
		RoleId:            m.RoleId,
	}
}

// memberPage describes the keyset paging of member lists. The cursor is the
// millisecond updated_at of the last row seen; stored times are RFC3339 text.
type memberPage struct {
	asc       bool
	enterTime int64
	limit     int
}

func newMemberPage(sortType servicepb.ChatroomMemberFetchSortType, enterTime int64, limit int32) memberPage {
	p := memberPage{
		asc:       sortType == servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_ASC,
		enterTime: enterTime,
		limit:     defaultMemberLimit,
	}
	if limit != 0 {
		p.limit = int(limit)
	}
	return p
}

// cursor returns the updated_at condition and its argument, or "" when the
// first page is requested. The cursor is moved by one millisecond so the
// last row is not returned again.
func (p memberPage) cursor() (string, any) {
	if p.enterTime <= 0 {
		return "", nil
	}
	sec, ms := p.enterTime/1000, p.enterTime%1000
	if p.asc {
		return "AND mi.updated_at > ? ", time.Unix(sec, (ms+1)*int64(time.Millisecond)).Format(time.RFC3339Nano)
	}
	return "AND mi.updated_at < ? ", time.Unix(sec, (ms-1)*int64(time.Millisecond)).Format(time.RFC3339Nano)
}

// order sorts by updated_at, optionally putting robots last.
func (p memberPage) order(robotsLast bool) string {
	dir := "DESC"
	if p.asc {
		dir = "ASC"
	}
	if robotsLast {
		return fmt.Sprintf("ORDER BY mi.is_robot ASC, mi.updated_at %s LIMIT %d", dir, p.limit)
	}
	return fmt.Sprintf("ORDER BY mi.updated_at %s LIMIT %d", dir, p.limit)
}

func (p memberPage) apply(sqlText string, args []any, robotsLast bool) (string, []any) {
	if cond, v := p.cursor(); cond != "" {
		sqlText += cond
		args = append(args, v)
	}
	return sqlText + p.order(robotsLast), args
}

func parseRFC3339(s string) (time.Time, error) { return model.ParseTime(s) }

func parseStringMap(s string) (model.StringMap, error) {
	if s == "" {
		return nil, nil
	}
	var m model.StringMap
	err := json.Unmarshal([]byte(s), &m)
	return m, err
}

// parseMemberRow decodes a joined member/status row. Rows with malformed
// data are reported and skipped by the caller.
func parseMemberRow(row map[string]string) (*MemberDetail, error) {
	m := &MemberDetail{}
	var err error
	if m.Uid, err = strconv.ParseInt(row["uid"], 10, 64); err != nil {
		return nil, fmt.Errorf("uid: %w", err)
	}
	if v, ok := row["id"]; ok {
		if m.Id, err = strconv.ParseUint(v, 10, 64); err != nil {
			return nil, fmt.Errorf("id: %w", err)
		}
	}
	if v, ok := row["chatroom_id"]; ok {
		if m.ChatroomId, err = strconv.ParseInt(v, 10, 64); err != nil {
			return nil, fmt.Errorf("chatroom_id: %w", err)
		}
	}
	m.AppId = row["app_id"]
	m.Nickname = row["nickname"]
	m.Avatar = row["avatar"]
	if m.CustomInfo, err = parseStringMap(row["custom_info"]); err != nil {
		return nil, fmt.Errorf("custom_info: %w", err)
	}
	if m.ChatroomRemoteExt, err = parseStringMap(row["chatroom_remote_ext"]); err != nil {
		return nil, fmt.Errorf("chatroom_remote_ext: %w", err)
	}
	if m.SearchData, err = parseStringMap(row["search_data"]); err != nil {
		return nil, fmt.Errorf("search_data: %w", err)
	}
	m.IsRobot = parseBool(row["is_robot"])
	m.IsBlock = parseBool(row["is_block"])
	m.IsMuted = parseBool(row["is_muted"])
	m.IsKicked = parseBool(row["is_kicked"])
	m.IsOnline = parseBool(row["is_online"])

	times := []struct {
		col string
		dst *time.Time
	}{
		{"muted_begin_at", &m.MutedBegin},
		{"muted_finish_at", &m.MutedEnd},
		{"kicked_begin_at", &m.KickedBegin},
		{"kicked_finish_at", &m.KickedEnd},
		{"updated_at", &m.UpdatedAt.Time},
	}
	if _, ok := row["created_at"]; ok {
		times = append(times, struct {
			col string
			dst *time.Time
		}{"created_at", &m.CreatedAt.Time})
	}
	for _, t := range times {
		if *t.dst, err = parseRFC3339(row[t.col]); err != nil {
			return nil, fmt.Errorf("%s: %w", t.col, err)
		}
	}
	return m, nil
}

// queryMembers runs a member list query and attaches the roles of each row.
func (c *ChatroomContainer) queryMembers(name, sqlText string, args ...any) ([]MemberDetail, error) {
	rows, err := queryStrings(db, sqlText, args...)
	if err != nil {
		c.ctx.Error(name+" err", zap.Error(err))
		return nil, err
	}
	members := make([]MemberDetail, 0, len(rows))
	uids := make([]int64, 0, len(rows))
	for _, row := range rows {
		m, err := parseMemberRow(row)
		if err != nil {
			c.ctx.Error(name+" parse row err", zap.Error(err))
			continue
		}
		members = append(members, *m)
		uids = append(uids, m.Uid)
	}
	roles := memberRoleIDsBatch(c.appId, c.roomId, uids)
	for i := range members {
		if r, ok := roles[members[i].Uid]; ok {
			members[i].RoleId = r
		} else {
			members[i].RoleId = []int32{}
		}
	}
	return members, nil
}

const memberDetailColumns = `SELECT mi.id, mi.app_id, mi.chatroom_id, mi.uid, mi.nickname, mi.avatar,
mi.custom_info, mi.chatroom_remote_ext, mi.search_data,
mi.is_robot, mi.created_at, mi.updated_at,
ms.is_block, ms.is_muted, ms.is_kicked, ms.is_online,
ms.muted_begin_at, ms.muted_finish_at, ms.kicked_begin_at, ms.kicked_finish_at
FROM chatroom_member_info mi
INNER JOIN chatroom_member_status_memory_list ms
ON ms.uid = mi.uid
WHERE ms.app_id = ? AND ms.chatroom_id = ? AND mi.app_id = ? AND mi.chatroom_id = ? `

// QueryHumanMemberInfo lists the human members of the room.
func (c *ChatroomContainer) QueryHumanMemberInfo(sortType servicepb.ChatroomMemberFetchSortType,
	enterTime int64, limit int32, isDetail, isOnline bool) (servicepb.ErrorCode, []*servicepb.HumanInfo, error) {
	cols := "mi.id, mi.uid, mi.updated_at"
	if isDetail {
		cols = "mi.id, mi.uid, mi.nickname, mi.avatar, mi.updated_at, ms.is_block, ms.is_muted, ms.muted_finish_at"
	}
	sqlText := `SELECT ` + cols + `
		FROM chatroom_member_info mi
		INNER JOIN chatroom_member_status_memory_list ms
		ON ms.uid = mi.uid
		WHERE mi.app_id = ? AND mi.chatroom_id = ? AND
		ms.app_id = ? AND ms.chatroom_id = ? AND
		mi.is_robot = 0 `
	if isOnline {
		sqlText += "AND ms.is_online = 1 "
	}
	sqlText, args := newMemberPage(sortType, enterTime, limit).apply(sqlText,
		[]any{c.appId, c.roomId, c.appId, c.roomId}, false)

	rows, err := queryStrings(db, sqlText, args...)
	if err != nil {
		c.ctx.Error("QueryHumanMemberInfo err", zap.Error(err))
		return servicepb.ErrorCode_SUCCESS, nil, err
	}

	var out []*servicepb.HumanInfo
	for _, row := range rows {
		updatedAt, err := parseRFC3339(row["updated_at"])
		if err != nil {
			c.ctx.Error("QueryHumanMemberInfo updated_at err", zap.Error(err))
			continue
		}
		uid, err := strconv.ParseInt(row["uid"], 10, 64)
		if err != nil {
			c.ctx.Error("QueryHumanMemberInfo uid err", zap.Error(err))
			continue
		}
		info := &servicepb.HumanInfo{Uid: uid, EnterTime: updatedAt.UnixNano() / int64(time.Millisecond)}
		if isDetail {
			mutedFinishAt, err := parseRFC3339(row["muted_finish_at"])
			if err != nil {
				c.ctx.Error("QueryHumanMemberInfo muted_finish_at err", zap.Error(err))
				continue
			}
			info.Nickname = row["nickname"]
			info.Avatar = row["avatar"]
			info.RoleId = memberRoleIDsFromDB(c.appId, c.roomId, uid)
			info.IsInBlockList = parseBool(row["is_block"])
			info.IsInMuteList = parseBool(row["is_muted"])
			info.MutedFinishAt = mutedFinishAt.UnixNano() / int64(time.Millisecond)
		}
		out = append(out, info)
	}
	return servicepb.ErrorCode_SUCCESS, out, nil
}

// QueryMemberInfoByType lists members by status. Listing blocked, muted or
// kicked members requires the matching privilege; listing online members
// requires being in the room.
func (c *ChatroomContainer) QueryMemberInfoByType(uid int64, sortType servicepb.ChatroomMemberFetchSortType,
	enterTime int64, limit int32, memberTypes []servicepb.ChatroomMemberFetchMemberType, onlyRobot bool) (
	servicepb.ErrorCode, []MemberDetail, error) {
	var conds []string
	for _, t := range memberTypes {
		var (
			errCode servicepb.ErrorCode
			err     error
		)
		switch t {
		case servicepb.ChatroomMemberFetchMemberType_CHATROOM_MEMBER_FETCH_MEMBER_BLOCK:
			errCode, err = c.CheckUserPermission(uid, 0, servicepb.Privilege_BLOCK)
			conds = append(conds, "ms.is_block = 1")
		case servicepb.ChatroomMemberFetchMemberType_CHATROOM_MEMBER_FETCH_MEMBER_MUTE:
			errCode, err = c.CheckUserPermission(uid, 0, servicepb.Privilege_MUTE)
			conds = append(conds, "ms.is_muted = 1")
		case servicepb.ChatroomMemberFetchMemberType_CHATROOM_MEMBER_FETCH_MEMBER_KICK:
			errCode, err = c.CheckUserPermission(uid, 0, servicepb.Privilege_KICK)
			conds = append(conds, "ms.is_kicked = 1")
		case servicepb.ChatroomMemberFetchMemberType_CHATROOM_MEMBER_FETCH_MEMBER_NORMAL:
			errCode, err = c.IsUserInChatroom(uid)
			conds = append(conds, "ms.is_online = 1")
		default:
			return servicepb.ErrorCode_SUCCESS, nil, fmt.Errorf("QueryMemberInfoByType member type not support: %v", memberTypes)
		}
		if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
			return errCode, nil, err
		}
	}

	sqlText := memberDetailColumns
	if onlyRobot {
		sqlText += "AND mi.is_robot = 1 "
	}
	if len(conds) > 0 {
		sqlText += "AND (" + strings.Join(conds, " OR ") + ") "
	}
	sqlText, args := newMemberPage(sortType, enterTime, limit).apply(sqlText,
		[]any{c.appId, c.roomId, c.appId, c.roomId}, true)
	members, err := c.queryMembers("QueryMemberInfoByType", sqlText, args...)
	return servicepb.ErrorCode_SUCCESS, members, err
}

// QueryMemberInfoByRole lists members holding any of roleIDs.
func (c *ChatroomContainer) QueryMemberInfoByRole(sortType servicepb.ChatroomMemberFetchSortType,
	enterTime int64, limit int32, roleIDs []int64, isOnline, onlyRobot bool) (servicepb.ErrorCode, []MemberDetail, error) {
	ids := make([]string, len(roleIDs))
	for i, id := range roleIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	sqlText := `SELECT mi.uid, mi.nickname, mi.avatar, mi.is_robot, mi.updated_at,
mi.custom_info, mi.chatroom_remote_ext, mi.search_data,
ms.is_block, ms.is_muted, ms.is_kicked, ms.is_online,
ms.muted_begin_at, ms.muted_finish_at, ms.kicked_begin_at, ms.kicked_finish_at
FROM chatroom_member_info mi, chatroom_member_status_memory_list ms, user_logic_role lr
WHERE mi.uid = ms.uid AND mi.uid = lr.uid AND
mi.app_id = ? AND mi.chatroom_id = ? AND
ms.app_id = ? AND ms.chatroom_id = ? AND
lr.app_id = ? AND lr.chatroom_id = ? AND
lr.role_id IN (` + strings.Join(ids, ",") + `) `
	if onlyRobot {
		sqlText += "AND mi.is_robot = 1 "
	}
	if isOnline {
		sqlText += "AND ms.is_online = 1 "
	}
	sqlText, args := newMemberPage(sortType, enterTime, limit).apply(sqlText,
		[]any{c.appId, c.roomId, c.appId, c.roomId, c.appId, c.roomId}, true)
	members, err := c.queryMembers("QueryMemberInfoByRole", sqlText, args...)
	return servicepb.ErrorCode_SUCCESS, members, err
}

// QueryMemberInfoById lists the members with the given uids (all members
// when uids is empty).
func (c *ChatroomContainer) QueryMemberInfoById(sortType servicepb.ChatroomMemberFetchSortType,
	enterTime int64, limit int32, uids []int64) (servicepb.ErrorCode, []MemberDetail, error) {
	sqlText := memberDetailColumns
	args := []any{c.appId, c.roomId, c.appId, c.roomId}
	if len(uids) > 0 {
		sqlText += "AND mi.uid IN ? "
		args = append(args, uids)
	}
	sqlText, args = newMemberPage(sortType, enterTime, limit).apply(sqlText, args, true)
	members, err := c.queryMembers("QueryMemberInfoById", sqlText, args...)
	return servicepb.ErrorCode_SUCCESS, members, err
}

// QueryMemberInfoByKeyword lists members whose uid, nickname or a search
// value contains keyword.
func (c *ChatroomContainer) QueryMemberInfoByKeyword(sortType servicepb.ChatroomMemberFetchSortType,
	enterTime int64, limit int32, keyword string) (servicepb.ErrorCode, []MemberDetail, error) {
	keyword = html.EscapeString(keyword)
	sqlText := memberDetailColumns + `AND mi.uid IN (
	SELECT uid FROM chatroom_member_search_info msi
	WHERE msi.app_id = ? AND msi.chatroom_id = ?
	AND (INSTR(msi.uid, ?) > 0 OR INSTR(msi.nickname, ?) > 0 OR INSTR(msi.` + "`value`" + `, ?) > 0)
) `
	sqlText, args := newMemberPage(sortType, enterTime, limit).apply(sqlText,
		[]any{c.appId, c.roomId, c.appId, c.roomId, c.appId, c.roomId, keyword, keyword, keyword}, true)
	members, err := c.queryMembers("QueryMemberInfoByKeyword", sqlText, args...)
	return servicepb.ErrorCode_SUCCESS, members, err
}
