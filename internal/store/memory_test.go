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
	s, err := st.CreateSchool(lumni.School{Name: "Canyon Ridge"})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{st: st, now: &now, school: s}
}

func (f *fixture) alum(t *testing.T, name, email string, year int, mods ...func(*lumni.Member)) lumni.Member {
	t.Helper()
	return f.member(t, lumni.Member{Kind: lumni.KindAlumnus, Name: name, Email: email, GradYear: year}, mods...)
}

func (f *fixture) parent(t *testing.T, name, email, grade string) lumni.Member {
	t.Helper()
	return f.member(t, lumni.Member{Kind: lumni.KindParent, Name: name, Email: email, Grade: grade})
}

func (f *fixture) member(t *testing.T, m lumni.Member, mods ...func(*lumni.Member)) lumni.Member {
	t.Helper()
	m.SchoolID = f.school.ID
	for _, mod := range mods {
		mod(&m)
	}
	out, err := f.st.CreateMember(m)
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

func TestDirectoryFilters(t *testing.T) {
	f := newFixture(t)
	f.alum(t, "Maya Chen", "maya@x.com", 2012, func(m *lumni.Member) { m.Employer, m.Industry, m.Location = "Stripe", "Technology", "SF" })
	f.alum(t, "Jordan Ellis", "jordan@x.com", 2012, func(m *lumni.Member) { m.Industry = "Healthcare" })
	f.alum(t, "Sam Okafor", "sam@x.com", 2018, func(m *lumni.Member) { m.Employer, m.Industry = "Stripe", "technology" })
	f.parent(t, "Dana Whitfield", "dana@x.com", "3")

	tests := []struct {
		name   string
		filter lumni.MemberFilter
		want   []string
	}{
		{"all, alumni first then parents", lumni.MemberFilter{}, []string{"Jordan Ellis", "Maya Chen", "Sam Okafor", "Dana Whitfield"}},
		{"by year", lumni.MemberFilter{GradYear: 2018}, []string{"Sam Okafor"}},
		{"by kind", lumni.MemberFilter{Kind: lumni.KindParent}, []string{"Dana Whitfield"}},
		{"by grade", lumni.MemberFilter{Grade: "3"}, []string{"Dana Whitfield"}},
		{"industry case-insensitive", lumni.MemberFilter{Industry: "TECHNOLOGY"}, []string{"Maya Chen", "Sam Okafor"}},
		{"text search employer", lumni.MemberFilter{Query: "stripe"}, []string{"Maya Chen", "Sam Okafor"}},
		{"combined", lumni.MemberFilter{Query: "stripe", GradYear: 2012}, []string{"Maya Chen"}},
		{"none", lumni.MemberFilter{Location: "Mars"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.st.ListMembers(f.school.ID, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d members, want %d", len(got), len(tt.want))
			}
			for i, m := range got {
				if m.Name != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, m.Name, tt.want[i])
				}
			}
		})
	}
}

func TestMemberValidation(t *testing.T) {
	f := newFixture(t)
	bad := []lumni.Member{
		{Kind: lumni.KindAlumnus, Name: "No year", Email: "a@x.com"},
		{Kind: lumni.KindParent, Name: "No grade", Email: "b@x.com"},
		{Kind: lumni.KindParent, Name: "Bad grade", Email: "c@x.com", Grade: "14"},
		{Kind: "robot", Name: "Bad kind", Email: "d@x.com"},
	}
	for _, m := range bad {
		m.SchoolID = f.school.ID
		if _, err := f.st.CreateMember(m); !errors.Is(err, lumni.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", m.Name, err)
		}
	}
	f.alum(t, "Maya", "maya@x.com", 2012)
	if _, err := f.st.CreateMember(lumni.Member{SchoolID: f.school.ID, Kind: lumni.KindAlumnus, Name: "Dup", Email: "MAYA@x.com", GradYear: 2014}); !errors.Is(err, lumni.ErrConflict) {
		t.Errorf("duplicate email: err = %v, want ErrConflict", err)
	}
}

