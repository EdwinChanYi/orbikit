// Package releaseapi serves the operator release API.
package releaseapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/EdwinChanYi/orbikit/internal/release"
)

// NewHandler serves the release routes. builderURL is the builder base URL.
func NewHandler(store release.Store, builderURL string, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	h := handler{store: store, builder: strings.TrimRight(builderURL, "/"), now: now, client: http.DefaultClient}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases", h.collection)
	mux.HandleFunc("/api/releases/", h.item)
	return mux
}

type handler struct {
	store   release.Store
	builder string
	now     func() time.Time
	client  *http.Client
}

func (h handler) collection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.publish(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h handler) item(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/releases/")
	if rest == "rollback" && r.Method == http.MethodPost {
		h.rollback(w, r)
		return
	}
	if rest == "versions" && r.Method == http.MethodGet {
		h.versions(w, r)
		return
	}
	if rest == "refs" && r.Method == http.MethodGet {
		h.refs(w, r)
		return
	}
	if rest == "deploy" && r.Method == http.MethodPost {
		h.deploy(w, r)
		return
	}
	idText, action, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseUint(idText, 10, 64)
	if err != nil || id == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "发布记录不存在"})
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		h.get(w, r, id)
	case action == "recreate" && r.Method == http.MethodPost:
		h.recreate(w, r, id)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "发布记录不存在"})
	}
}

func (h handler) publish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Service string `json:"service"`
		Ref     string `json:"ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求正文无效"})
		return
	}
	if !release.Allowed(body.Service) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
		return
	}
	if _, err := release.ParseRef(body.Ref); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "引用无效"})
		return
	}
	if !h.prepare(w, r, true) {
		return
	}
	task, err := h.store.Create(r.Context(), release.Task{
		Service: body.Service, Kind: release.KindPublish, GitRef: body.Ref, CreatedAt: h.now(),
	})
	if errors.Is(err, release.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "已有发布在进行"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	h.start(w, r, task.ID)
}

func (h handler) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求正文无效"})
		return
	}
	if !release.Allowed(body.Service) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
		return
	}
	if !h.prepare(w, r, false) {
		return
	}
	previous, ok, err := h.store.LatestSuccess(r.Context(), body.Service)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有可回滚的镜像"})
		return
	}
	task, err := h.store.Create(r.Context(), release.Task{
		Service: body.Service, Kind: release.KindRollback, ImageID: previous.ImageID, CreatedAt: h.now(),
	})
	if errors.Is(err, release.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "已有发布在进行"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	h.start(w, r, task.ID)
}

type imageVersion struct {
	Tag       string    `json:"tag"`
	ImageID   string    `json:"image_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (h handler) refs(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.builder+"/v1/releases/refs", nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	res, err := h.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	if res.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &body)
		if body.Error == "" {
			body.Error = "拉取失败"
		}
		code := res.StatusCode
		if code < 400 || code > 499 {
			code = http.StatusBadGateway
		}
		writeJSON(w, code, map[string]string{"error": body.Error})
		return
	}
	var body struct {
		Refs []release.RemoteRef `json:"refs"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "拉取失败"})
		return
	}
	if body.Refs == nil {
		body.Refs = []release.RemoteRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": body.Refs})
}

func (h handler) versions(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if !release.Allowed(service) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
		return
	}
	versions, err := h.imageVersions(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h handler) deploy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Service string `json:"service"`
		ImageID string `json:"image_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求正文无效"})
		return
	}
	if !release.Allowed(body.Service) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
		return
	}
	if !h.prepare(w, r, false) {
		return
	}
	versions, err := h.imageVersions(r.Context(), body.Service)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
		return
	}
	found := false
	for _, version := range versions {
		if version.ImageID == body.ImageID && body.ImageID != "" {
			found = true
		}
	}
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有可重建的镜像"})
		return
	}
	task, err := h.store.Create(r.Context(), release.Task{
		Service: body.Service, Kind: release.KindRecreate, ImageID: body.ImageID, CreatedAt: h.now(),
	})
	if errors.Is(err, release.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "已有发布在进行"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	h.start(w, r, task.ID)
}

func (h handler) recreate(w http.ResponseWriter, r *http.Request, id uint64) {
	if !h.prepare(w, r, false) {
		return
	}
	source, err := h.store.Get(r.Context(), id)
	if errors.Is(err, release.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "发布记录不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	if source.Status != release.StatusFailed || source.ImageID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有可重建的镜像"})
		return
	}
	task, err := h.store.Create(r.Context(), release.Task{
		Service: source.Service, Kind: release.KindRecreate, GitRef: source.GitRef, ImageID: source.ImageID, CreatedAt: h.now(),
	})
	if errors.Is(err, release.ErrBusy) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "已有发布在进行"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	h.start(w, r, task.ID)
}

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Expire(r.Context(), h.now()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	service := r.URL.Query().Get("service")
	if !release.Allowed(service) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
		return
	}
	items, err := h.store.List(r.Context(), service, 20)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	if items == nil {
		items = []release.Task{}
	}
	active, err := h.store.Active(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	_, rollback, err := h.store.LatestSuccess(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": items, "active": active, "rollback": rollback})
}

func (h handler) get(w http.ResponseWriter, r *http.Request, id uint64) {
	if err := h.store.Expire(r.Context(), h.now()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	task, err := h.store.Get(r.Context(), id)
	if errors.Is(err, release.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "发布记录不存在"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// prepare expires stale tasks and checks builder reachability.
// credentials is true only for a build, which needs Git credentials.
func (h handler) prepare(w http.ResponseWriter, r *http.Request, credentials bool) bool {
	if err := h.store.Expire(r.Context(), h.now()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return false
	}
	configured, err := h.ready(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "builder 不可达"})
		return false
	}
	if credentials && !configured {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 Git 凭证"})
		return false
	}
	return true
}

func (h handler) start(w http.ResponseWriter, r *http.Request, id uint64) {
	if err := h.postStart(r.Context(), id); err != nil {
		_, _ = h.store.Finish(r.Context(), id, release.StatusFailed, "", "", "builder 不可达", h.now())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "builder 不可达", "id": id})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
}

func (h handler) imageVersions(ctx context.Context, service string) ([]imageVersion, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.builder+"/v1/releases/images?service="+url.QueryEscape(service), nil)
	if err != nil {
		return nil, err
	}
	res, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("容器查询失败")
	}
	var body struct {
		Versions []imageVersion `json:"versions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if body.Versions == nil {
		body.Versions = []imageVersion{}
	}
	return body.Versions, nil
}

func (h handler) ready(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.builder+"/v1/releases/ready", nil)
	if err != nil {
		return false, err
	}
	res, err := h.client.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return false, err
	}
	if res.StatusCode != http.StatusOK {
		return false, errors.New("builder 不可达")
	}
	var body struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false, err
	}
	return body.Configured, nil
}

func (h handler) postStart(ctx context.Context, id uint64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.builder+"/v1/releases/"+strconv.FormatUint(id, 10)+"/start", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	res, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusAccepted {
		return errors.New("builder 不可达")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
