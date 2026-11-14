package nacosapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EdwinChanYi/orbikit/pkg/nacos"
)

// NewHandler serves the config API. A nil client means Nacos is off.
func NewHandler(c *nacos.Client) http.Handler {
	return handler{client: c}
}

type handler struct {
	client *nacos.Client
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/config/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] == "source" && r.Method == http.MethodGet {
		source := "local"
		if h.client != nil {
			source = "nacos"
		}
		writeJSON(w, http.StatusOK, map[string]any{"source": source, "enabled": h.client != nil})
		return
	}
	if h.client == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Nacos 未打开"})
		return
	}
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Nacos 未打开"})
		return
	}
	service := parts[0]
	dataID, err := nacos.DataID(service)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		content, err := h.client.Get(r.Context(), dataID)
		if err != nil {
			writeUpstream(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"content": content})
	case action == "" && r.Method == http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求正文无效"})
			return
		}
		if err := h.client.Publish(r.Context(), dataID, body.Content); err != nil {
			writeUpstream(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case action == "history" && r.Method == http.MethodGet:
		revs, err := h.client.History(r.Context(), dataID)
		if err != nil {
			writeUpstream(w, err)
			return
		}
		items := make([]map[string]string, len(revs))
		for i, rev := range revs {
			items[i] = map[string]string{"id": rev.ID, "content": rev.Content, "modified": rev.Modified}
		}
		writeJSON(w, http.StatusOK, map[string]any{"revisions": items})
	case action == "rollback" && r.Method == http.MethodPost:
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求正文无效"})
			return
		}
		revs, err := h.client.History(r.Context(), dataID)
		if err != nil {
			writeUpstream(w, err)
			return
		}
		for _, rev := range revs {
			if rev.ID == body.ID {
				if err := h.client.Publish(r.Context(), dataID, rev.Content); err != nil {
					writeUpstream(w, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "发布记录不存在"})
	case action == "beta" && r.Method == http.MethodGet:
		content, ips, err := h.client.Beta(r.Context(), dataID)
		if err != nil {
			writeUpstream(w, err)
			return
		}
		if ips == nil {
			ips = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"content": content, "ips": ips})
	case action == "beta" && r.Method == http.MethodPut:
		var body struct {
			Content string   `json:"content"`
			IPs     []string `json:"ips"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求正文无效"})
			return
		}
		if err := h.client.PublishBeta(r.Context(), dataID, body.Content, body.IPs); err != nil {
			writeUpstream(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case action == "beta" && r.Method == http.MethodDelete:
		if err := h.client.DeleteBeta(r.Context(), dataID); err != nil {
			writeUpstream(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case action == "listeners" && r.Method == http.MethodGet:
		items, err := h.client.Listeners(r.Context(), dataID)
		if err != nil {
			writeUpstream(w, err)
			return
		}
		if items == nil {
			items = []nacos.Listener{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"listeners": items})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Nacos 未打开"})
	}
}

func writeUpstream(w http.ResponseWriter, err error) {
	if err != nil && strings.Contains(err.Error(), "无效的灰度 IP") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的灰度 IP"})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Nacos 请求失败"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
