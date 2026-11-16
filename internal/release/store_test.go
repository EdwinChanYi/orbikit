package release

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateRejectsASecondOpenTask(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()
	if _, err := store.Create(ctx, Task{Service: "core", Kind: KindPublish, GitRef: "v1"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.Create(ctx, Task{Service: "webhook", Kind: KindPublish, GitRef: "v2"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
}

func TestExpireClosesOnlyOldOpenTasks(t *testing.T) {
	store := NewMemory()
	old := store.Seed(Task{Service: "core", Status: StatusRunning, CreatedAt: time.Now().Add(-Timeout - time.Minute)})
	fresh := store.Seed(Task{Service: "chatroom", Status: StatusQueued, CreatedAt: time.Now()})
	now := time.Now()
	if err := store.Expire(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), old.ID)
	if err != nil || got.Status != StatusFailed || got.Error != "超时" || got.FinishedAt == nil {
		t.Fatalf("old = %+v %v", got, err)
	}
	got, err = store.Get(context.Background(), fresh.ID)
	if err != nil || got.Status != StatusQueued {
		t.Fatalf("fresh = %+v %v", got, err)
	}
	ok, err := store.Finish(context.Background(), old.ID, StatusSucceeded, "sha256:abc", "", "", now)
	if err != nil || ok {
		t.Fatalf("finish closed task = %v %v", ok, err)
	}
	got, _ = store.Get(context.Background(), old.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status changed to %s", got.Status)
	}
}

func TestLatestSuccessIgnoresFailures(t *testing.T) {
	store := NewMemory()
	store.Seed(Task{Service: "core", Status: StatusSucceeded, ImageID: "sha256:old"})
	store.Seed(Task{Service: "core", Status: StatusFailed, ImageID: "sha256:newer"})
	store.Seed(Task{Service: "core", Status: StatusSucceeded, ImageID: "sha256:keep"})
	task, ok, err := store.LatestSuccess(context.Background(), "core")
	if err != nil || !ok || task.ImageID != "sha256:keep" {
		t.Fatalf("latest = %+v ok=%v err=%v", task, ok, err)
	}
	if _, ok, err := store.LatestSuccess(context.Background(), "gateway"); err != nil || ok {
		t.Fatalf("gateway ok=%v err=%v", ok, err)
	}
}
