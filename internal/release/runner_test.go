package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	kind string
	ref  string
	dir  string
	err  error
	log  string
}

func (f *fakeSource) Fetch(_ context.Context, dir, kind, ref string) (string, error) {
	f.dir = dir
	f.kind = kind
	f.ref = ref
	return f.log, f.err
}

type fakeEngine struct {
	buildID     string
	buildLog    string
	buildErr    error
	exists      bool
	spec        Spec
	inspectErr  error
	createErr   error
	stopErr     error
	renameErr   error
	created     string
	createdSpec Spec
	image       string
	stopped     string
	renamedTo   string
	started     string
	tag         string
}

func (f *fakeEngine) Build(_ context.Context, _, _, tag, _ string) (string, string, error) {
	f.tag = tag
	return f.buildID, f.buildLog, f.buildErr
}

func (f *fakeEngine) ImageExists(context.Context, string) (bool, error) {
	return f.exists, nil
}

func (f *fakeEngine) Inspect(context.Context, string) (Spec, error) {
	return f.spec, f.inspectErr
}

func (f *fakeEngine) Create(_ context.Context, name string, spec Spec, imageID string) error {
	f.created = name
	f.createdSpec = spec
	f.image = imageID
	return f.createErr
}

func (f *fakeEngine) StopRemove(_ context.Context, container string) error {
	f.stopped = container
	return f.stopErr
}

func (f *fakeEngine) Rename(_ context.Context, _, to string) error {
	f.renamedTo = to
	return f.renameErr
}

func (f *fakeEngine) Start(_ context.Context, container string) error {
	f.started = container
	return nil
}

func TestPublishSwapsTheContainerAndDoesNotFetchABranch(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()
	task, err := store.Create(ctx, Task{Service: "core", Kind: KindPublish, GitRef: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{log: "fetched\n"}
	eng := &fakeEngine{
		buildID:  "sha256:new",
		buildLog: "built\n",
		spec:     Spec{Binds: []string{"./configs:/app/configs:ro"}, NetworkMode: "orbikit_default"},
	}
	err = Run(ctx, Deps{Store: store, Source: src, Engine: eng, Project: "orbikit", Now: time.Now}, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if src.kind != RefTag || src.ref != "v1.2.3" || strings.Contains(src.kind+src.ref, "refs/heads") {
		t.Fatalf("fetch kind=%s ref=%s", src.kind, src.ref)
	}
	if src.dir == "" {
		t.Fatal("fetch dir is empty")
	}
	if eng.tag != "orbikit-core:v1.2.3" || eng.image != "sha256:new" {
		t.Fatalf("tag=%s image=%s", eng.tag, eng.image)
	}
	if eng.created != "orbikit-core-1-next" || eng.stopped != "orbikit-core-1" || eng.renamedTo != "orbikit-core-1" || eng.started != "orbikit-core-1" {
		t.Fatalf("swap created=%s stopped=%s renamed=%s", eng.created, eng.stopped, eng.renamedTo)
	}
	if len(eng.createdSpec.Binds) != 1 || eng.createdSpec.NetworkMode != "orbikit_default" {
		t.Fatalf("copied spec = %+v", eng.createdSpec)
	}
	got, _ := store.Get(ctx, task.ID)
	if got.Status != StatusSucceeded || got.ImageID != "sha256:new" || !strings.Contains(got.Log, "built") {
		t.Fatalf("task = %+v", got)
	}
}

func TestPublishKeepsTheOldContainerWhenBuildFails(t *testing.T) {
	store := NewMemory()
	task, _ := store.Create(context.Background(), Task{Service: "core", Kind: KindPublish, GitRef: "v1"})
	eng := &fakeEngine{buildErr: errors.New("compile failed")}
	if err := Run(context.Background(), Deps{Store: store, Source: &fakeSource{}, Engine: eng, Project: "orbikit", Now: time.Now}, task.ID); err != nil {
		t.Fatal(err)
	}
	if eng.created != "" {
		t.Fatal("container was created after a failed build")
	}
	got, _ := store.Get(context.Background(), task.ID)
	if got.Status != StatusFailed || got.Error != "构建失败" || got.ImageID != "" {
		t.Fatalf("task = %+v", got)
	}
}

func TestSwapFailureKeepsTheImageID(t *testing.T) {
	store := NewMemory()
	task, _ := store.Create(context.Background(), Task{Service: "gateway", Kind: KindPublish, GitRef: "v9"})
	eng := &fakeEngine{buildID: "sha256:built", stopErr: errors.New("stop failed")}
	if err := Run(context.Background(), Deps{Store: store, Source: &fakeSource{}, Engine: eng, Project: "orbikit", Now: time.Now}, task.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), task.ID)
	if got.Status != StatusFailed || got.ImageID != "sha256:built" || got.Error != "容器替换未完成" || !strings.Contains(got.Log, "orbikit-gateway-1-next") {
		t.Fatalf("task = %+v", got)
	}
}

func TestRollbackFailsWhenTheImageIsGone(t *testing.T) {
	store := NewMemory()
	task, _ := store.Create(context.Background(), Task{Service: "core", Kind: KindRollback, ImageID: "sha256:old"})
	eng := &fakeEngine{exists: false}
	src := &fakeSource{}
	if err := Run(context.Background(), Deps{Store: store, Source: src, Engine: eng, Project: "orbikit", Now: time.Now}, task.ID); err != nil {
		t.Fatal(err)
	}
	if src.ref != "" || eng.created != "" {
		t.Fatal("rollback fetched or created a container")
	}
	got, _ := store.Get(context.Background(), task.ID)
	if got.Status != StatusFailed || got.Error != "镜像不在本机" || got.ImageID != "sha256:old" {
		t.Fatalf("task = %+v", got)
	}
}

func TestRecreateSkipsTheBuild(t *testing.T) {
	store := NewMemory()
	task, _ := store.Create(context.Background(), Task{Service: "webhook", Kind: KindRecreate, GitRef: "v1", ImageID: "sha256:keep"})
	eng := &fakeEngine{exists: true, spec: Spec{NetworkMode: "orbikit_default"}}
	if err := Run(context.Background(), Deps{Store: store, Source: &fakeSource{}, Engine: eng, Project: "orbikit", Now: time.Now}, task.ID); err != nil {
		t.Fatal(err)
	}
	if eng.tag != "" || eng.image != "sha256:keep" {
		t.Fatalf("tag=%s image=%s", eng.tag, eng.image)
	}
	got, _ := store.Get(context.Background(), task.ID)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestRunLeavesATimedOutTaskFailed(t *testing.T) {
	store := NewMemory()
	task := store.Seed(Task{Service: "core", Status: StatusFailed, Error: "超时", ImageID: "sha256:old"})
	eng := &fakeEngine{buildID: "sha256:new"}
	if err := Run(context.Background(), Deps{Store: store, Source: &fakeSource{}, Engine: eng, Project: "orbikit", Now: time.Now}, task.ID); err != nil {
		t.Fatal(err)
	}
	if eng.tag != "" {
		t.Fatal("timed out task was built")
	}
	got, _ := store.Get(context.Background(), task.ID)
	if got.Status != StatusFailed || got.Error != "超时" {
		t.Fatalf("task = %+v", got)
	}
}
