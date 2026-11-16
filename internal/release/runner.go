package release

import (
	"context"
	"os"
	"time"
)

// Source fetches one tag or one full SHA into dir.
type Source interface {
	Fetch(ctx context.Context, dir, kind, ref string) (string, error)
}

// Spec is the container configuration copied onto the replacement.
type Spec struct {
	Env           []string
	Cmd           []string
	Entrypoint    []string
	User          string
	WorkingDir    string
	Binds         []string
	PortBindings  []byte
	RestartPolicy []byte
	NetworkMode   string
}

// Engine builds images and swaps one container. Tests supply a fake.
type Engine interface {
	Build(ctx context.Context, dir, service, tag, goproxy string) (imageID, log string, err error)
	ImageExists(ctx context.Context, imageID string) (bool, error)
	Inspect(ctx context.Context, container string) (Spec, error)
	Create(ctx context.Context, name string, spec Spec, imageID string) error
	StopRemove(ctx context.Context, container string) error
	Rename(ctx context.Context, from, to string) error
	Start(ctx context.Context, container string) error
}

// Deps are the collaborators for one release run.
type Deps struct {
	Store   Store
	Source  Source
	Engine  Engine
	Project string
	GoProxy string
	Now     func() time.Time
}

// Run executes the task. A task that is already finished is left unchanged.
func Run(ctx context.Context, deps Deps, id uint64) error {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	task, err := deps.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	ok, err := deps.Store.MarkRunning(ctx, id)
	if err != nil || !ok {
		return err
	}
	logText := ""
	fail := func(message, imageID string) error {
		_, finishErr := deps.Store.Finish(ctx, id, StatusFailed, imageID, logText, message, now())
		return finishErr
	}
	if !Allowed(task.Service) {
		return fail("服务名不在允许范围内", "")
	}
	imageID := task.ImageID
	if task.Kind == KindPublish {
		kind, err := ParseRef(task.GitRef)
		if err != nil {
			return fail(err.Error(), "")
		}
		dir, err := os.MkdirTemp("", "orbikit-release-")
		if err != nil {
			return fail("拉取失败", "")
		}
		defer os.RemoveAll(dir)
		fetched, err := deps.Source.Fetch(ctx, dir, kind, task.GitRef)
		logText += fetched
		if err != nil {
			return fail("拉取失败", "")
		}
		proxy := deps.GoProxy
		if proxy == "" {
			proxy = "https://proxy.golang.org,direct"
		}
		built, buildLog, err := deps.Engine.Build(ctx, dir, task.Service, ImageTag(task.Service, task.GitRef), proxy)
		logText += buildLog
		if err != nil {
			return fail("构建失败", "")
		}
		imageID = built
		kept, err := deps.Store.SetImage(ctx, id, imageID)
		if err != nil || !kept {
			return err
		}
	} else {
		exists, err := deps.Engine.ImageExists(ctx, imageID)
		if err != nil {
			return fail(err.Error(), imageID)
		}
		if !exists {
			return fail("镜像不在本机", imageID)
		}
	}
	container, err := ContainerName(deps.Project, task.Service)
	if err != nil {
		return fail(err.Error(), imageID)
	}
	spec, err := deps.Engine.Inspect(ctx, container)
	if err != nil {
		logText += err.Error()
		return fail("容器替换未完成", imageID)
	}
	temp := container + "-next"
	if err := deps.Engine.Create(ctx, temp, spec, imageID); err != nil {
		logText += err.Error()
		return fail("容器替换未完成", imageID)
	}
	if err := deps.Engine.StopRemove(ctx, container); err != nil {
		logText += "临时容器 " + temp + "\n" + err.Error()
		return fail("容器替换未完成", imageID)
	}
	if err := deps.Engine.Rename(ctx, temp, container); err != nil {
		logText += "临时容器 " + temp + "\n" + err.Error()
		return fail("容器替换未完成", imageID)
	}
	if err := deps.Engine.Start(ctx, container); err != nil {
		logText += "临时容器 " + temp + "\n" + err.Error()
		return fail("容器替换未完成", imageID)
	}
	_, err = deps.Store.Finish(ctx, id, StatusSucceeded, imageID, logText, "", now())
	return err
}
