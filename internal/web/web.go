// Package web serves Lumni's server-rendered HTML pages on top of the store.
package web

import (
	"embed"
	"encoding/csv"
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
	for _, page := range []string{"home", "school", "people", "gifts", "campaign", "give", "me", "error"} {
		h.tmpl[page] = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+page+".html"))
	}
	static, _ := fs.Sub(staticFS, "static")
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	h.mux.HandleFunc("GET /{$}", h.home)
	h.mux.HandleFunc("POST /schools", h.createSchool)

	// School-facing pages.
	h.mux.HandleFunc("GET /s/{schoolID}", h.school)
	h.mux.HandleFunc("POST /s/{schoolID}/campaigns", h.createCampaign)
	h.mux.HandleFunc("GET /s/{schoolID}/people", h.people)
	h.mux.HandleFunc("GET /s/{schoolID}/people.csv", h.peopleCSV)
	h.mux.HandleFunc("POST /s/{schoolID}/people", h.createMember)
	h.mux.HandleFunc("GET /s/{schoolID}/gifts", h.gifts)
	h.mux.HandleFunc("GET /s/{schoolID}/gifts.csv", h.giftsCSV)

	// Community-facing pages.
	h.mux.HandleFunc("GET /c/{campaignID}", h.campaign)
	h.mux.HandleFunc("GET /give/{campaignID}", h.give)
	h.mux.HandleFunc("POST /give/{campaignID}", h.donate)
	h.mux.HandleFunc("POST /c/{campaignID}/updates", h.postUpdate)
	h.mux.HandleFunc("POST /c/{campaignID}/challenges", h.addChallenge)
	h.mux.HandleFunc("POST /c/{campaignID}/close", h.closeCampaign)
	h.mux.HandleFunc("GET /me/{memberID}", h.me)
	h.mux.HandleFunc("POST /me/{memberID}", h.updateMe)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

var funcs = template.FuncMap{
	"money": money,
	"pct":   func(f float64) int { return int(math.Min(math.Round(f), 100)) },
	"pct1":  func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"pct0":  func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) },
	"date":  func(t time.Time) string { return t.Format("Jan 2, 2006") },
	"dt":    func(t time.Time) string { return t.Format("Jan 2, 2006 3:04 PM") },
	"dtInp": func(t time.Time) string { return t.Format("2006-01-02T15:04") },
	"initial": func(s string) string {
		if s == "" {
			return "?"
		}
		return strings.ToUpper(s[:1])
	},
	"sub":       func(a, b int64) int64 { return a - b },
	"inc":       func(i int) int { return i + 1 },
	"mod":       func(a, b int) int { return a % b },
	"amounts":   func() []int { return []int{100, 250, 1000, 5000} },
	"kinds":     func() []lumni.MemberKind { return lumni.Kinds },
	"grades":    func() []string { return lumni.Grades },
	"kindCount": func(m map[lumni.MemberKind]int, k lumni.MemberKind) int { return m[k] },
	"kindCents": func(m map[lumni.MemberKind]int64, k lumni.MemberKind) int64 { return m[k] },
	"threshold": func(c lumni.Challenge) string {
		if c.Metric == lumni.MetricDollars {
			return money(c.Threshold)
		}
		return strconv.FormatInt(c.Threshold, 10) + " donors"
	},
	"progress": func(c lumni.Challenge) string {
		if c.Metric == lumni.MetricDollars {
			return money(c.Progress)
		}
		return strconv.FormatInt(c.Progress, 10)
	},
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

