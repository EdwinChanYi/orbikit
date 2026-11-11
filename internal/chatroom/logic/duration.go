package logic

import (
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/model"
	"github.com/yourname/roomkit/pkg/log"
)

const defaultRecordPageSize = 200

// roomRecord opens an at-room record, or closes the open one with reason.
// An open record has leave_time = 0, so the insert collides with it through
// the (app, uid, room, leave_time) unique key.
func roomRecord(appID string, roomID, uid int64, reason servicepb.AtRoomInfo_LeaveReason) error {
	now := time.Now()
	ns, sNow := now.UnixNano(), model.FormatTime(now)
	res := db.Exec(`INSERT user_at_room_duration_record
		(app_id, chatroom_id, uid, enter_time, leave_time, created_at, updated_at)
		VALUES(?, ?, ?, ?, 0, ?, ?) ON DUPLICATE KEY UPDATE
		updated_at = ?, leave_reason = ?, leave_time = ?`,
		appID, roomID, uid, ns, sNow, sNow,
		sNow, int32(reason), ns)
	fields := []zap.Field{zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Int64("uid", uid),
		zap.Int32("reason", int32(reason))}
	switch {
	case res.Error != nil:
		log.L().Error("RoomRecord exec err", zap.Error(res.Error))
		return res.Error
	case res.RowsAffected == 0:
		log.L().Error("RoomRecord nothing insert/update", fields...)
	case res.RowsAffected == 2:
		log.L().Debug("RoomRecord leave room", fields...)
	}
	return nil
}

// micRecord opens an at-mic record, or closes the open one with reason.
// MIC_CHANGED closes the old seat and then opens a record for the new one.
func micRecord(appID string, roomID, uid int64, micIndex int32, reason servicepb.OnMicInfo_OffReason) error {
	for attempt := 0; attempt < 2; attempt++ {
		now := time.Now()
		ns, sNow := now.UnixNano(), model.FormatTime(now)
		res := db.Exec(`INSERT user_at_mic_duration_record
			(app_id, chatroom_id, uid, up_index, up_time, down_time, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, 0, ?, ?) ON DUPLICATE KEY UPDATE
			updated_at = ?, down_index = ?, down_reason = ?, down_time = ?`,
			appID, roomID, uid, micIndex, ns, sNow, sNow,
			sNow, micIndex, int32(reason), ns)
		fields := []zap.Field{zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Int64("uid", uid),
			zap.Int32("micIndex", micIndex), zap.Int32("reason", int32(reason))}
		switch {
		case res.Error != nil:
			log.L().Error("MicRecord exec err", zap.Error(res.Error))
			return res.Error
		case res.RowsAffected == 0:
			log.L().Error("MicRecord nothing inserted or updated", fields...)
			return nil
		case res.RowsAffected == 2 && reason == servicepb.OnMicInfo_MIC_CHANGED:
			log.L().Debug("MicRecord change", fields...)
			continue
		case res.RowsAffected == 2:
			log.L().Debug("MicRecord downMic", fields...)
		}
		return nil
	}
	return nil
}

// recordCursor builds the paging condition for a millisecond cursor; the
// stored times are nanoseconds.
func recordCursor(column string, last int64, asc bool) (string, int64) {
	if asc {
		return column + " > ?", (last + 1) * int64(time.Millisecond)
	}
	return column + " < ?", (last - 1) * int64(time.Millisecond)
}

func recordLimit(pageSize int) int {
	if pageSize != 0 {
		return pageSize
	}
	return defaultRecordPageSize
}

// queryUserAtRoomRecord pages closed at-room records of uid; sortType 2 is
// ascending by enter time, anything else descending. Times are milliseconds.
func queryUserAtRoomRecord(appID string, uid, lastEnter int64, sortType int32, pageSize int) ([]*model.UserAtRoomDurationRecord, error) {
	asc := sortType == 2
	q := db.Select("chatroom_id", "leave_reason", "enter_time", "leave_time").
		Where("app_id = ? AND uid = ? AND enter_time <> 0 AND leave_time > enter_time", appID, uid)
	if lastEnter > 0 {
		cond, v := recordCursor("enter_time", lastEnter, asc)
		q = q.Where(cond, v)
	}
	if asc {
		q = q.Order("enter_time ASC")
	} else {
		q = q.Order("enter_time DESC")
	}
	records := make([]*model.UserAtRoomDurationRecord, 0)
	if err := q.Limit(recordLimit(pageSize)).Find(&records).Error; err != nil {
		return nil, err
	}
	for _, r := range records {
		r.EnterTime /= int64(time.Millisecond)
		r.LeaveTime /= int64(time.Millisecond)
	}
	return records, nil
}

// queryUserAtMicRecord pages closed at-mic records of uid, like
// queryUserAtRoomRecord.
func queryUserAtMicRecord(appID string, uid, lastEnter int64, sortType int32, pageSize int) ([]*model.UserAtMicDurationRecord, error) {
	asc := sortType == 2
	q := db.Select("chatroom_id", "down_reason", "up_index", "down_index", "up_time", "down_time").
		Where("app_id = ? AND uid = ? AND up_time <> 0 AND down_time > up_time", appID, uid)
	if lastEnter > 0 {
		cond, v := recordCursor("up_time", lastEnter, asc)
		q = q.Where(cond, v)
	}
	if asc {
		q = q.Order("up_time ASC")
	} else {
		q = q.Order("up_time DESC")
	}
	records := make([]*model.UserAtMicDurationRecord, 0)
	if err := q.Limit(recordLimit(pageSize)).Find(&records).Error; err != nil {
		return nil, err
	}
	for _, r := range records {
		r.UpTime /= int64(time.Millisecond)
		r.DownTime /= int64(time.Millisecond)
	}
	return records, nil
}
