// Package api exposes Lumni over a JSON HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/zachbrooker/go-lumni/internal/lumni"
	"github.com/zachbrooker/go-lumni/internal/store"
)

const maxBodyBytes = 1 << 20

// Server serves the Lumni API.
type Server struct {
	store *store.Memory
	log   *slog.Logger
	mux   *http.ServeMux
}

// New builds a Server backed by st.
func New(st *store.Memory, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{store: st, log: log, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	s.mux.HandleFunc("POST /schools", s.createSchool)
	s.mux.HandleFunc("GET /schools", s.listSchools)
	s.mux.HandleFunc("GET /schools/{schoolID}", s.getSchool)
	s.mux.HandleFunc("GET /schools/{schoolID}/insights", s.insights)

	s.mux.HandleFunc("POST /schools/{schoolID}/alumni", s.createAlumnus)
	s.mux.HandleFunc("GET /schools/{schoolID}/alumni", s.listAlumni)
	s.mux.HandleFunc("GET /schools/{schoolID}/alumni/{alumnusID}", s.getAlumnus)
	s.mux.HandleFunc("PATCH /schools/{schoolID}/alumni/{alumnusID}", s.updateAlumnus)

	s.mux.HandleFunc("POST /schools/{schoolID}/campaigns", s.createCampaign)
	s.mux.HandleFunc("GET /schools/{schoolID}/campaigns", s.listCampaigns)
	s.mux.HandleFunc("GET /campaigns/{campaignID}", s.getCampaign)
	s.mux.HandleFunc("POST /campaigns/{campaignID}/close", s.closeCampaign)
	s.mux.HandleFunc("POST /campaigns/{campaignID}/updates", s.postUpdate)
	s.mux.HandleFunc("POST /campaigns/{campaignID}/donations", s.donate)
	s.mux.HandleFunc("GET /campaigns/{campaignID}/donations", s.listDonations)
	s.mux.HandleFunc("GET /campaigns/{campaignID}/leaderboard", s.leaderboard)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	s.mux.ServeHTTP(sw, r)
	s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, lumni.ErrValidation):
		status = http.StatusBadRequest
	case errors.Is(err, lumni.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, lumni.ErrConflict):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	return true
}

// campaignView adds computed fields to a campaign response.
type campaignView struct {
	lumni.Campaign
	PercentFunded float64 `json:"percent_funded"`
}

func viewCampaign(c lumni.Campaign) campaignView {
	return campaignView{Campaign: c, PercentFunded: c.PercentFunded()}
}

func respond[T any](w http.ResponseWriter, status int, v T, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, v)
}

// --- schools ---

func (s *Server) createSchool(w http.ResponseWriter, r *http.Request) {
	var in lumni.School
	if !decode(w, r, &in) {
		return
	}
	out, err := s.store.CreateSchool(in)
	respond(w, http.StatusCreated, out, err)
}

func (s *Server) listSchools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListSchools())
}

func (s *Server) getSchool(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GetSchool(r.PathValue("schoolID"))
	respond(w, http.StatusOK, out, err)
}

func (s *Server) insights(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Insights(r.PathValue("schoolID"))
	respond(w, http.StatusOK, out, err)
}

// --- alumni ---

func (s *Server) createAlumnus(w http.ResponseWriter, r *http.Request) {
	var in lumni.Alumnus
	if !decode(w, r, &in) {
		return
	}
	in.SchoolID = r.PathValue("schoolID")
	out, err := s.store.CreateAlumnus(in)
	respond(w, http.StatusCreated, out, err)
}

func (s *Server) listAlumni(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := lumni.AlumniFilter{Query: q.Get("q"), Industry: q.Get("industry"), Location: q.Get("location")}
	if gy := q.Get("grad_year"); gy != "" {
		n, err := strconv.Atoi(gy)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "grad_year must be a number"})
			return
		}
		f.GradYear = n
	}
	out, err := s.store.ListAlumni(r.PathValue("schoolID"), f)
	respond(w, http.StatusOK, out, err)
}

func (s *Server) getAlumnus(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GetAlumnus(r.PathValue("schoolID"), r.PathValue("alumnusID"))
	respond(w, http.StatusOK, out, err)
}

func (s *Server) updateAlumnus(w http.ResponseWriter, r *http.Request) {
	var p lumni.AlumnusPatch
	if !decode(w, r, &p) {
		return
	}
	out, err := s.store.UpdateAlumnus(r.PathValue("schoolID"), r.PathValue("alumnusID"), p)
	respond(w, http.StatusOK, out, err)
}

// --- campaigns ---

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	var in lumni.Campaign
	if !decode(w, r, &in) {
		return
	}
	in.SchoolID = r.PathValue("schoolID")
	out, err := s.store.CreateCampaign(in)
	respond(w, http.StatusCreated, viewCampaign(out), err)
}

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	cs, err := s.store.ListCampaigns(r.PathValue("schoolID"))
	out := make([]campaignView, len(cs))
	for i, c := range cs {
		out[i] = viewCampaign(c)
	}
	respond(w, http.StatusOK, out, err)
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GetCampaign(r.PathValue("campaignID"))
	respond(w, http.StatusOK, viewCampaign(out), err)
}

func (s *Server) closeCampaign(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.CloseCampaign(r.PathValue("campaignID"))
	respond(w, http.StatusOK, viewCampaign(out), err)
}

func (s *Server) postUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.store.PostUpdate(r.PathValue("campaignID"), in.Body)
	respond(w, http.StatusCreated, viewCampaign(out), err)
}

func (s *Server) donate(w http.ResponseWriter, r *http.Request) {
	var in lumni.Donation
	if !decode(w, r, &in) {
		return
	}
	out, err := s.store.Donate(r.PathValue("campaignID"), in)
	respond(w, http.StatusCreated, out, err)
}

func (s *Server) listDonations(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListDonations(r.PathValue("campaignID"))
	respond(w, http.StatusOK, out, err)
}

func (s *Server) leaderboard(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Leaderboard(r.PathValue("campaignID"))
	respond(w, http.StatusOK, out, err)
}
