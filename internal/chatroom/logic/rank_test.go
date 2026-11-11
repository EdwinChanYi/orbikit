package logic

import "testing"

func TestRankScoresOrderByPrimaryCount(t *testing.T) {
	// More members always outranks more mics in the member ranking, as long
	// as the used mic count stays below micPosition.
	if !(memicScore(2, 0) > memicScore(1, 99)) {
		t.Fatalf("memicScore(2,0)=%v should exceed memicScore(1,99)=%v", memicScore(2, 0), memicScore(1, 99))
	}
	if !(memicScore(1, 2) > memicScore(1, 1)) {
		t.Fatal("used mics should break ties between equal member counts")
	}
	if !(micmemScore(0, 1) > micmemScore(99999999, 0)) {
		t.Fatal("more used mics should outrank more members in the mic ranking")
	}
	if !(micmemScore(5, 1) > micmemScore(4, 1)) {
		t.Fatal("members should break ties between equal mic counts")
	}
}

func TestRankScoreValues(t *testing.T) {
	if got := memicScore(3, 4); got != 304 {
		t.Fatalf("memicScore(3,4) = %v, want 304", got)
	}
	if got := micmemScore(3, 4); got != 400000003 {
		t.Fatalf("micmemScore(3,4) = %v, want 400000003", got)
	}
}

func TestRankWindow(t *testing.T) {
	tests := []struct {
		page, pageSize      int32
		wantOffset, wantCnt int64
	}{
		{page: 1, pageSize: 5, wantOffset: 0, wantCnt: 5},
		{page: 3, pageSize: 5, wantOffset: 10, wantCnt: 5},
		{page: 1, pageSize: 0, wantOffset: 0, wantCnt: defaultRankPageSize},
		{page: 2, pageSize: 0, wantOffset: defaultRankPageSize, wantCnt: defaultRankPageSize},
	}
	for _, tt := range tests {
		offset, count := rankWindow(tt.page, tt.pageSize)
		if offset != tt.wantOffset || count != tt.wantCnt {
			t.Errorf("rankWindow(%d, %d) = (%d, %d), want (%d, %d)",
				tt.page, tt.pageSize, offset, count, tt.wantOffset, tt.wantCnt)
		}
	}
}
