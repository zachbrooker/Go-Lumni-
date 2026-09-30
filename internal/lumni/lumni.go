// Package lumni defines the core domain types for the Lumni platform:
// schools, the members of their communities (alumni, parents, and friends),
// and the fundraising campaigns and giving days schools run.
package lumni

import (
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors returned by the store and mapped to HTTP statuses by the API.
var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrValidation = errors.New("validation failed")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// School is a tenant on the platform. Everything else hangs off a school.
type School struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *School) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return invalid("name is required")
	}
	return nil
}

// MemberKind says how someone belongs to the school community.
type MemberKind string

const (
	KindAlumnus      MemberKind = "alumnus"
	KindParent       MemberKind = "parent"        // current parent
	KindAlumniParent MemberKind = "alumni_parent" // parent of a graduate
	KindGrandparent  MemberKind = "grandparent"
	KindFaculty      MemberKind = "faculty"
	KindFriend       MemberKind = "friend"
)

// Kinds lists every member kind, in display order.
var Kinds = []MemberKind{KindAlumnus, KindParent, KindAlumniParent, KindGrandparent, KindFaculty, KindFriend}

// Label is the kind's human-readable name.
func (k MemberKind) Label() string {
	switch k {
	case KindAlumnus:
		return "Alumnus"
	case KindParent:
		return "Current parent"
	case KindAlumniParent:
		return "Alumni parent"
	case KindGrandparent:
		return "Grandparent"
	case KindFaculty:
		return "Faculty & staff"
	case KindFriend:
		return "Friend"
	}
	return string(k)
}

func (k MemberKind) valid() bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Grades lists school grades in order, used to validate and sort parents' grades.
var Grades = []string{"PK", "K", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}

// GradeIndex returns a grade's position for sorting, or -1 if unknown.
func GradeIndex(g string) int {
	for i, x := range Grades {
		if x == g {
			return i
		}
	}
	return -1
}

// Member is someone in a school's community: a graduate, a current parent,
// a grandparent, and so on. Alumni have a GradYear; parents have a Grade
// (the grade of their child).
type Member struct {
	ID        string     `json:"id"`
	SchoolID  string     `json:"school_id"`
	Kind      MemberKind `json:"kind"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	GradYear  int        `json:"grad_year,omitempty"` // alumni and alumni parents (the child's class)
	Grade     string     `json:"grade,omitempty"`     // current parents and grandparents
	Employer  string     `json:"employer,omitempty"`
	Title     string     `json:"title,omitempty"`
	Industry  string     `json:"industry,omitempty"`
	Location  string     `json:"location,omitempty"`
	Bio       string     `json:"bio,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (m *Member) Validate() error {
	m.Name = strings.TrimSpace(m.Name)
	m.Email = strings.ToLower(strings.TrimSpace(m.Email))
	m.Grade = strings.ToUpper(strings.TrimSpace(m.Grade))
	if m.Kind == "" {
		m.Kind = KindAlumnus
	}
	if !m.Kind.valid() {
		return invalid("kind %q is not one of %v", m.Kind, Kinds)
	}
	if m.Name == "" {
		return invalid("name is required")
	}
	if _, err := mail.ParseAddress(m.Email); err != nil {
		return invalid("a valid email is required")
	}
	switch m.Kind {
	case KindAlumnus, KindAlumniParent:
		if m.GradYear < 1800 || m.GradYear > time.Now().Year()+10 {
			return invalid("grad_year is required for %s", m.Kind.Label())
		}
	case KindParent, KindGrandparent:
		if GradeIndex(m.Grade) < 0 {
			return invalid("grade is required for %s and must be one of %v", m.Kind.Label(), Grades)
		}
	}
	if m.GradYear != 0 && (m.GradYear < 1800 || m.GradYear > time.Now().Year()+10) {
		return invalid("grad_year is out of range")
	}
	if m.Grade != "" && GradeIndex(m.Grade) < 0 {
		return invalid("grade must be one of %v", Grades)
	}
	return nil
}

