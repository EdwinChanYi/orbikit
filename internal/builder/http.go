package builder

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/EdwinChanYi/orbikit/internal/release"
)

// listRemoteRefs is replaced in tests. The token stays in the builder process.
var listRemoteRefs = func(ctx context.Context, username, token string) ([]release.RemoteRef, error) {
	return release.ListRemote(ctx, username, token, nil, "")
}

// NewHandler serves container queries and the release start API.
// dock may be nil in tests that only query containers.
func NewHandler(ins Inspector, project string, rel ReleaseEnv, dock *Docker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/releases/images", func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		if !release.Allowed(service) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
			return
		}
		if dock == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
			return
		}
		versions, err := dock.Images(r.Context(), service)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
	})
	mux.HandleFunc("GET /v1/releases/refs", func(w http.ResponseWriter, r *http.Request) {
		if !rel.Configured() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 Git 凭证"})
			return
		}
		refs, err := listRemoteRefs(r.Context(), rel.Username, rel.Token)
		if err != nil {
			code := http.StatusBadGateway
			message := "拉取失败"
			if err.Error() == "缺少 Git 凭证" {
				code = http.StatusBadRequest
				message = err.Error()
			}
			writeJSON(w, code, map[string]string{"error": message})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
	})
	mux.HandleFunc("GET /v1/releases/ready", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"configured": rel.Configured()})
	})
	mux.HandleFunc("POST /v1/releases/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err != nil || id == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "发布记录不存在"})
			return
		}
		if dock == nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
			return
		}
		if err := dock.Spawn(r.Context(), id, project, rel); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/containers/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		container, err := ContainerName(project, name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "服务名不在允许范围内"})
			return
		}
		info, err := ins.Inspect(r.Context(), container)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "容器查询失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name": name, "status": info.Status, "running": info.Running, "image_id": info.ImageID,
		})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
