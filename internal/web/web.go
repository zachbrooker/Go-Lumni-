// Package web serves Lumni's server-rendered HTML pages on top of the store.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zachbrooker/go-lumni/internal/lumni"
	"github.com/zachbrooker/go-lumni/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Handler serves the HTML UI.
type Handler struct {
	store *store.Memory
	log   *slog.Logger
	mux   *http.ServeMux
	tmpl  map[string]*template.Template
}

// New builds the UI handler. It panics if the embedded templates fail to parse,
// which can only happen from a programming error.
func New(st *store.Memory, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{store: st, log: log, mux: http.NewServeMux(), tmpl: map[string]*template.Template{}}
	for _, page := range []string{"home", "school", "alumni", "campaign", "error"} {
		h.tmpl[page] = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+page+".html"))
	}
	static, _ := fs.Sub(staticFS, "static")
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	h.mux.HandleFunc("GET /{$}", h.home)
	h.mux.HandleFunc("POST /schools", h.createSchool)
	h.mux.HandleFunc("GET /s/{schoolID}", h.school)
	h.mux.HandleFunc("POST /s/{schoolID}/campaigns", h.createCampaign)
	h.mux.HandleFunc("GET /s/{schoolID}/alumni", h.alumni)
	h.mux.HandleFunc("POST /s/{schoolID}/alumni", h.createAlumnus)
	h.mux.HandleFunc("GET /c/{campaignID}", h.campaign)
	h.mux.HandleFunc("POST /c/{campaignID}/give", h.give)
	h.mux.HandleFunc("POST /c/{campaignID}/updates", h.postUpdate)
	h.mux.HandleFunc("POST /c/{campaignID}/close", h.closeCampaign)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

var funcs = template.FuncMap{
	"money":   money,
	"pct":     func(f float64) int { return int(math.Min(math.Round(f), 100)) },
	"pct1":    func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"date":    func(t time.Time) string { return t.Format("Jan 2, 2006") },
	"dateInp": func(t time.Time) string { return t.Format("2006-01-02") },
	"initial": func(s string) string {
		if s == "" {
			return "?"
		}
		return strings.ToUpper(s[:1])
	},
	"sortYears": sortedYears,
	"sub":       func(a, b int64) int64 { return a - b },
	"inc":       func(i int) int { return i + 1 },
	"mod":       func(a, b int) int { return a % b },
	"amounts":   func() []int { return []int{50, 100, 250, 1000} },
}

// money formats cents as dollars, dropping ".00" for whole amounts.
func money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	dollars, rem := cents/100, cents%100
	s := strconv.FormatInt(dollars, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if rem != 0 {
		s += fmt.Sprintf(".%02d", rem)
	}
	if neg {
		return "-$" + s
	}
	return "$" + s
}

type yearCount struct {
	Year  int
	Count int
}

func sortedYears(m map[int]int) []yearCount {
	out := make([]yearCount, 0, len(m))
	for y, n := range m {
		out = append(out, yearCount{y, n})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Year < out[j-1].Year; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// page is the data every template receives.
type page struct {
	Title string
	Error string // from ?err=, shown as a banner
	Data  any
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, status int, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	p := page{Title: title, Error: r.URL.Query().Get("err"), Data: data}
	if err := h.tmpl[name].ExecuteTemplate(w, "layout", p); err != nil {
		h.log.Error("render", "page", name, "err", err)
	}
}

// fail renders an error page with a status derived from err.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, lumni.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, lumni.ErrValidation):
		status = http.StatusBadRequest
	case errors.Is(err, lumni.ErrConflict):
		status = http.StatusConflict
	}
	h.render(w, r, "error", status, "Something went wrong", err.Error())
}