// ClassLabel describes an alumnus's class, e.g. "Class of 2012".
func (m Member) ClassLabel() string {
	if m.GradYear == 0 {
		return ""
	}
	return "Class of " + strconv.Itoa(m.GradYear)
}

// MemberPatch holds optional fields for updating a member's profile.
type MemberPatch struct {
	Kind     *MemberKind `json:"kind"`
	Name     *string     `json:"name"`
	Email    *string     `json:"email"`
	GradYear *int        `json:"grad_year"`
	Grade    *string     `json:"grade"`
	Employer *string     `json:"employer"`
	Title    *string     `json:"title"`
	Industry *string     `json:"industry"`
	Location *string     `json:"location"`
	Bio      *string     `json:"bio"`
}

// Apply copies the set fields of p onto m.
func (p MemberPatch) Apply(m *Member) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&m.Name, p.Name)
	set(&m.Email, p.Email)
	set(&m.Grade, p.Grade)
	set(&m.Employer, p.Employer)
	set(&m.Title, p.Title)
	set(&m.Industry, p.Industry)
	set(&m.Location, p.Location)
	set(&m.Bio, p.Bio)
	if p.Kind != nil {
		m.Kind = *p.Kind
	}
	if p.GradYear != nil {
		m.GradYear = *p.GradYear
	}
}

// MemberFilter narrows a directory search. Zero values mean "any".
type MemberFilter struct {
	Query    string // matches name, employer, title or bio
	Kind     MemberKind
	Industry string
	Location string
	GradYear int
	Grade    string
}

// Match is a sponsor's pledge to match donations 1:1 up to a cap.
type Match struct {
	Sponsor   string `json:"sponsor"`
	CapCents  int64  `json:"cap_cents"`
	UsedCents int64  `json:"used_cents"`
}

// Campaign kinds.
const (
	KindCampaign  = "campaign"   // an open-ended drive with a goal
	KindGivingDay = "giving_day" // a time-boxed event with challenges and live leaderboards
)

// Campaign status values.
const (
	StatusActive = "active"
	StatusClosed = "closed"
)

// Segment names the slice of the community a challenge counts. Empty means everyone.
// Forms: "" | "alumni" | "parents" | "class:2012" | "grade:3".
type Segment string

// Includes reports whether the member belongs to the segment.
func (s Segment) Includes(m Member) bool {
	switch {
	case s == "":
		return true
	case s == "alumni":
		return m.Kind == KindAlumnus
	case s == "parents":
		return m.Kind == KindParent
	case strings.HasPrefix(string(s), "class:"):
		y, _ := strconv.Atoi(strings.TrimPrefix(string(s), "class:"))
		return m.GradYear == y && (m.Kind == KindAlumnus || m.Kind == KindAlumniParent)
	case strings.HasPrefix(string(s), "grade:"):
		return m.Grade == strings.TrimPrefix(string(s), "grade:") && (m.Kind == KindParent || m.Kind == KindGrandparent)
	}
	return false
}

// Label is a display name for the segment.
func (s Segment) Label() string {
	switch {
	case s == "":
		return "everyone"
	case s == "alumni":
		return "alumni"
	case s == "parents":
		return "current parents"
	case strings.HasPrefix(string(s), "class:"):
		return "Class of " + strings.TrimPrefix(string(s), "class:")
	case strings.HasPrefix(string(s), "grade:"):
		return "Grade " + strings.TrimPrefix(string(s), "grade:")
	}
	return string(s)
}

func (s Segment) valid() bool {
	switch {
	case s == "" || s == "alumni" || s == "parents":
		return true
	case strings.HasPrefix(string(s), "class:"):
		_, err := strconv.Atoi(strings.TrimPrefix(string(s), "class:"))
		return err == nil
	case strings.HasPrefix(string(s), "grade:"):
		return GradeIndex(strings.TrimPrefix(string(s), "grade:")) >= 0
	}
	return false
}

