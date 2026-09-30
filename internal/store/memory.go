// Package store provides an in-memory, concurrency-safe store for Lumni.
// It is intended for the concept stage; a SQL-backed store can replace it
// behind the same methods later.
package store

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zachbrooker/go-lumni/internal/lumni"
)

// Memory is an in-memory store. The zero value is not usable; call NewMemory.
type Memory struct {
	mu        sync.RWMutex
	now       func() time.Time
	schools   map[string]*lumni.School
	alumni    map[string]*lumni.Alumnus
	campaigns map[string]*lumni.Campaign
	donations map[string][]*lumni.Donation // by campaign ID
}

// NewMemory returns an empty store. now may be nil to use time.Now.
func NewMemory(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{
		now:       now,
		schools:   map[string]*lumni.School{},
		alumni:    map[string]*lumni.Alumnus{},
		campaigns: map[string]*lumni.Campaign{},
		donations: map[string][]*lumni.Donation{},
	}
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func notFound(kind, id string) error {
	return fmt.Errorf("%s %q: %w", kind, id, lumni.ErrNotFound)
}

// --- Schools ---

func (m *Memory) CreateSchool(s lumni.School) (lumni.School, error) {
	if err := s.Validate(); err != nil {
		return lumni.School{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.ID = newID("sch")
	s.CreatedAt = m.now()
	m.schools[s.ID] = &s
	return s, nil
}

func (m *Memory) GetSchool(id string) (lumni.School, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.schools[id]
	if !ok {
		return lumni.School{}, notFound("school", id)
	}
	return *s, nil
}

func (m *Memory) ListSchools() []lumni.School {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]lumni.School, 0, len(m.schools))
	for _, s := range m.schools {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b lumni.School) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// --- Alumni ---

func (m *Memory) emailTaken(schoolID, email, exceptID string) bool {
	for _, a := range m.alumni {
		if a.SchoolID == schoolID && a.Email == email && a.ID != exceptID {
			return true
		}
	}
	return false
}

func (m *Memory) CreateAlumnus(a lumni.Alumnus) (lumni.Alumnus, error) {
	if err := a.Validate(); err != nil {
		return lumni.Alumnus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schools[a.SchoolID]; !ok {
		return lumni.Alumnus{}, notFound("school", a.SchoolID)
	}
	if m.emailTaken(a.SchoolID, a.Email, "") {
		return lumni.Alumnus{}, fmt.Errorf("email %q already registered: %w", a.Email, lumni.ErrConflict)
	}
	a.ID = newID("alm")
	a.CreatedAt = m.now()
	a.UpdatedAt = a.CreatedAt
	m.alumni[a.ID] = &a
	return a, nil
}

func (m *Memory) GetAlumnus(schoolID, id string) (lumni.Alumnus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.alumni[id]
	if !ok || a.SchoolID != schoolID {
		return lumni.Alumnus{}, notFound("alumnus", id)
	}
	return *a, nil
}

func (m *Memory) UpdateAlumnus(schoolID, id string, p lumni.AlumnusPatch) (lumni.Alumnus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.alumni[id]
	if !ok || cur.SchoolID != schoolID {
		return lumni.Alumnus{}, notFound("alumnus", id)
	}
	next := *cur
	p.Apply(&next)
	if err := next.Validate(); err != nil {
		return lumni.Alumnus{}, err
	}
	if m.emailTaken(schoolID, next.Email, id) {
		return lumni.Alumnus{}, fmt.Errorf("email %q already registered: %w", next.Email, lumni.ErrConflict)
	}
	next.UpdatedAt = m.now()
	*cur = next
	return next, nil
}

func eqFold(filter, v string) bool { return filter == "" || strings.EqualFold(filter, v) }

// ListAlumni returns a school's alumni matching f, sorted by grad year then name.
func (m *Memory) ListAlumni(schoolID string, f lumni.AlumniFilter) ([]lumni.Alumnus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.schools[schoolID]; !ok {
		return nil, notFound("school", schoolID)
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := []lumni.Alumnus{}
	for _, a := range m.alumni {
		if a.SchoolID != schoolID || !eqFold(f.Industry, a.Industry) || !eqFold(f.Location, a.Location) {
			continue
		}
		if f.GradYear != 0 && f.GradYear != a.GradYear {
			continue
		}
		if q != "" {
			hay := strings.ToLower(strings.Join([]string{a.Name, a.Employer, a.Title, a.Bio}, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, *a)
	}
	slices.SortFunc(out, func(x, y lumni.Alumnus) int {
		return cmp.Or(cmp.Compare(x.GradYear, y.GradYear), cmp.Compare(x.Name, y.Name))
	})
	return out, nil
}

// --- Campaigns ---

func cloneCampaign(c *lumni.Campaign) lumni.Campaign {
	out := *c
	if c.Match != nil {
		mt := *c.Match
		out.Match = &mt
	}
	out.Updates = slices.Clone(c.Updates)
	if out.Updates == nil {
		out.Updates = []lumni.Update{}
	}
	return out
}

func (m *Memory) CreateCampaign(c lumni.Campaign) (lumni.Campaign, error) {
	if err := c.Validate(); err != nil {
		return lumni.Campaign{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schools[c.SchoolID]; !ok {
		return lumni.Campaign{}, notFound("school", c.SchoolID)
	}
	c.ID = newID("cmp")
	c.Status = lumni.StatusActive
	c.RaisedCents, c.DonorCount, c.Updates = 0, 0, nil
	c.CreatedAt = m.now()
	m.campaigns[c.ID] = &c
	return cloneCampaign(&c), nil
}

func (m *Memory) GetCampaign(id string) (lumni.Campaign, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.campaigns[id]
	if !ok {
		return lumni.Campaign{}, notFound("campaign", id)
	}
	return cloneCampaign(c), nil
}

func (m *Memory) ListCampaigns(schoolID string) ([]lumni.Campaign, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.schools[schoolID]; !ok {
		return nil, notFound("school", schoolID)
	}
	out := []lumni.Campaign{}
	for _, c := range m.campaigns {
		if c.SchoolID == schoolID {
			out = append(out, cloneCampaign(c))
		}
	}
	slices.SortFunc(out, func(a, b lumni.Campaign) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func (m *Memory) PostUpdate(campaignID, body string) (lumni.Campaign, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return lumni.Campaign{}, fmt.Errorf("%w: body is required", lumni.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.campaigns[campaignID]
	if !ok {
		return lumni.Campaign{}, notFound("campaign", campaignID)
	}
	c.Updates = append(c.Updates, lumni.Update{Body: body, CreatedAt: m.now()})
	return cloneCampaign(c), nil
}

func (m *Memory) CloseCampaign(campaignID string) (lumni.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.campaigns[campaignID]
	if !ok {
		return lumni.Campaign{}, notFound("campaign", campaignID)
	}
	c.Status = lumni.StatusClosed
	return cloneCampaign(c), nil
}

// --- Donations ---

// Donate records a gift and applies any sponsor match that has room left.
// When AlumnusID is set, the donor name defaults to the alumnus's name.
func (m *Memory) Donate(campaignID string, d lumni.Donation) (lumni.Donation, error) {
	if err := d.Validate(); err != nil {
		return lumni.Donation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.campaigns[campaignID]
	if !ok {
		return lumni.Donation{}, notFound("campaign", campaignID)
	}
	now := m.now()
	if !c.Open(now) {
		return lumni.Donation{}, fmt.Errorf("campaign is not accepting donations: %w", lumni.ErrConflict)
	}
	if d.AlumnusID != "" {
		a, ok := m.alumni[d.AlumnusID]
		if !ok || a.SchoolID != c.SchoolID {
			return lumni.Donation{}, notFound("alumnus", d.AlumnusID)
		}
		if d.DonorName == "" {
			d.DonorName = a.Name
		}
	}

	d.ID = newID("don")
	d.CampaignID = campaignID
	d.CreatedAt = now
	d.MatchedCents = 0
	if c.Match != nil {
		d.MatchedCents = min(d.AmountCents, c.Match.CapCents-c.Match.UsedCents)
		c.Match.UsedCents += d.MatchedCents
	}
	c.RaisedCents += d.AmountCents + d.MatchedCents
	c.DonorCount++
	m.donations[campaignID] = append(m.donations[campaignID], &d)
	return d, nil
}

// ListDonations returns a campaign's donations, newest first, as shown on the donor wall.
func (m *Memory) ListDonations(campaignID string) ([]lumni.Donation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.campaigns[campaignID]; !ok {
		return nil, notFound("campaign", campaignID)
	}
	ds := m.donations[campaignID]
	out := make([]lumni.Donation, 0, len(ds))
	for i := len(ds) - 1; i >= 0; i-- {
		out = append(out, ds[i].Public())
	}
	return out, nil
}

// Leaderboard ranks graduating classes by how much they have given to a campaign.
// Only donations tied to an alumnus count toward a class.
func (m *Memory) Leaderboard(campaignID string) ([]lumni.ClassStanding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.campaigns[campaignID]; !ok {
		return nil, notFound("campaign", campaignID)
	}
	byYear := map[int]*lumni.ClassStanding{}
	donors := map[int]map[string]bool{}
	for _, d := range m.donations[campaignID] {
		a, ok := m.alumni[d.AlumnusID]
		if !ok {
			continue
		}
		row := byYear[a.GradYear]
		if row == nil {
			row = &lumni.ClassStanding{GradYear: a.GradYear}
			byYear[a.GradYear] = row
			donors[a.GradYear] = map[string]bool{}
		}
		row.RaisedCents += d.AmountCents
		donors[a.GradYear][a.ID] = true
		row.Donors = len(donors[a.GradYear])
	}
	out := make([]lumni.ClassStanding, 0, len(byYear))
	for _, row := range byYear {
		out = append(out, *row)
	}
	slices.SortFunc(out, func(a, b lumni.ClassStanding) int {
		return cmp.Or(cmp.Compare(b.RaisedCents, a.RaisedCents), cmp.Compare(a.GradYear, b.GradYear))
	})
	return out, nil
}

// --- Insights ---

const topEmployers = 5

// Insights summarizes where a school's alumni are and how they give.
func (m *Memory) Insights(schoolID string) (lumni.Insights, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.schools[schoolID]; !ok {
		return lumni.Insights{}, notFound("school", schoolID)
	}
	in := lumni.Insights{
		ByIndustry:   map[string]int{},
		ByLocation:   map[string]int{},
		ByGradYear:   map[int]int{},
		TopEmployers: []lumni.Count{},
	}
	employers := map[string]int{}
	for _, a := range m.alumni {
		if a.SchoolID != schoolID {
			continue
		}
		in.TotalAlumni++
		in.ByGradYear[a.GradYear]++
		if a.Industry != "" {
			in.ByIndustry[a.Industry]++
		}
		if a.Location != "" {
			in.ByLocation[a.Location]++
		}
		if a.Employer != "" {
			employers[a.Employer]++
		}
	}
	for name, n := range employers {
		in.TopEmployers = append(in.TopEmployers, lumni.Count{Label: name, Count: n})
	}
	slices.SortFunc(in.TopEmployers, func(a, b lumni.Count) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Label, b.Label))
	})
	in.TopEmployers = in.TopEmployers[:min(len(in.TopEmployers), topEmployers)]

	now := m.now()
	givers := map[string]bool{}
	for _, c := range m.campaigns {
		if c.SchoolID != schoolID {
			continue
		}
		in.TotalRaisedCents += c.RaisedCents
		if c.Open(now) {
			in.ActiveCampaigns++
		}
		for _, d := range m.donations[c.ID] {
			if d.AlumnusID != "" {
				givers[d.AlumnusID] = true
			}
		}
	}
	in.AlumniDonors = len(givers)
	if in.TotalAlumni > 0 {
		in.ParticipationRate = float64(in.AlumniDonors) / float64(in.TotalAlumni) * 100
	}
	return in, nil
}