func TestUpdateMember(t *testing.T) {
	f := newFixture(t)
	m := f.alum(t, "Maya", "maya@x.com", 2012)
	employer := "Anthropic"
	got, err := f.st.UpdateMember(f.school.ID, m.ID, lumni.MemberPatch{Employer: &employer})
	if err != nil {
		t.Fatal(err)
	}
	if got.Employer != "Anthropic" || got.Name != "Maya" {
		t.Errorf("got %+v", got)
	}
	bad := ""
	if _, err := f.st.UpdateMember(f.school.ID, m.ID, lumni.MemberPatch{Name: &bad}); !errors.Is(err, lumni.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
	if cur, _ := f.st.GetMember(f.school.ID, m.ID); cur.Name != "Maya" {
		t.Errorf("failed update mutated record: %+v", cur)
	}
}

func TestDonationMatchingRespectsCap(t *testing.T) {
	f := newFixture(t)
	c := f.campaign(t, 1_000_00, &lumni.Match{Sponsor: "Class of 1990", CapCents: 150_00})

	var matched []int64
	for _, name := range []string{"A", "B", "C"} {
		d, err := f.st.Donate(c.ID, lumni.Donation{DonorName: name, AmountCents: 100_00})
		if err != nil {
			t.Fatal(err)
		}
		matched = append(matched, d.MatchedCents)
	}
	if matched[0] != 100_00 || matched[1] != 50_00 || matched[2] != 0 {
		t.Errorf("matched = %v; want [10000 5000 0]", matched)
	}
	got, _ := f.st.GetCampaign(c.ID)
	if got.RaisedCents != 450_00 || got.BonusCents != 150_00 || got.DonorCount != 3 || got.Match.UsedCents != 150_00 {
		t.Errorf("campaign = raised %d bonus %d donors %d used %d", got.RaisedCents, got.BonusCents, got.DonorCount, got.Match.UsedCents)
	}
	if pct := got.PercentFunded(); pct != 45 {
		t.Errorf("percent funded = %v, want 45", pct)
	}
}

func TestPlatformFeeAndMajorGift(t *testing.T) {
	f := newFixture(t)
	c := f.campaign(t, 100_000_00, nil)
	d, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "Big", AmountCents: 1_000_000_00})
	if err != nil {
		t.Fatal(err)
	}
	if d.FeeCents != 40_000_00 || !d.Major() {
		t.Errorf("fee = %d major = %v", d.FeeCents, d.Major())
	}
	small, _ := f.st.Donate(c.ID, lumni.Donation{DonorName: "Small", AmountCents: 100_00})
	if small.FeeCents != 4_00 || small.Major() {
		t.Errorf("small fee = %d major = %v", small.FeeCents, small.Major())
	}
	in, _ := f.st.Insights(f.school.ID)
	if in.PlatformFeeCents != 40_004_00 {
		t.Errorf("platform fee = %d", in.PlatformFeeCents)
	}
}

func TestDonateLinksByEmail(t *testing.T) {
	f := newFixture(t)
	maya := f.alum(t, "Maya Chen", "maya@x.com", 2012)
	c := f.campaign(t, 100_00, nil)
	d, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "M. Chen", DonorEmail: "Maya@X.com", AmountCents: 500})
	if err != nil {
		t.Fatal(err)
	}
	if d.MemberID != maya.ID || d.DonorKind != "Alumnus" || d.DonorName != "M. Chen" {
		t.Errorf("donation = %+v", d)
	}
	wall, _ := f.st.ListDonations(c.ID)
	if wall[0].DonorEmail != "" {
		t.Errorf("email leaked on donor wall: %+v", wall[0])
	}
	ledger, _ := f.st.SchoolDonations(f.school.ID)
	if ledger[0].DonorEmail != "maya@x.com" {
		t.Errorf("school ledger should keep the email: %+v", ledger[0])
	}
}