// Challenge metrics.
const (
	MetricDonors  = "donors"  // number of distinct gifts that count
	MetricDollars = "dollars" // cents given
)

// Challenge is a giving-day unlock: when the segment reaches the threshold,
// a sponsor releases RewardCents to the campaign.
// "First 100 donors unlock $10,000 from the Board" is
// {Metric: donors, Threshold: 100, RewardCents: 1_000_000, Sponsor: "the Board"}.
type Challenge struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Sponsor     string    `json:"sponsor"`
	Segment     Segment   `json:"segment"`
	Metric      string    `json:"metric"`
	Threshold   int64     `json:"threshold"` // donors, or cents for the dollars metric
	RewardCents int64     `json:"reward_cents"`
	StartsAt    time.Time `json:"starts_at,omitzero"` // optional window inside the giving day
	EndsAt      time.Time `json:"ends_at,omitzero"`
	Progress    int64     `json:"progress"` // current donors or cents toward the threshold
	UnlockedAt  time.Time `json:"unlocked_at,omitzero"`
}

func (c *Challenge) Validate() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Sponsor = strings.TrimSpace(c.Sponsor)
	if c.Name == "" {
		return invalid("challenge name is required")
	}
	if c.Metric == "" {
		c.Metric = MetricDonors
	}
	if c.Metric != MetricDonors && c.Metric != MetricDollars {
		return invalid("challenge metric must be donors or dollars")
	}
	if c.Threshold <= 0 || c.RewardCents <= 0 {
		return invalid("challenge needs a positive threshold and reward_cents")
	}
	if !c.Segment.valid() {
		return invalid("challenge segment %q is not valid", c.Segment)
	}
	if !c.StartsAt.IsZero() && !c.EndsAt.IsZero() && !c.EndsAt.After(c.StartsAt) {
		return invalid("challenge window must end after it starts")
	}
	return nil
}

// Unlocked reports whether the sponsor's reward has been released.
func (c Challenge) Unlocked() bool { return !c.UnlockedAt.IsZero() }

// Counts reports whether a gift at time t from member m (zero Member for
// non-member gifts) counts toward this challenge.
func (c Challenge) Counts(t time.Time, m Member, isMember bool) bool {
	if !c.StartsAt.IsZero() && t.Before(c.StartsAt) {
		return false
	}
	if !c.EndsAt.IsZero() && !t.Before(c.EndsAt) {
		return false
	}
	if c.Segment == "" {
		return true
	}
	return isMember && c.Segment.Includes(m)
}

// Percent is progress toward the threshold, 0–100.
func (c Challenge) Percent() float64 {
	if c.Threshold == 0 {
		return 0
	}
	p := float64(c.Progress) / float64(c.Threshold) * 100
	if p > 100 {
		p = 100
	}
	return p
}

// Campaign is a fundraising drive run by a school. A giving day is a campaign
// with Kind == KindGivingDay, a start time, a deadline and usually challenges.
type Campaign struct {
	ID          string      `json:"id"`
	SchoolID    string      `json:"school_id"`
	Kind        string      `json:"kind"`
	Title       string      `json:"title"`
	Story       string      `json:"story"`
	GoalCents   int64       `json:"goal_cents"`
	StartsAt    time.Time   `json:"starts_at,omitzero"`
	Deadline    time.Time   `json:"deadline,omitzero"`
	Match       *Match      `json:"match,omitempty"`
	Challenges  []Challenge `json:"challenges"`
	Status      string      `json:"status"`
	RaisedCents int64       `json:"raised_cents"` // gifts + matched funds + unlocked challenge rewards
	BonusCents  int64       `json:"bonus_cents"`  // matched funds + unlocked rewards, included in RaisedCents
	DonorCount  int         `json:"donor_count"`
	Updates     []Update    `json:"updates"`
	CreatedAt   time.Time   `json:"created_at"`
}

// PercentFunded reports progress toward the goal.
func (c Campaign) PercentFunded() float64 {
	if c.GoalCents == 0 {
		return 0
	}
	return float64(c.RaisedCents) / float64(c.GoalCents) * 100
}