// page is the data every template receives.
type page struct {
	Title string
	Error string // from ?err=, shown as a banner
	OK    string // from ?ok=, shown as a success banner
	Data  any
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, status int, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	p := page{Title: title, Error: r.URL.Query().Get("err"), OK: r.URL.Query().Get("ok"), Data: data}
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

// back redirects to path, attaching err as an error banner when non-nil,
// or ok as a success banner otherwise.
func back(w http.ResponseWriter, r *http.Request, path string, err error, ok string) {
	switch {
	case err != nil:
		path += "?err=" + url.QueryEscape(err.Error())
	case ok != "":
		path += "?ok=" + url.QueryEscape(ok)
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// dollarsToCents parses a form amount like "25", "$1,000" or "25.50".
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

// parseLocalTime reads a datetime-local form value; empty is zero time.
func parseLocalTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q is not a date", lumni.ErrValidation, s)
}

// --- home ---

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "home", http.StatusOK, "Lumni", h.store.ListSchools())
}

func (h *Handler) createSchool(w http.ResponseWriter, r *http.Request) {
	s, err := h.store.CreateSchool(lumni.School{Name: r.FormValue("name")})
	if err != nil {
		back(w, r, "/", err, "")
		return
	}
	back(w, r, "/s/"+s.ID, nil, "")
}

// --- school dashboard ---

type schoolPage struct {
	School    lumni.School
	Insights  lumni.Insights
	Campaigns []lumni.Campaign
	Recent    []lumni.Donation // latest gifts, unredacted, for the office
	Major     []lumni.Donation // gifts over the major-gift threshold
	Now       time.Time
	Base      string // scheme://host, for share links
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
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
	ds, _ := h.store.SchoolDonations(id)
	var major []lumni.Donation
	for _, d := range ds {
		if d.Major() {
			major = append(major, d)
		}
	}
	if len(ds) > 8 {
		ds = ds[:8]
	}
	h.render(w, r, "school", http.StatusOK, s.Name, schoolPage{
		School: s, Insights: in, Campaigns: cs, Recent: ds, Major: major, Now: time.Now(), Base: baseURL(r),
	})
}

func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	c := lumni.Campaign{SchoolID: id, Kind: r.FormValue("kind"), Title: r.FormValue("title"), Story: r.FormValue("story")}
	var err error
	if c.GoalCents, err = dollarsToCents(r.FormValue("goal")); err != nil {
		back(w, r, "/s/"+id, err, "")
		return
	}
	if c.StartsAt, err = parseLocalTime(r.FormValue("starts_at")); err != nil {
		back(w, r, "/s/"+id, err, "")
		return
	}
	if c.Deadline, err = parseLocalTime(r.FormValue("deadline")); err != nil {
		back(w, r, "/s/"+id, err, "")
		return
	}
	if sponsor := strings.TrimSpace(r.FormValue("match_sponsor")); sponsor != "" {
		capCents, err := dollarsToCents(r.FormValue("match_cap"))
		if err != nil {
			back(w, r, "/s/"+id, err, "")
			return
		}
		c.Match = &lumni.Match{Sponsor: sponsor, CapCents: capCents}
	}
	out, err := h.store.CreateCampaign(c)
	if err != nil {
		back(w, r, "/s/"+id, err, "")
		return
	}
	back(w, r, "/c/"+out.ID, nil, "Campaign launched. Share the give link below.")
}

// --- people (the community directory) ---

type peoplePage struct {
	School lumni.School
	Filter lumni.MemberFilter
	People []lumni.Member
	Base   string
}

func filterFromQuery(q url.Values) lumni.MemberFilter {
	return lumni.MemberFilter{
		Query: q.Get("q"), Kind: lumni.MemberKind(q.Get("kind")), Industry: q.Get("industry"),
		Location: q.Get("location"), GradYear: atoi(q.Get("grad_year")), Grade: q.Get("grade"),
	}
}

