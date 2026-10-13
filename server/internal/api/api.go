// Package api serves the human-facing REST + SSE endpoints and the built spectator UI.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentswars/internal/mcp"
	"agentswars/internal/room"
	"agentswars/internal/sim"
)

func Handler(m *room.Manager, webDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.New(m))

	mux.HandleFunc("POST /api/rooms", func(w http.ResponseWriter, r *http.Request) {
		var cfg room.Config
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
				jsonErr(w, 400, "bad json")
				return
			}
		}
		rm, err := m.Create(cfg)
		if err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 201, struct {
			room.Info
			HostToken string `json:"host_token"`
		}{rm.Info(), rm.HostToken})
	})

	mux.HandleFunc("GET /api/rooms/{code}", func(w http.ResponseWriter, r *http.Request) {
		rm := m.Get(strings.ToUpper(r.PathValue("code")))
		if rm == nil {
			jsonErr(w, 404, "room not found")
			return
		}
		writeJSON(w, 200, rm.Info())
	})

	mux.HandleFunc("POST /api/rooms/{code}/start", func(w http.ResponseWriter, r *http.Request) {
		rm := m.Get(strings.ToUpper(r.PathValue("code")))
		if rm == nil {
			jsonErr(w, 404, "room not found")
			return
		}
		if !rm.CheckHost(r.Header.Get("X-Host-Token")) {
			jsonErr(w, 403, "only the room host can start the match")
			return
		}
		if err := rm.Start(); err != nil {
			jsonErr(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, rm.Info())
	})

	// The human trainer sets up an agent's build (persona, position, skill concepts) before kickoff.
	mux.HandleFunc("PUT /api/rooms/{code}/slots/{team}/{index}/brief", func(w http.ResponseWriter, r *http.Request) {
		rm := m.Get(strings.ToUpper(r.PathValue("code")))
		if rm == nil {
			jsonErr(w, 404, "room not found")
			return
		}
		if !rm.CheckHost(r.Header.Get("X-Host-Token")) {
			jsonErr(w, 403, "only the room host can edit briefs")
			return
		}
		team, e1 := strconv.Atoi(r.PathValue("team"))
		idx, e2 := strconv.Atoi(r.PathValue("index"))
		var b sim.Brief
		if e1 != nil || e2 != nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&b) != nil {
			jsonErr(w, 400, "bad request")
			return
		}
		if err := rm.SetBrief(team, idx, b); err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, rm.Info())
	})

	mux.HandleFunc("GET /api/rooms/{code}/benchmark", func(w http.ResponseWriter, r *http.Request) {
		rm := m.Get(strings.ToUpper(r.PathValue("code")))
		if rm == nil {
			jsonErr(w, 404, "room not found")
			return
		}
		writeJSON(w, 200, map[string]any{"room": rm.Code, "bench": rm.Bench(),
			"note": "calls/turnaround/reaction/authoring are measured by the server; tokens_per_sec, provider and model are self-reported by the agent via report_usage"})
	})

	mux.HandleFunc("GET /api/rooms/{code}/stream", func(w http.ResponseWriter, r *http.Request) {
		rm := m.Get(strings.ToUpper(r.PathValue("code")))
		if rm == nil {
			jsonErr(w, 404, "room not found")
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			jsonErr(w, 500, "streaming unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		ch, cancel := rm.Subscribe()
		defer cancel()
		keep := time.NewTicker(15 * time.Second)
		defer keep.Stop()
		for {
			select {
			case b := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			case <-keep.C:
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	if webDir != "" {
		if st, err := os.Stat(webDir); err == nil && st.IsDir() {
			mux.Handle("/", spa(webDir))
		}
	}
	return cors(mux)
}

func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(p); err != nil {
			r.URL.Path = "/"
		}
		fs.ServeHTTP(w, r)
	})
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Host-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