// IsGivingDay reports whether the campaign is a time-boxed giving day.
func (c Campaign) IsGivingDay() bool { return c.Kind == KindGivingDay }

// Started reports whether the campaign has begun at time now.
func (c Campaign) Started(now time.Time) bool { return c.StartsAt.IsZero() || !now.Before(c.StartsAt) }

// Open reports whether the campaign accepts donations at time now.
func (c Campaign) Open(now time.Time) bool {
	if c.Status != StatusActive || !c.Started(now) {
		return false
	}
	return c.Deadline.IsZero() || now.Before(c.Deadline)
}

func (c *Campaign) Validate() error {
	c.Title = strings.TrimSpace(c.Title)
	if c.Title == "" {
		return invalid("title is required")
	}
	if c.GoalCents <= 0 {
		return invalid("goal_cents must be positive")
	}
	if c.Kind == "" {
		c.Kind = KindCampaign
	}
	if c.Kind != KindCampaign && c.Kind != KindGivingDay {
		return invalid("kind must be campaign or giving_day")
	}
	if c.Kind == KindGivingDay && (c.StartsAt.IsZero() || c.Deadline.IsZero()) {
		return invalid("a giving day needs starts_at and deadline")
	}
	if !c.StartsAt.IsZero() && !c.Deadline.IsZero() && !c.Deadline.After(c.StartsAt) {
		return invalid("deadline must be after starts_at")
	}
	if c.Match != nil {
		c.Match.Sponsor = strings.TrimSpace(c.Match.Sponsor)
		if c.Match.Sponsor == "" || c.Match.CapCents <= 0 {
			return invalid("match needs a sponsor and a positive cap_cents")
		}
		c.Match.UsedCents = 0
	}
	for i := range c.Challenges {
		if err := c.Challenges[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Update is a progress post on a campaign ("We hit 50%! Here's what's next.").
type Update struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// PlatformFeeBps is Lumni's take on each gift, in basis points (400 = 4%).
// The business model is a percentage of donations, with no subscription.
const PlatformFeeBps = 400

// Fee returns Lumni's platform fee on a gift of amountCents.
func Fee(amountCents int64) int64 { return amountCents * PlatformFeeBps / 10_000 }

// Gift methods. Card gifts clear immediately; the others are recorded as
// pledged and confirmed when the transfer, grant or distribution arrives.
const (
	MethodCard    = "card"
	MethodStock   = "stock"   // appreciated securities transferred to the school's brokerage account
	MethodDAF     = "daf"     // grant recommended from a donor-advised fund
	MethodMonthly = "monthly" // recurring card gift
	MethodIRA     = "ira"     // qualified charitable distribution from an IRA
	MethodCheck   = "check"
	MethodWire    = "wire"
)

// Methods lists every gift method, in display order.
var Methods = []string{MethodCard, MethodStock, MethodDAF, MethodMonthly, MethodIRA, MethodCheck, MethodWire}

// MethodLabel is a method's human-readable name.
func MethodLabel(m string) string {
	switch m {
	case MethodCard:
		return "Card"
	case MethodStock:
		return "Stock"
	case MethodDAF:
		return "Donor-advised fund"
	case MethodMonthly:
		return "Monthly"
	case MethodIRA:
		return "IRA gift"
	case MethodCheck:
		return "Check"
	case MethodWire:
		return "Wire"
	}
	return m
}

// Donation is a gift to a campaign, optionally tied to a community member.
type Donation struct {
	ID            string    `json:"id"`
	CampaignID    string    `json:"campaign_id"`
	Method        string    `json:"method"`
	EmployerMatch bool      `json:"employer_match"` // donor asked Lumni to check for a corporate match
	MemberID      string    `json:"member_id,omitempty"`
	DonorName     string    `json:"donor_name"`
	DonorEmail    string    `json:"donor_email,omitempty"` // used to link the gift to a member when MemberID is empty
	DonorKind     string    `json:"donor_kind,omitempty"`  // member kind label at time of gift, for the wall
	AmountCents   int64     `json:"amount_cents"`
	MatchedCents  int64     `json:"matched_cents"`
	FeeCents      int64     `json:"fee_cents"` // Lumni's platform fee, taken from AmountCents
	Message       string    `json:"message,omitempty"`
	Anonymous     bool      `json:"anonymous"`
	CreatedAt     time.Time `json:"created_at"`
}

// MinDonationCents is the smallest accepted gift ($1).
const MinDonationCents = 100

// MajorGiftCents is the threshold above which a gift is flagged for personal
// follow-up by the advancement office.
const MajorGiftCents = 10_000_00

func (d *Donation) Validate() error {
	d.DonorName = strings.TrimSpace(d.DonorName)
	d.DonorEmail = strings.ToLower(strings.TrimSpace(d.DonorEmail))
	if d.AmountCents < MinDonationCents {
		return invalid("amount_cents must be at least %d", MinDonationCents)
	}
	if d.DonorEmail != "" {
		if _, err := mail.ParseAddress(d.DonorEmail); err != nil {
			return invalid("donor_email is not a valid email")
		}
	}
	if d.Method == "" {
		d.Method = MethodCard
	}
	if MethodLabel(d.Method) == d.Method {
		return invalid("method must be one of %v", Methods)
	}
	if d.DonorName == "" && d.MemberID == "" && d.DonorEmail == "" {
		return invalid("donor_name, donor_email or member_id is required")
	}
	return nil
}

// Major reports whether the gift is large enough to warrant personal follow-up.
func (d Donation) Major() bool { return d.AmountCents >= MajorGiftCents }

// Pending reports whether the gift is a pledge awaiting a transfer, grant or
// distribution rather than money already received.
func (d Donation) Pending() bool {
	return d.Method == MethodStock || d.Method == MethodDAF || d.Method == MethodIRA || d.Method == MethodCheck || d.Method == MethodWire
}

// Public returns the donation as shown on a public donor wall.
func (d Donation) Public() Donation {
	d.DonorEmail = ""
	if d.Anonymous {
		d.DonorName = "Anonymous"
		d.MemberID = ""
		d.DonorKind = ""
	}
	return d
}

// Leaderboard groupings.
const (
	ByClass = "class" // alumni by graduating class
	ByGrade = "grade" // parents by their child's grade
)

// Standing is one row of a leaderboard: a class year or a grade.
type Standing struct {
	Key           string  `json:"key"`   // "2012" or "3"
	Label         string  `json:"label"` // "Class of 2012" or "Grade 3"
	RaisedCents   int64   `json:"raised_cents"`
	Donors        int     `json:"donors"`        // distinct members who gave
	Members       int     `json:"members"`       // members in the group (the denominator)
	Participation float64 `json:"participation"` // Donors / Members, as a percentage
}

// Insights summarizes a school's community and giving.
type Insights struct {
	TotalMembers        int                  `json:"total_members"`
	ByKind              map[MemberKind]int   `json:"by_kind"`
	Donors              int                  `json:"donors"`             // distinct members who have ever given
	ParticipationRate   float64              `json:"participation_rate"` // % of members who have given
	AlumniParticipation float64              `json:"alumni_participation"`
	ParentParticipation float64              `json:"parent_participation"`
	TotalRaisedCents    int64                `json:"total_raised_cents"`
	PlatformFeeCents    int64                `json:"platform_fee_cents"` // what Lumni has earned from this school
	ActiveCampaigns     int                  `json:"active_campaigns"`
	ByIndustry          map[string]int       `json:"by_industry"`
	ByLocation          map[string]int       `json:"by_location"`
	ByGradYear          map[int]int          `json:"by_grad_year"`
	ByGrade             map[string]int       `json:"by_grade"`
	TopEmployers        []Count              `json:"top_employers"`
	RaisedByKindCents   map[MemberKind]int64 `json:"raised_by_kind_cents"`
}

// Count is a labeled tally.
type Count struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}
