package model

import (
	"testing"
	"time"
)

func TestTimestampValueScan(t *testing.T) {
	now := time.Now()
	v, err := At(now).Value()
	if err != nil {
		t.Fatal(err)
	}
	var got Timestamp
	if err := got.Scan([]byte(v.(string))); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(now) {
		t.Fatalf("round trip = %v, want %v", got.Time, now)
	}

	if v, _ := (Timestamp{}).Value(); v != "" {
		t.Fatalf("zero timestamp should store empty string, got %q", v)
	}
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Fatalf("Scan(nil) = %v, %v", got, err)
	}
	if err := got.Scan(""); err != nil || !got.IsZero() {
		t.Fatalf(`Scan("") = %v, %v`, got, err)
	}
	if err := got.Scan(42); err == nil {
		t.Fatal("Scan(int) should fail")
	}
}

func TestParseTimeLegacyLayout(t *testing.T) {
	got, err := ParseTime("2023-01-02 03:04:05.678")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2023, 1, 2, 3, 4, 5, 678*int(time.Millisecond), time.Local)
	if !got.Equal(want) {
		t.Fatalf("ParseTime = %v, want %v", got, want)
	}
	if _, err := ParseTime("yesterday"); err == nil {
		t.Fatal("garbage should fail")
	}
}

func TestStringMapAndList(t *testing.T) {
	v, err := StringMap{"k": "v"}.Value()
	if err != nil || v != `{"k":"v"}` {
		t.Fatalf("StringMap.Value = %v, %v", v, err)
	}
	if v, _ := StringMap(nil).Value(); v != "null" {
		t.Fatalf("nil StringMap = %v", v)
	}
	var m StringMap
	if err := m.Scan([]byte(`{"a":"b"}`)); err != nil || m["a"] != "b" {
		t.Fatalf("StringMap.Scan = %v, %v", m, err)
	}
	if err := m.Scan(""); err != nil || m != nil {
		t.Fatalf("empty StringMap.Scan = %v, %v", m, err)
	}
	if err := m.Scan("{"); err == nil {
		t.Fatal("malformed JSON should fail")
	}

	var l StringList
	if err := l.Scan(`["x","y"]`); err != nil || len(l) != 2 || l[1] != "y" {
		t.Fatalf("StringList.Scan = %v, %v", l, err)
	}
	if v, _ := l.Value(); v != `["x","y"]` {
		t.Fatalf("StringList.Value = %v", v)
	}
}