// back redirects to path, attaching err as a banner message when non-nil.
func back(w http.ResponseWriter, r *http.Request, path string, err error) {
	if err != nil {
		path += "?err=" + url.QueryEscape(err.Error())
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// dollarsToCents parses a form amount like "25" or "25.50".
func dollarsToCents(s string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	s = strings.ReplaceAll(s, ",", "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e12 {
		return 0, fmt.Errorf("%w: amount %q is not a valid dollar figure", lumni.ErrValidation, s)
	}
	return int64(math.Round(f * 100)), nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// --- pages ---

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "home", http.StatusOK, "Lumni", h.store.ListSchools())
}

func (h *Handler) createSchool(w http.ResponseWriter, r *http.Request) {
	s, err := h.store.CreateSchool(lumni.School{Name: r.FormValue("name")})
	if err != nil {
		back(w, r, "/", err)
		return
	}
	back(w, r, "/s/"+s.ID, nil)
}

type schoolPage struct {
	School    lumni.School
	Insights  lumni.Insights
	Campaigns []lumni.Campaign
	Today     time.Time
}

func (h *Handler) school(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	s, err := h.store.GetSchool(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	in, _ := h.store.Insights(id)
	cs, _ := h.store.ListCampaigns(id)
	h.render(w, r, "school", http.StatusOK, s.Name, schoolPage{School: s, Insights: in, Campaigns: cs, Today: time.Now()})
}

func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	c := lumni.Campaign{SchoolID: id, Title: r.FormValue("title"), Story: r.FormValue("story")}
	var err error
	if c.GoalCents, err = dollarsToCents(r.FormValue("goal")); err != nil {
		back(w, r, "/s/"+id, err)
		return
	}
	if d := r.FormValue("deadline"); d != "" {
		if c.Deadline, err = time.Parse("2006-01-02", d); err != nil {
			back(w, r, "/s/"+id, fmt.Errorf("%w: deadline must be YYYY-MM-DD", lumni.ErrValidation))
			return
		}
		c.Deadline = c.Deadline.Add(24*time.Hour - time.Second) // end of day
	}
	if sponsor := strings.TrimSpace(r.FormValue("match_sponsor")); sponsor != "" {
		capCents, err := dollarsToCents(r.FormValue("match_cap"))
		if err != nil {
			back(w, r, "/s/"+id, err)
			return
		}
		c.Match = &lumni.Match{Sponsor: sponsor, CapCents: capCents}
	}
	out, err := h.store.CreateCampaign(c)
	if err != nil {
		back(w, r, "/s/"+id, err)
		return
	}
	back(w, r, "/c/"+out.ID, nil)
}

type alumniPage struct {
	School lumni.School
	Filter lumni.AlumniFilter
	Alumni []lumni.Alumnus
}

func (h *Handler) alumni(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	s, err := h.store.GetSchool(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	f := lumni.AlumniFilter{Query: q.Get("q"), Industry: q.Get("industry"), Location: q.Get("location"), GradYear: atoi(q.Get("grad_year"))}
	list, _ := h.store.ListAlumni(id, f)
	h.render(w, r, "alumni", http.StatusOK, s.Name+" · Alumni", alumniPage{School: s, Filter: f, Alumni: list})
}

func (h *Handler) createAlumnus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	a := lumni.Alumnus{
		SchoolID: id,
		Name:     r.FormValue("name"),
		Email:    r.FormValue("email"),
		GradYear: atoi(r.FormValue("grad_year")),
		Employer: r.FormValue("employer"),
		Title:    r.FormValue("title"),
		Industry: r.FormValue("industry"),
		Location: r.FormValue("location"),
	}
	_, err := h.store.CreateAlumnus(a)
	back(w, r, "/s/"+id+"/alumni", err)
}

type campaignPage struct {
	School      lumni.School
	Campaign    lumni.Campaign
	Open        bool
	Donations   []lumni.Donation
	Leaderboard []lumni.ClassStanding
	Alumni      []lumni.Alumnus // for the "I'm an alum" picker
}

func (h *Handler) campaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	c, err := h.store.GetCampaign(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, _ := h.store.GetSchool(c.SchoolID)
	ds, _ := h.store.ListDonations(id)
	lb, _ := h.store.Leaderboard(id)
	al, _ := h.store.ListAlumni(c.SchoolID, lumni.AlumniFilter{})
	h.render(w, r, "campaign", http.StatusOK, c.Title, campaignPage{
		School: s, Campaign: c, Open: c.Open(time.Now()), Donations: ds, Leaderboard: lb, Alumni: al,
	})
}

func (h *Handler) give(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	amount, err := dollarsToCents(r.FormValue("amount"))
	if err != nil {
		back(w, r, "/c/"+id, err)
		return
	}
	d := lumni.Donation{
		AlumnusID:   r.FormValue("alumnus_id"),
		DonorName:   r.FormValue("donor_name"),
		AmountCents: amount,
		Message:     strings.TrimSpace(r.FormValue("message")),
		Anonymous:   r.FormValue("anonymous") != "",
	}
	_, err = h.store.Donate(id, d)
	back(w, r, "/c/"+id, err)
}

func (h *Handler) postUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	_, err := h.store.PostUpdate(id, r.FormValue("body"))
	back(w, r, "/c/"+id, err)
}

func (h *Handler) closeCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	_, err := h.store.CloseCampaign(id)
	back(w, r, "/c/"+id, err)
}
