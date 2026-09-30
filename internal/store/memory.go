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
	"strconv"
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
	members   map[string]*lumni.Member
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
		members:   map[string]*lumni.Member{},
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

// --- Members ---

func (m *Memory) emailTaken(schoolID, email, exceptID string) bool {
	for _, x := range m.members {
		if x.SchoolID == schoolID && x.Email == email && x.ID != exceptID {
			return true
		}
	}
	return false
}

func (m *Memory) CreateMember(x lumni.Member) (lumni.Member, error) {
	if err := x.Validate(); err != nil {
		return lumni.Member{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schools[x.SchoolID]; !ok {
		return lumni.Member{}, notFound("school", x.SchoolID)
	}
	if m.emailTaken(x.SchoolID, x.Email, "") {
		return lumni.Member{}, fmt.Errorf("email %q already registered: %w", x.Email, lumni.ErrConflict)
	}
	x.ID = newID("mem")
	x.CreatedAt = m.now()
	x.UpdatedAt = x.CreatedAt
	m.members[x.ID] = &x
	return x, nil
}

func (m *Memory) GetMember(schoolID, id string) (lumni.Member, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	x, ok := m.members[id]
	if !ok || x.SchoolID != schoolID {
		return lumni.Member{}, notFound("member", id)
	}
	return *x, nil
}

func (m *Memory) UpdateMember(schoolID, id string, p lumni.MemberPatch) (lumni.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.members[id]
	if !ok || cur.SchoolID != schoolID {
		return lumni.Member{}, notFound("member", id)
	}
	next := *cur
	p.Apply(&next)
	if err := next.Validate(); err != nil {
		return lumni.Member{}, err
	}
	if m.emailTaken(schoolID, next.Email, id) {
		return lumni.Member{}, fmt.Errorf("email %q already registered: %w", next.Email, lumni.ErrConflict)
	}
	next.UpdatedAt = m.now()
	*cur = next
	return next, nil
}

func eqFold(filter, v string) bool { return filter == "" || strings.EqualFold(filter, v) }

// ListMembers returns a school's members matching f, sorted by kind, then
// class year / grade, then name.
func (m *Memory) ListMembers(schoolID string, f lumni.MemberFilter) ([]lumni.Member, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.schools[schoolID]; !ok {
		return nil, notFound("school", schoolID)
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := []lumni.Member{}
	for _, x := range m.members {
		if x.SchoolID != schoolID || !eqFold(f.Industry, x.Industry) || !eqFold(f.Location, x.Location) {
			continue
		}
		if f.Kind != "" && f.Kind != x.Kind {
			continue
		}
		if f.GradYear != 0 && f.GradYear != x.GradYear {
			continue
		}
		if f.Grade != "" && !strings.EqualFold(f.Grade, x.Grade) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(strings.Join([]string{x.Name, x.Employer, x.Title, x.Bio}, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, *x)
	}
	kindRank := func(k lumni.MemberKind) int { return slices.Index(lumni.Kinds, k) }
	slices.SortFunc(out, func(a, b lumni.Member) int {
		return cmp.Or(
			cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)),
			cmp.Compare(a.GradYear, b.GradYear),
			cmp.Compare(lumni.GradeIndex(a.Grade), lumni.GradeIndex(b.Grade)),
			cmp.Compare(a.Name, b.Name),
		)
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
	out.Challenges = slices.Clone(c.Challenges)
	if out.Challenges == nil {
		out.Challenges = []lumni.Challenge{}
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
	c.RaisedCents, c.BonusCents, c.DonorCount, c.Updates = 0, 0, 0, nil
	c.Challenges = slices.Clone(c.Challenges)
	for i := range c.Challenges {
		c.Challenges[i].ID = newID("chl")
		c.Challenges[i].Progress = 0
		c.Challenges[i].UnlockedAt = time.Time{}
	}
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

// AddChallenge attaches a challenge to a campaign. Earlier gifts do not count toward it.
func (m *Memory) AddChallenge(campaignID string, ch lumni.Challenge) (lumni.Campaign, error) {
	if err := ch.Validate(); err != nil {
		return lumni.Campaign{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.campaigns[campaignID]
	if !ok {
		return lumni.Campaign{}, notFound("campaign", campaignID)
	}
	ch.ID = newID("chl")
	ch.Progress, ch.UnlockedAt = 0, time.Time{}
	c.Challenges = append(c.Challenges, ch)
	return cloneCampaign(c), nil
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

// Donate records a gift, applies any sponsor match with room left, and
// advances (and possibly unlocks) the campaign's challenges. When MemberID
// is set, the donor name defaults to the member's name.
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
	// A gift with only an email is linked to the member with that email, so
	// a shared "give" link still tells the school which alumni gave.
	if d.MemberID == "" && d.DonorEmail != "" {
		for _, x := range m.members {
			if x.SchoolID == c.SchoolID && x.Email == d.DonorEmail {
				d.MemberID = x.ID
				break
			}
		}
	}
	var member lumni.Member
	isMember := d.MemberID != ""
	if isMember {
		x, ok := m.members[d.MemberID]
		if !ok || x.SchoolID != c.SchoolID {
			return lumni.Donation{}, notFound("member", d.MemberID)
		}
		member = *x
		if d.DonorName == "" {
			d.DonorName = member.Name
		}
		if d.DonorEmail == "" {
			d.DonorEmail = member.Email
		}
		d.DonorKind = member.Kind.Label()
	} else {
		d.DonorKind = ""
	}

	d.ID = newID("don")
	d.CampaignID = campaignID
	d.CreatedAt = now
	d.MatchedCents = 0
	d.FeeCents = lumni.Fee(d.AmountCents)
	if c.Match != nil {
		d.MatchedCents = min(d.AmountCents, c.Match.CapCents-c.Match.UsedCents)
		c.Match.UsedCents += d.MatchedCents
	}
	c.RaisedCents += d.AmountCents + d.MatchedCents
	c.BonusCents += d.MatchedCents
	c.DonorCount++

	for i := range c.Challenges {
		ch := &c.Challenges[i]
		if ch.Unlocked() || !ch.Counts(now, member, isMember) {
			continue
		}
		if ch.Metric == lumni.MetricDollars {
			ch.Progress += d.AmountCents
		} else {
			ch.Progress++
		}
		if ch.Progress >= ch.Threshold {
			ch.UnlockedAt = now
			c.RaisedCents += ch.RewardCents
			c.BonusCents += ch.RewardCents
		}
	}

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

// SchoolDonations returns every gift to a school's campaigns, newest first,
// unredacted, for the school's own records (donor names and emails included).
func (m *Memory) SchoolDonations(schoolID string) ([]lumni.Donation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.schools[schoolID]; !ok {
		return nil, notFound("school", schoolID)
	}
	out := []lumni.Donation{}
	for id, c := range m.campaigns {
		if c.SchoolID != schoolID {
			continue
		}
		for _, d := range m.donations[id] {
			out = append(out, *d)
		}
	}
	slices.SortFunc(out, func(a, b lumni.Donation) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Leaderboard ranks groups of the community by giving to a campaign.
// by is lumni.ByClass (alumni and alumni parents, by class year) or
// lumni.ByGrade (current parents and grandparents, by child's grade).
// Members of every matching group are counted so participation can be shown,
// even for groups with no gifts yet. Rows are sorted by participation, then dollars.
func (m *Memory) Leaderboard(campaignID, by string) ([]lumni.Standing, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.campaigns[campaignID]
	if !ok {
		return nil, notFound("campaign", campaignID)
	}
	key := func(x lumni.Member) (string, bool) {
		switch by {
		case lumni.ByClass:
			if (x.Kind == lumni.KindAlumnus || x.Kind == lumni.KindAlumniParent) && x.GradYear != 0 {
				return strconv.Itoa(x.GradYear), true
			}
		case lumni.ByGrade:
			if (x.Kind == lumni.KindParent || x.Kind == lumni.KindGrandparent) && x.Grade != "" {
				return x.Grade, true
			}
		}
		return "", false
	}
	rows := map[string]*lumni.Standing{}
	row := func(k string) *lumni.Standing {
		r := rows[k]
		if r == nil {
			r = &lumni.Standing{Key: k, Label: lumni.Segment(by + ":" + k).Label()}
			rows[k] = r
		}
		return r
	}
	for _, x := range m.members {
		if x.SchoolID != c.SchoolID {
			continue
		}
		if k, ok := key(*x); ok {
			row(k).Members++
		}
	}
	donors := map[string]map[string]bool{}
	for _, d := range m.donations[campaignID] {
		x, ok := m.members[d.MemberID]
		if !ok {
			continue
		}
		k, ok := key(*x)
		if !ok {
			continue
		}
		r := row(k)
		r.RaisedCents += d.AmountCents
		if donors[k] == nil {
			donors[k] = map[string]bool{}
		}
		donors[k][x.ID] = true
		r.Donors = len(donors[k])
	}
	out := make([]lumni.Standing, 0, len(rows))
	for _, r := range rows {
		if r.Members > 0 {
			r.Participation = float64(r.Donors) / float64(r.Members) * 100
		}
		out = append(out, *r)
	}
	sortKey := func(s lumni.Standing) int {
		if by == lumni.ByGrade {
			return lumni.GradeIndex(s.Key)
		}
		n, _ := strconv.Atoi(s.Key)
		return n
	}
	slices.SortFunc(out, func(a, b lumni.Standing) int {
		return cmp.Or(
			cmp.Compare(b.Participation, a.Participation),
			cmp.Compare(b.RaisedCents, a.RaisedCents),
			cmp.Compare(sortKey(a), sortKey(b)),
		)
	})
	return out, nil
}

// --- Insights ---

const topEmployers = 5

// Insights summarizes a school's community and how it gives.
func (m *Memory) Insights(schoolID string) (lumni.Insights, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.schools[schoolID]; !ok {
		return lumni.Insights{}, notFound("school", schoolID)
	}
	in := lumni.Insights{
		ByKind:            map[lumni.MemberKind]int{},
		ByIndustry:        map[string]int{},
		ByLocation:        map[string]int{},
		ByGradYear:        map[int]int{},
		ByGrade:           map[string]int{},
		TopEmployers:      []lumni.Count{},
		RaisedByKindCents: map[lumni.MemberKind]int64{},
	}
	employers := map[string]int{}
	for _, x := range m.members {
		if x.SchoolID != schoolID {
			continue
		}
		in.TotalMembers++
		in.ByKind[x.Kind]++
		if x.GradYear != 0 {
			in.ByGradYear[x.GradYear]++
		}
		if x.Grade != "" {
			in.ByGrade[x.Grade]++
		}
		if x.Industry != "" {
			in.ByIndustry[x.Industry]++
		}
		if x.Location != "" {
			in.ByLocation[x.Location]++
		}
		if x.Employer != "" {
			employers[x.Employer]++
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
	givers := map[string]lumni.MemberKind{}
	for _, c := range m.campaigns {
		if c.SchoolID != schoolID {
			continue
		}
		in.TotalRaisedCents += c.RaisedCents
		if c.Open(now) {
			in.ActiveCampaigns++
		}
		for _, d := range m.donations[c.ID] {
			in.PlatformFeeCents += d.FeeCents
			if x, ok := m.members[d.MemberID]; ok {
				givers[x.ID] = x.Kind
				in.RaisedByKindCents[x.Kind] += d.AmountCents
			}
		}
	}
	in.Donors = len(givers)
	byKindGivers := map[lumni.MemberKind]int{}
	for _, k := range givers {
		byKindGivers[k]++
	}
	rate := func(n, total int) float64 {
		if total == 0 {
			return 0
		}
		return float64(n) / float64(total) * 100
	}
	in.ParticipationRate = rate(in.Donors, in.TotalMembers)
	in.AlumniParticipation = rate(byKindGivers[lumni.KindAlumnus], in.ByKind[lumni.KindAlumnus])
	in.ParentParticipation = rate(byKindGivers[lumni.KindParent], in.ByKind[lumni.KindParent])
	return in, nil
}
