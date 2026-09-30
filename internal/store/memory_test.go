package store

import (
	"errors"
	"testing"
	"time"

	"github.com/zachbrooker/go-lumni/internal/lumni"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	st     *Memory
	now    *time.Time
	school lumni.School
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := t0
	st := NewMemory(func() time.Time { return now })
	s, err := st.CreateSchool(lumni.School{Name: "Westbrook"})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{st: st, now: &now, school: s}
}

func (f *fixture) alum(t *testing.T, name, email string, year int, mods ...func(*lumni.Alumnus)) lumni.Alumnus {
	t.Helper()
	a := lumni.Alumnus{SchoolID: f.school.ID, Name: name, Email: email, GradYear: year}
	for _, m := range mods {
		m(&a)
	}
	out, err := f.st.CreateAlumnus(a)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *fixture) campaign(t *testing.T, goal int64, match *lumni.Match) lumni.Campaign {
	t.Helper()
	c, err := f.st.CreateCampaign(lumni.Campaign{SchoolID: f.school.ID, Title: "Library", GoalCents: goal, Match: match})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAlumniDirectoryFilters(t *testing.T) {
	f := newFixture(t)
	f.alum(t, "Maya Chen", "maya@x.com", 2012, func(a *lumni.Alumnus) { a.Employer, a.Industry, a.Location = "Stripe", "Technology", "SF" })
	f.alum(t, "Jordan Ellis", "jordan@x.com", 2012, func(a *lumni.Alumnus) { a.Industry = "Healthcare" })
	f.alum(t, "Sam Okafor", "sam@x.com", 2018, func(a *lumni.Alumnus) { a.Employer, a.Industry = "Stripe", "technology" })

	tests := []struct {
		name   string
		filter lumni.AlumniFilter
		want   []string
	}{
		{"all", lumni.AlumniFilter{}, []string{"Jordan Ellis", "Maya Chen", "Sam Okafor"}},
		{"by year", lumni.AlumniFilter{GradYear: 2018}, []string{"Sam Okafor"}},
		{"industry case-insensitive", lumni.AlumniFilter{Industry: "TECHNOLOGY"}, []string{"Maya Chen", "Sam Okafor"}},
		{"text search employer", lumni.AlumniFilter{Query: "stripe"}, []string{"Maya Chen", "Sam Okafor"}},
		{"combined", lumni.AlumniFilter{Query: "stripe", GradYear: 2012}, []string{"Maya Chen"}},
		{"none", lumni.AlumniFilter{Location: "Mars"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.st.ListAlumni(f.school.ID, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d alumni, want %d", len(got), len(tt.want))
			}
			for i, a := range got {
				if a.Name != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, a.Name, tt.want[i])
				}
			}
		})
	}
}

func TestDuplicateEmailRejected(t *testing.T) {
	f := newFixture(t)
	f.alum(t, "Maya", "maya@x.com", 2012)
	_, err := f.st.CreateAlumnus(lumni.Alumnus{SchoolID: f.school.ID, Name: "Other", Email: "MAYA@x.com", GradYear: 2014})
	if !errors.Is(err, lumni.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestUpdateAlumnus(t *testing.T) {
	f := newFixture(t)
	a := f.alum(t, "Maya", "maya@x.com", 2012)
	employer := "Anthropic"
	got, err := f.st.UpdateAlumnus(f.school.ID, a.ID, lumni.AlumnusPatch{Employer: &employer})
	if err != nil {
		t.Fatal(err)
	}
	if got.Employer != "Anthropic" || got.Name != "Maya" {
		t.Errorf("got %+v", got)
	}

	bad := ""
	if _, err := f.st.UpdateAlumnus(f.school.ID, a.ID, lumni.AlumnusPatch{Name: &bad}); !errors.Is(err, lumni.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
	if cur, _ := f.st.GetAlumnus(f.school.ID, a.ID); cur.Name != "Maya" {
		t.Errorf("failed update mutated record: %+v", cur)
	}
}

func TestDonationMatchingRespectsCap(t *testing.T) {
	f := newFixture(t)
	c := f.campaign(t, 1_000_00, &lumni.Match{Sponsor: "Class of 1990", CapCents: 150_00})

	d1, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "A", AmountCents: 100_00})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "B", AmountCents: 100_00})
	if err != nil {
		t.Fatal(err)
	}
	d3, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "C", AmountCents: 100_00})
	if err != nil {
		t.Fatal(err)
	}
	if d1.MatchedCents != 100_00 || d2.MatchedCents != 50_00 || d3.MatchedCents != 0 {
		t.Errorf("matched = %d, %d, %d; want 10000, 5000, 0", d1.MatchedCents, d2.MatchedCents, d3.MatchedCents)
	}
	got, _ := f.st.GetCampaign(c.ID)
	if got.RaisedCents != 450_00 || got.DonorCount != 3 || got.Match.UsedCents != 150_00 {
		t.Errorf("campaign = raised %d donors %d used %d", got.RaisedCents, got.DonorCount, got.Match.UsedCents)
	}
	if pct := got.PercentFunded(); pct != 45 {
		t.Errorf("percent funded = %v, want 45", pct)
	}
}