func TestDonateRejectsClosedExpiredOrUnstarted(t *testing.T) {
	f := newFixture(t)
	c := f.campaign(t, 1_000_00, nil)
	if _, err := f.st.CloseCampaign(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Donate(c.ID, lumni.Donation{DonorName: "A", AmountCents: 500}); !errors.Is(err, lumni.ErrConflict) {
		t.Errorf("closed: err = %v, want ErrConflict", err)
	}

	day, err := f.st.CreateCampaign(lumni.Campaign{
		SchoolID: f.school.ID, Kind: lumni.KindGivingDay, Title: "Giving Day", GoalCents: 100_00,
		StartsAt: t0.Add(time.Hour), Deadline: t0.Add(25 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Donate(day.ID, lumni.Donation{DonorName: "A", AmountCents: 500}); !errors.Is(err, lumni.ErrConflict) {
		t.Errorf("unstarted: err = %v, want ErrConflict", err)
	}
	*f.now = t0.Add(2 * time.Hour)
	if _, err := f.st.Donate(day.ID, lumni.Donation{DonorName: "A", AmountCents: 500}); err != nil {
		t.Errorf("during: err = %v", err)
	}
	*f.now = t0.Add(26 * time.Hour)
	if _, err := f.st.Donate(day.ID, lumni.Donation{DonorName: "A", AmountCents: 500}); !errors.Is(err, lumni.ErrConflict) {
		t.Errorf("expired: err = %v, want ErrConflict", err)
	}

	if _, err := f.st.CreateCampaign(lumni.Campaign{SchoolID: f.school.ID, Kind: lumni.KindGivingDay, Title: "No window", GoalCents: 100}); !errors.Is(err, lumni.ErrValidation) {
		t.Errorf("giving day without window: err = %v, want ErrValidation", err)
	}
}

func TestDonationFromOtherSchoolMemberRejected(t *testing.T) {
	f := newFixture(t)
	other, _ := f.st.CreateSchool(lumni.School{Name: "Rival"})
	outsider, err := f.st.CreateMember(lumni.Member{SchoolID: other.ID, Kind: lumni.KindAlumnus, Name: "X", Email: "x@x.com", GradYear: 2000})
	if err != nil {
		t.Fatal(err)
	}
	c := f.campaign(t, 100_00, nil)
	if _, err := f.st.Donate(c.ID, lumni.Donation{MemberID: outsider.ID, AmountCents: 500}); !errors.Is(err, lumni.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDonorWallHidesAnonymous(t *testing.T) {
	f := newFixture(t)
	m := f.alum(t, "Maya", "maya@x.com", 2012)
	c := f.campaign(t, 100_00, nil)
	d, err := f.st.Donate(c.ID, lumni.Donation{MemberID: m.ID, AmountCents: 500, Anonymous: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.DonorName != "Maya" {
		t.Errorf("donor name should default to member name, got %q", d.DonorName)
	}
	wall, _ := f.st.ListDonations(c.ID)
	if len(wall) != 1 || wall[0].DonorName != "Anonymous" || wall[0].MemberID != "" || wall[0].DonorKind != "" {
		t.Errorf("wall = %+v", wall)
	}
}

func TestChallengesUnlock(t *testing.T) {
	f := newFixture(t)
	maya := f.alum(t, "Maya", "maya@x.com", 2012)
	sam := f.alum(t, "Sam", "sam@x.com", 2018)
	dana := f.parent(t, "Dana", "dana@x.com", "3")
	c, err := f.st.CreateCampaign(lumni.Campaign{
		SchoolID: f.school.ID, Kind: lumni.KindGivingDay, Title: "Day", GoalCents: 1_000_00,
		StartsAt: t0.Add(-time.Hour), Deadline: t0.Add(time.Hour),
		Challenges: []lumni.Challenge{
			{Name: "Alumni", Segment: "alumni", Metric: lumni.MetricDonors, Threshold: 2, RewardCents: 100_00},
			{Name: "Grade 3 dollars", Segment: "grade:3", Metric: lumni.MetricDollars, Threshold: 50_00, RewardCents: 200_00},
			{Name: "Late window", Metric: lumni.MetricDonors, Threshold: 1, RewardCents: 1, StartsAt: t0.Add(30 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	give := func(memberID, name string, amount int64) {
		t.Helper()
		if _, err := f.st.Donate(c.ID, lumni.Donation{MemberID: memberID, DonorName: name, AmountCents: amount}); err != nil {
			t.Fatal(err)
		}
	}
	give("", "Friend", 10_00) // counts for no segment-restricted challenge; late window not open yet
	give(maya.ID, "", 10_00)  // alumni 1/2
	give(dana.ID, "", 30_00)  // grade 3: $30 of $50
	got, _ := f.st.GetCampaign(c.ID)
	if got.Challenges[0].Progress != 1 || got.Challenges[1].Progress != 30_00 || got.Challenges[2].Progress != 0 || got.BonusCents != 0 {
		t.Fatalf("mid-way: %+v bonus %d", got.Challenges, got.BonusCents)
	}
	give(sam.ID, "", 10_00)  // alumni 2/2 → unlock $100
	give(dana.ID, "", 25_00) // grade 3: $55 → unlock $200
	got, _ = f.st.GetCampaign(c.ID)
	if !got.Challenges[0].Unlocked() || !got.Challenges[1].Unlocked() || got.Challenges[2].Unlocked() {
		t.Errorf("unlocked = %v %v %v", got.Challenges[0].Unlocked(), got.Challenges[1].Unlocked(), got.Challenges[2].Unlocked())
	}
	if got.BonusCents != 300_00 || got.RaisedCents != 85_00+300_00 {
		t.Errorf("bonus %d raised %d", got.BonusCents, got.RaisedCents)
	}
	// A further alumni gift does not re-trigger an unlocked challenge.
	give(maya.ID, "", 10_00)
	got, _ = f.st.GetCampaign(c.ID)
	if got.Challenges[0].Progress != 2 || got.BonusCents != 300_00 {
		t.Errorf("re-trigger: progress %d bonus %d", got.Challenges[0].Progress, got.BonusCents)
	}
}

func TestLeaderboardsAndInsights(t *testing.T) {
	f := newFixture(t)
	maya := f.alum(t, "Maya", "maya@x.com", 2012, func(m *lumni.Member) { m.Employer, m.Industry = "Stripe", "Technology" })
	jordan := f.alum(t, "Jordan", "jordan@x.com", 2012, func(m *lumni.Member) { m.Industry = "Healthcare" })
	sam := f.alum(t, "Sam", "sam@x.com", 2018, func(m *lumni.Member) { m.Employer, m.Industry = "Stripe", "Technology" })
	f.alum(t, "Priya", "priya@x.com", 2018)
	dana := f.parent(t, "Dana", "dana@x.com", "3")
	f.parent(t, "Chris", "chris@x.com", "3")
	f.parent(t, "Nadia", "nadia@x.com", "7")
	c := f.campaign(t, 1_000_00, nil)

	for _, d := range []lumni.Donation{
		{MemberID: maya.ID, AmountCents: 100_00},
		{MemberID: maya.ID, AmountCents: 50_00},
		{MemberID: jordan.ID, AmountCents: 25_00},
		{MemberID: sam.ID, AmountCents: 200_00},
		{MemberID: dana.ID, AmountCents: 500_00},
		{DonorName: "Friend", AmountCents: 1_000_00},
	} {
		if _, err := f.st.Donate(c.ID, d); err != nil {
			t.Fatal(err)
		}
	}

	byClass, err := f.st.Leaderboard(c.ID, lumni.ByClass)
	if err != nil {
		t.Fatal(err)
	}
	want := []lumni.Standing{
		{Key: "2012", Label: "Class of 2012", RaisedCents: 175_00, Donors: 2, Members: 2, Participation: 100},
		{Key: "2018", Label: "Class of 2018", RaisedCents: 200_00, Donors: 1, Members: 2, Participation: 50},
	}
	if len(byClass) != 2 || byClass[0] != want[0] || byClass[1] != want[1] {
		t.Errorf("by class = %+v, want %+v", byClass, want)
	}
	byGrade, _ := f.st.Leaderboard(c.ID, lumni.ByGrade)
	wantGrade := []lumni.Standing{
		{Key: "3", Label: "Grade 3", RaisedCents: 500_00, Donors: 1, Members: 2, Participation: 50},
		{Key: "7", Label: "Grade 7", Members: 1},
	}
	if len(byGrade) != 2 || byGrade[0] != wantGrade[0] || byGrade[1] != wantGrade[1] {
		t.Errorf("by grade = %+v, want %+v", byGrade, wantGrade)
	}

	in, err := f.st.Insights(f.school.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in.TotalMembers != 7 || in.Donors != 4 || in.ByKind[lumni.KindAlumnus] != 4 || in.ByKind[lumni.KindParent] != 3 {
		t.Errorf("insights = %+v", in)
	}
	if in.AlumniParticipation != 75 || in.ParentParticipation < 33.3 || in.ParentParticipation > 33.4 {
		t.Errorf("participation alumni %v parents %v", in.AlumniParticipation, in.ParentParticipation)
	}
	if in.TotalRaisedCents != 1_875_00 || in.ActiveCampaigns != 1 || in.RaisedByKindCents[lumni.KindParent] != 500_00 {
		t.Errorf("raised %d active %d by parent %d", in.TotalRaisedCents, in.ActiveCampaigns, in.RaisedByKindCents[lumni.KindParent])
	}
	if in.ByIndustry["Technology"] != 2 || in.ByGradYear[2018] != 2 || in.ByGrade["3"] != 2 {
		t.Errorf("breakdowns = %+v %+v %+v", in.ByIndustry, in.ByGradYear, in.ByGrade)
	}
	if len(in.TopEmployers) != 1 || in.TopEmployers[0] != (lumni.Count{Label: "Stripe", Count: 2}) {
		t.Errorf("top employers = %+v", in.TopEmployers)
	}
}
