// Package lumni defines the core domain types for the Lumni platform:
// schools, their alumni, and the fundraising campaigns schools run.
package lumni

import (
	"errors"
	"fmt"
	"net/mail"
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

// Alumnus is a graduate of a school and what they are doing now.
type Alumnus struct {
	ID        string    `json:"id"`
	SchoolID  string    `json:"school_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	GradYear  int       `json:"grad_year"`
	Degree    string    `json:"degree,omitempty"`
	Employer  string    `json:"employer,omitempty"`
	Title     string    `json:"title,omitempty"`
	Industry  string    `json:"industry,omitempty"`
	Location  string    `json:"location,omitempty"`
	Bio       string    `json:"bio,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (a *Alumnus) Validate() error {
	a.Name = strings.TrimSpace(a.Name)
	a.Email = strings.ToLower(strings.TrimSpace(a.Email))
	if a.Name == "" {
		return invalid("name is required")
	}
	if _, err := mail.ParseAddress(a.Email); err != nil {
		return invalid("a valid email is required")
	}
	if a.GradYear < 1800 || a.GradYear > time.Now().Year()+10 {
		return invalid("grad_year is out of range")
	}
	return nil
}

// AlumnusPatch holds optional fields for updating an alumnus's profile.
type AlumnusPatch struct {
	Name     *string `json:"name"`
	Email    *string `json:"email"`
	GradYear *int    `json:"grad_year"`
	Degree   *string `json:"degree"`
	Employer *string `json:"employer"`
	Title    *string `json:"title"`
	Industry *string `json:"industry"`
	Location *string `json:"location"`
	Bio      *string `json:"bio"`
}

// Apply copies the set fields of p onto a.
func (p AlumnusPatch) Apply(a *Alumnus) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&a.Name, p.Name)
	set(&a.Email, p.Email)
	set(&a.Degree, p.Degree)
	set(&a.Employer, p.Employer)
	set(&a.Title, p.Title)
	set(&a.Industry, p.Industry)
	set(&a.Location, p.Location)
	set(&a.Bio, p.Bio)
	if p.GradYear != nil {
		a.GradYear = *p.GradYear
	}
}

// AlumniFilter narrows a directory search. Zero values mean "any".
type AlumniFilter struct {
	Query    string // matches name, employer, title or bio
	Industry string
	Location string
	GradYear int
}

// Match is a sponsor's pledge to match donations 1:1 up to a cap.
type Match struct {
	Sponsor   string `json:"sponsor"`
	CapCents  int64  `json:"cap_cents"`
	UsedCents int64  `json:"used_cents"`
}

// Campaign status values.
const (
	StatusActive = "active"
	StatusClosed = "closed"
)

// Campaign is a fundraising drive run by a school.
type Campaign struct {
	ID          string    `json:"id"`
	SchoolID    string    `json:"school_id"`
	Title       string    `json:"title"`
	Story       string    `json:"story"`
	GoalCents   int64     `json:"goal_cents"`
	Deadline    time.Time `json:"deadline,omitzero"`
	Match       *Match    `json:"match,omitempty"`
	Status      string    `json:"status"`
	RaisedCents int64     `json:"raised_cents"` // donations plus matched funds
	DonorCount  int       `json:"donor_count"`
	Updates     []Update  `json:"updates"`
	CreatedAt   time.Time `json:"created_at"`
}

// PercentFunded reports progress toward the goal, capped at display time by the client.
func (c *Campaign) PercentFunded() float64 {
	if c.GoalCents == 0 {
		return 0
	}
	return float64(c.RaisedCents) / float64(c.GoalCents) * 100
}

// Open reports whether the campaign accepts donations at time now.
func (c *Campaign) Open(now time.Time) bool {
	if c.Status != StatusActive {
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
	if c.Match != nil {
		c.Match.Sponsor = strings.TrimSpace(c.Match.Sponsor)
		if c.Match.Sponsor == "" || c.Match.CapCents <= 0 {
			return invalid("match needs a sponsor and a positive cap_cents")
		}
		c.Match.UsedCents = 0
	}
	return nil
}

// Update is a progress post on a campaign ("We hit 50%! Here's what's next.").
type Update struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Donation is a gift to a campaign, optionally tied to an alumnus.
type Donation struct {
	ID           string    `json:"id"`
	CampaignID   string    `json:"campaign_id"`
	AlumnusID    string    `json:"alumnus_id,omitempty"`
	DonorName    string    `json:"donor_name"`
	AmountCents  int64     `json:"amount_cents"`
	MatchedCents int64     `json:"matched_cents"`
	Message      string    `json:"message,omitempty"`
	Anonymous    bool      `json:"anonymous"`
	CreatedAt    time.Time `json:"created_at"`
}

// MinDonationCents is the smallest accepted gift ($1).
const MinDonationCents = 100

func (d *Donation) Validate() error {
	d.DonorName = strings.TrimSpace(d.DonorName)
	if d.AmountCents < MinDonationCents {
		return invalid("amount_cents must be at least %d", MinDonationCents)
	}
	if d.DonorName == "" && d.AlumnusID == "" {
		return invalid("donor_name or alumnus_id is required")
	}
	return nil
}

// Public returns the donation as shown on a public donor wall.
func (d Donation) Public() Donation {
	if d.Anonymous {
		d.DonorName = "Anonymous"
		d.AlumnusID = ""
	}
	return d
}

// ClassStanding is one row of a campaign's class-year leaderboard.
type ClassStanding struct {
	GradYear    int   `json:"grad_year"`
	RaisedCents int64 `json:"raised_cents"`
	Donors      int   `json:"donors"`
}

// Insights summarizes a school's alumni network and giving.
type Insights struct {
	TotalAlumni       int            `json:"total_alumni"`
	AlumniDonors      int            `json:"alumni_donors"`
	ParticipationRate float64        `json:"participation_rate"` // % of alumni who have given
	TotalRaisedCents  int64          `json:"total_raised_cents"`
	ActiveCampaigns   int            `json:"active_campaigns"`
	ByIndustry        map[string]int `json:"by_industry"`
	ByLocation        map[string]int `json:"by_location"`
	ByGradYear        map[int]int    `json:"by_grad_year"`
	TopEmployers      []Count        `json:"top_employers"`
}

// Count is a labeled tally.
type Count struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}
