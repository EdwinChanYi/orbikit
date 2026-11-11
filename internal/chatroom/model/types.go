package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Timestamp is persisted as an RFC3339Nano string in a CHAR(64) column, which
// is the layout already used by existing chatroom tables. In JSON it encodes
// exactly like time.Time.
type Timestamp struct {
	time.Time
}

// Now returns the current local time as a Timestamp.
func Now() Timestamp {
	return Timestamp{Time: time.Now()}
}

// At wraps t.
func At(t time.Time) Timestamp {
	return Timestamp{Time: t}
}

// Value implements driver.Valuer.
func (t Timestamp) Value() (driver.Value, error) {
	if t.IsZero() {
		return "", nil
	}
	return t.Format(time.RFC3339Nano), nil
}

// Scan implements sql.Scanner.
func (t *Timestamp) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		t.Time = time.Time{}
		return nil
	case time.Time:
		t.Time = v
		return nil
	case []byte:
		return t.parse(string(v))
	case string:
		return t.parse(v)
	default:
		return fmt.Errorf("model: cannot scan %T into Timestamp", src)
	}
}

func (t *Timestamp) parse(s string) error {
	pt, err := ParseTime(s)
	if err != nil {
		return err
	}
	t.Time = pt
	return nil
}

// ParseTime parses a stored timestamp string. Empty strings yield the zero time.
func ParseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if pt, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return pt, nil
	}
	pt, err := time.ParseInLocation("2006-01-02 15:04:05.999999999", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("model: invalid timestamp %q", s)
	}
	return pt, nil
}

// FormatTime formats t the same way Timestamp.Value does, for raw SQL.
func FormatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// StringMap is persisted as JSON text; a nil map is stored as "null".
type StringMap map[string]string

// Value implements driver.Valuer.
func (m StringMap) Value() (driver.Value, error) {
	b, err := json.Marshal(map[string]string(m))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (m *StringMap) Scan(src any) error {
	b, err := textBytes(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		*m = nil
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*m = out
	return nil
}

// StringList is persisted as JSON text; a nil slice is stored as "null".
type StringList []string

// Value implements driver.Valuer.
func (l StringList) Value() (driver.Value, error) {
	b, err := json.Marshal([]string(l))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (l *StringList) Scan(src any) error {
	b, err := textBytes(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		*l = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*l = out
	return nil
}

func textBytes(src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("model: cannot scan %T as JSON text", src)
	}
}