func TestDonateRejectsClosedOrExpiredCampaign(t *testing.T) {
	f := newFixture(t)
	c := f.campaign(t, 1_000_00, nil)
	if _, err := f.st.CloseCampaign(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "A", AmountCents: 500}); !errors.Is(err, lumni.ErrConflict) {
		t.Errorf("closed: err = %v, want ErrConflict", err)
	}

	expiring, err := f.st.CreateCampaign(lumni.Campaign{SchoolID: f.school.ID, Title: "Gala", GoalCents: 100_00, Deadline: t0.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = t0.Add(2 * time.Hour)
	if _, err := f.st.Donate(expiring.ID, lumni.Donation{DonorName: "A", AmountCents: 500}); !errors.Is(err, lumni.ErrConflict) {
		t.Errorf("expired: err = %v, want ErrConflict", err)
	}
}

func TestDonationFromOtherSchoolAlumnusRejected(t *testing.T) {
	f := newFixture(t)
	other, _ := f.st.CreateSchool(lumni.School{Name: "Rival U"})
	outsider, err := f.st.CreateAlumnus(lumni.Alumnus{SchoolID: other.ID, Name: "X", Email: "x@x.com", GradYear: 2000})
	if err != nil {
		t.Fatal(err)
	}
	c := f.campaign(t, 100_00, nil)
	if _, err := f.st.Donate(c.ID, lumni.Donation{AlumnusID: outsider.ID, AmountCents: 500}); !errors.Is(err, lumni.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDonorWallHidesAnonymous(t *testing.T) {
	f := newFixture(t)
	a := f.alum(t, "Maya", "maya@x.com", 2012)
	c := f.campaign(t, 100_00, nil)
	d, err := f.st.Donate(c.ID, lumni.Donation{AlumnusID: a.ID, AmountCents: 500, Anonymous: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.DonorName != "Maya" {
		t.Errorf("donor name should default to alumnus name, got %q", d.DonorName)
	}
	wall, _ := f.st.ListDonations(c.ID)
	if len(wall) != 1 || wall[0].DonorName != "Anonymous" || wall[0].AlumnusID != "" {
		t.Errorf("wall = %+v", wall)
	}
}

func TestLeaderboardAndInsights(t *testing.T) {
	f := newFixture(t)
	maya := f.alum(t, "Maya", "maya@x.com", 2012, func(a *lumni.Alumnus) { a.Employer, a.Industry = "Stripe", "Technology" })
	jordan := f.alum(t, "Jordan", "jordan@x.com", 2012, func(a *lumni.Alumnus) { a.Industry = "Healthcare" })
	sam := f.alum(t, "Sam", "sam@x.com", 2018, func(a *lumni.Alumnus) { a.Employer, a.Industry = "Stripe", "Technology" })
	f.alum(t, "Priya", "priya@x.com", 2018)
	c := f.campaign(t, 1_000_00, nil)

	for _, d := range []lumni.Donation{
		{AlumnusID: maya.ID, AmountCents: 100_00},
		{AlumnusID: maya.ID, AmountCents: 50_00},
		{AlumnusID: jordan.ID, AmountCents: 25_00},
		{AlumnusID: sam.ID, AmountCents: 200_00},
		{DonorName: "Friend of Westbrook", AmountCents: 1_000_00},
	} {
		if _, err := f.st.Donate(c.ID, d); err != nil {
			t.Fatal(err)
		}
	}

	board, err := f.st.Leaderboard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []lumni.ClassStanding{{GradYear: 2018, RaisedCents: 200_00, Donors: 1}, {GradYear: 2012, RaisedCents: 175_00, Donors: 2}}
	if len(board) != len(want) || board[0] != want[0] || board[1] != want[1] {
		t.Errorf("leaderboard = %+v, want %+v", board, want)
	}

	in, err := f.st.Insights(f.school.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in.TotalAlumni != 4 || in.AlumniDonors != 3 || in.ParticipationRate != 75 {
		t.Errorf("insights = %+v", in)
	}
	if in.TotalRaisedCents != 1_375_00 || in.ActiveCampaigns != 1 {
		t.Errorf("raised %d active %d", in.TotalRaisedCents, in.ActiveCampaigns)
	}
	if in.ByIndustry["Technology"] != 2 || in.ByGradYear[2018] != 2 {
		t.Errorf("breakdowns = %+v %+v", in.ByIndustry, in.ByGradYear)
	}
	if len(in.TopEmployers) != 1 || in.TopEmployers[0] != (lumni.Count{Label: "Stripe", Count: 2}) {
		t.Errorf("top employers = %+v", in.TopEmployers)
	}
}
