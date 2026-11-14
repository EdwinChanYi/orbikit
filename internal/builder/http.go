package builder

import (
	"encoding/json"
	"net/http"
)

// NewHandler serves GET /v1/containers/{name} for whitelist names only.
func NewHandler(ins Inspector, project string) http.Handler {
	mux := http.NewServeMux()
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
