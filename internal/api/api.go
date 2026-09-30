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

	s.mux.HandleFunc("POST /schools/{schoolID}/members", s.createMember)
	s.mux.HandleFunc("GET /schools/{schoolID}/members", s.listMembers)
	s.mux.HandleFunc("GET /schools/{schoolID}/members/{memberID}", s.getMember)
	s.mux.HandleFunc("PATCH /schools/{schoolID}/members/{memberID}", s.updateMember)

	s.mux.HandleFunc("POST /schools/{schoolID}/campaigns", s.createCampaign)
	s.mux.HandleFunc("GET /schools/{schoolID}/campaigns", s.listCampaigns)
	s.mux.HandleFunc("GET /campaigns/{campaignID}", s.getCampaign)
	s.mux.HandleFunc("GET /campaigns/{campaignID}/live", s.live)
	s.mux.HandleFunc("POST /campaigns/{campaignID}/close", s.closeCampaign)
	s.mux.HandleFunc("POST /campaigns/{campaignID}/updates", s.postUpdate)
	s.mux.HandleFunc("POST /campaigns/{campaignID}/challenges", s.addChallenge)
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
	Open          bool    `json:"open"`
}

func viewCampaign(c lumni.Campaign) campaignView {
	return campaignView{Campaign: c, PercentFunded: c.PercentFunded(), Open: c.Open(time.Now())}
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

// --- members ---

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	var in lumni.Member
	if !decode(w, r, &in) {
		return
	}
	in.SchoolID = r.PathValue("schoolID")
	out, err := s.store.CreateMember(in)
	respond(w, http.StatusCreated, out, err)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := lumni.MemberFilter{
		Query: q.Get("q"), Kind: lumni.MemberKind(q.Get("kind")),
		Industry: q.Get("industry"), Location: q.Get("location"), Grade: q.Get("grade"),
	}
	if gy := q.Get("grad_year"); gy != "" {
		n, err := strconv.Atoi(gy)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "grad_year must be a number"})
			return
		}
		f.GradYear = n
	}
	out, err := s.store.ListMembers(r.PathValue("schoolID"), f)
	respond(w, http.StatusOK, out, err)
}

func (s *Server) getMember(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GetMember(r.PathValue("schoolID"), r.PathValue("memberID"))
	respond(w, http.StatusOK, out, err)
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	var p lumni.MemberPatch
	if !decode(w, r, &p) {
		return
	}
	out, err := s.store.UpdateMember(r.PathValue("schoolID"), r.PathValue("memberID"), p)
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

// liveView is the small payload a campaign page polls to update its numbers.
type liveView struct {
	RaisedCents   int64             `json:"raised_cents"`
	GoalCents     int64             `json:"goal_cents"`
	PercentFunded float64           `json:"percent_funded"`
	DonorCount    int               `json:"donor_count"`
	Open          bool              `json:"open"`
	Challenges    []lumni.Challenge `json:"challenges"`
	ByClass       []lumni.Standing  `json:"by_class"`
	ByGrade       []lumni.Standing  `json:"by_grade"`
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	c, err := s.store.GetCampaign(id)
	if err != nil {
		writeError(w, err)
		return
	}
	byClass, _ := s.store.Leaderboard(id, lumni.ByClass)
	byGrade, _ := s.store.Leaderboard(id, lumni.ByGrade)
	writeJSON(w, http.StatusOK, liveView{
		RaisedCents: c.RaisedCents, GoalCents: c.GoalCents, PercentFunded: c.PercentFunded(),
		DonorCount: c.DonorCount, Open: c.Open(time.Now()), Challenges: c.Challenges,
		ByClass: byClass, ByGrade: byGrade,
	})
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

func (s *Server) addChallenge(w http.ResponseWriter, r *http.Request) {
	var in lumni.Challenge
	if !decode(w, r, &in) {
		return
	}
	out, err := s.store.AddChallenge(r.PathValue("campaignID"), in)
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
	by := r.URL.Query().Get("by")
	if by == "" {
		by = lumni.ByClass
	}
	if by != lumni.ByClass && by != lumni.ByGrade {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "by must be class or grade"})
		return
	}
	out, err := s.store.Leaderboard(r.PathValue("campaignID"), by)
	respond(w, http.StatusOK, out, err)
}