func (h *Handler) people(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	s, err := h.store.GetSchool(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f := filterFromQuery(r.URL.Query())
	list, _ := h.store.ListMembers(id, f)
	h.render(w, r, "people", http.StatusOK, s.Name+" · People", peoplePage{School: s, Filter: f, People: list, Base: baseURL(r)})
}

// peopleCSV exports the directory (honoring the same filters) for the office.
func (h *Handler) peopleCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	list, err := h.store.ListMembers(id, filterFromQuery(r.URL.Query()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="people.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"name", "email", "kind", "class_of", "grade", "title", "employer", "industry", "location", "bio", "profile_link"})
	for _, m := range list {
		year := ""
		if m.GradYear != 0 {
			year = strconv.Itoa(m.GradYear)
		}
		_ = cw.Write([]string{m.Name, m.Email, m.Kind.Label(), year, m.Grade, m.Title, m.Employer, m.Industry, m.Location, m.Bio, baseURL(r) + "/me/" + m.ID})
	}
	cw.Flush()
}

func (h *Handler) createMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("schoolID")
	m := lumni.Member{
		SchoolID: id,
		Kind:     lumni.MemberKind(r.FormValue("kind")),
		Name:     r.FormValue("name"),
		Email:    r.FormValue("email"),
		GradYear: atoi(r.FormValue("grad_year")),
		Grade:    r.FormValue("grade"),
		Employer: r.FormValue("employer"),
		Title:    r.FormValue("title"),
		Industry: r.FormValue("industry"),
		Location: r.FormValue("location"),
	}
	_, err := h.store.CreateMember(m)
	back(w, r, "/s/"+id+"/people", err, "Added.")
}

// --- gifts ledger ---

type giftsPage struct {
	School    lumni.School
	Gifts     []lumni.Donation
	Campaigns map[string]string // id → title
	Total     int64
	Fees      int64
}

func (h *Handler) giftsData(id string) (giftsPage, error) {
	s, err := h.store.GetSchool(id)
	if err != nil {
		return giftsPage{}, err
	}
	ds, _ := h.store.SchoolDonations(id)
	cs, _ := h.store.ListCampaigns(id)
	p := giftsPage{School: s, Gifts: ds, Campaigns: map[string]string{}}
	for _, c := range cs {
		p.Campaigns[c.ID] = c.Title
	}
	for _, d := range ds {
		p.Total += d.AmountCents
		p.Fees += d.FeeCents
	}
	return p, nil
}

func (h *Handler) gifts(w http.ResponseWriter, r *http.Request) {
	p, err := h.giftsData(r.PathValue("schoolID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, "gifts", http.StatusOK, p.School.Name+" · Gifts", p)
}

func (h *Handler) giftsCSV(w http.ResponseWriter, r *http.Request) {
	p, err := h.giftsData(r.PathValue("schoolID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gifts.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "donor", "email", "kind", "campaign", "amount", "matched", "major_gift", "anonymous", "message"})
	for _, d := range p.Gifts {
		_ = cw.Write([]string{
			d.CreatedAt.Format(time.RFC3339), d.DonorName, d.DonorEmail, d.DonorKind, p.Campaigns[d.CampaignID],
			strconv.FormatFloat(float64(d.AmountCents)/100, 'f', 2, 64), strconv.FormatFloat(float64(d.MatchedCents)/100, 'f', 2, 64),
			strconv.FormatBool(d.Major()), strconv.FormatBool(d.Anonymous), d.Message,
		})
	}
	cw.Flush()
}

// --- campaign page ---

type campaignPage struct {
	School    lumni.School
	Campaign  lumni.Campaign
	Open      bool
	Started   bool
	Donations []lumni.Donation
	ByClass   []lumni.Standing
	ByGrade   []lumni.Standing
	Base      string
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
	byClass, _ := h.store.Leaderboard(id, lumni.ByClass)
	byGrade, _ := h.store.Leaderboard(id, lumni.ByGrade)
	now := time.Now()
	h.render(w, r, "campaign", http.StatusOK, c.Title, campaignPage{
		School: s, Campaign: c, Open: c.Open(now), Started: c.Started(now),
		Donations: ds, ByClass: byClass, ByGrade: byGrade, Base: baseURL(r),
	})
}

func (h *Handler) postUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	_, err := h.store.PostUpdate(id, r.FormValue("body"))
	back(w, r, "/c/"+id, err, "Update posted.")
}

func (h *Handler) addChallenge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	ch := lumni.Challenge{
		Name: r.FormValue("name"), Sponsor: r.FormValue("sponsor"),
		Segment: lumni.Segment(r.FormValue("segment")), Metric: r.FormValue("metric"),
	}
	var err error
	if ch.Metric == lumni.MetricDollars {
		ch.Threshold, err = dollarsToCents(r.FormValue("threshold"))
	} else {
		ch.Threshold = int64(atoi(r.FormValue("threshold")))
	}
	if err != nil {
		back(w, r, "/c/"+id, err, "")
		return
	}
	if ch.RewardCents, err = dollarsToCents(r.FormValue("reward")); err != nil {
		back(w, r, "/c/"+id, err, "")
		return
	}
	_, err = h.store.AddChallenge(id, ch)
	back(w, r, "/c/"+id, err, "Challenge added.")
}

func (h *Handler) closeCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	_, err := h.store.CloseCampaign(id)
	back(w, r, "/c/"+id, err, "Campaign closed.")
}

// --- give (the shareable, one-screen gift page) ---

type givePage struct {
	School   lumni.School
	Campaign lumni.Campaign
	Open     bool
	Amount   string // preset from ?amount=
	Email    string // preset from ?email=
}

func (h *Handler) give(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	c, err := h.store.GetCampaign(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, _ := h.store.GetSchool(c.SchoolID)
	q := r.URL.Query()
	h.render(w, r, "give", http.StatusOK, "Give to "+s.Name, givePage{
		School: s, Campaign: c, Open: c.Open(time.Now()), Amount: q.Get("amount"), Email: q.Get("email"),
	})
}

func (h *Handler) donate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("campaignID")
	amount, err := dollarsToCents(r.FormValue("amount"))
	if err != nil {
		back(w, r, "/give/"+id, err, "")
		return
	}
	d := lumni.Donation{
		MemberID:    r.FormValue("member_id"),
		DonorName:   r.FormValue("donor_name"),
		DonorEmail:  r.FormValue("donor_email"),
		AmountCents: amount,
		Message:     strings.TrimSpace(r.FormValue("message")),
		Anonymous:   r.FormValue("anonymous") != "",
	}
	out, err := h.store.Donate(id, d)
	if err != nil {
		back(w, r, "/give/"+id, err, "")
		return
	}
	msg := "Thank you! Your gift of " + money(out.AmountCents) + " is in."
	if out.Major() {
		msg += " The advancement office will reach out personally."
	}
	back(w, r, "/c/"+id, nil, msg)
}

// --- me (a member updates their own profile from a shared link) ---

type mePage struct {
	School lumni.School
	Member lumni.Member
}

func (h *Handler) findMember(id string) (lumni.Member, lumni.School, error) {
	for _, s := range h.store.ListSchools() {
		if m, err := h.store.GetMember(s.ID, id); err == nil {
			return m, s, nil
		}
	}
	return lumni.Member{}, lumni.School{}, fmt.Errorf("profile %q: %w", id, lumni.ErrNotFound)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	m, s, err := h.findMember(r.PathValue("memberID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, "me", http.StatusOK, "Your "+s.Name+" profile", mePage{School: s, Member: m})
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("memberID")
	m, _, err := h.findMember(id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	str := func(k string) *string { v := r.FormValue(k); return &v }
	p := lumni.MemberPatch{
		Title: str("title"), Employer: str("employer"), Industry: str("industry"),
		Location: str("location"), Bio: str("bio"),
	}
	if v := strings.TrimSpace(r.FormValue("name")); v != "" {
		p.Name = &v
	}
	_, err = h.store.UpdateMember(m.SchoolID, id, p)
	back(w, r, "/me/"+id, err, "Saved. Thanks for keeping "+m.Name+"'s profile current.")
}
