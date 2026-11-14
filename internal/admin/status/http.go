package status

import (
	"encoding/json"
	"net/http"
)

// View is what the status pages encode. Overview reads containers through
// BuilderURL. Cluster reads the etcd snapshot from Source.
type View struct {
	BuilderURL string
	Health     Health
	Source     Source
}

// NewHandler serves GET /api/overview and GET /api/cluster.
// It encodes Collect and Source.Cluster. A builder miss stays in Collect
// and still returns every service row.
func NewHandler(view View) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, Collect(r.Context(), view.BuilderURL, view.Health, readCluster(r, view)))
	})
	mux.HandleFunc("GET /api/cluster", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, readCluster(r, view))
	})
	return mux
}

func readCluster(r *http.Request, view View) Cluster {
	if view.Source == nil {
		return Cluster{Status: "fault", Error: ErrEtcd.Error()}
	}
	snap, err := view.Source.Cluster(r.Context())
	if err != nil && snap.Status == "" {
		return Cluster{Status: "fault", Error: err.Error()}
	}
	return snap
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
