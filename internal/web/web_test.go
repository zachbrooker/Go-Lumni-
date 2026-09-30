package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zachbrooker/go-lumni/internal/lumni"
	"github.com/zachbrooker/go-lumni/internal/store"
)

func setup(t *testing.T) (*store.Memory, *httptest.Server) {
	t.Helper()
	st := store.NewMemory(nil)
	srv := httptest.NewServer(New(st, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)
	return st, srv
}

// get fetches path and returns the status and body.
func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// post submits a form without following redirects and returns the status and Location.
func post(t *testing.T, srv *httptest.Server, path string, form url.Values) (int, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(srv.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

func TestPagesRender(t *testing.T) {
	st, srv := setup(t)
	school, _ := st.CreateSchool(lumni.School{Name: "Canyon Ridge"})
	maya, _ := st.CreateMember(lumni.Member{SchoolID: school.ID, Kind: lumni.KindAlumnus, Name: "Maya Chen", Email: "maya@x.com", GradYear: 2018, Employer: "Stripe", Industry: "Technology"})
	dana, _ := st.CreateMember(lumni.Member{SchoolID: school.ID, Kind: lumni.KindParent, Name: "Dana Whitfield", Email: "dana@x.com", Grade: "3", Title: "Producer"})
	now := time.Now()
	c, err := st.CreateCampaign(lumni.Campaign{
		SchoolID: school.ID, Kind: lumni.KindGivingDay, Title: "Giving Day", Story: "One day.", GoalCents: 100_000_00,
		StartsAt: now.Add(-time.Hour), Deadline: now.Add(time.Hour),
		Match:      &lumni.Match{Sponsor: "The Board", CapCents: 10_000_00},
		Challenges: []lumni.Challenge{{Name: "Early Bird", Sponsor: "The Whitfields", Metric: lumni.MetricDonors, Threshold: 5, RewardCents: 25_000_00}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Donate(c.ID, lumni.Donation{MemberID: maya.ID, AmountCents: 2_500_00, Message: "Go team"})
	_, _ = st.Donate(c.ID, lumni.Donation{MemberID: dana.ID, AmountCents: 1_000_000_00})
	_, _ = st.PostUpdate(c.ID, "Halfway there")

	tests := []struct {
		path string
		want []string
	}{
		{"/", []string{"Canyon Ridge", "/s/" + school.ID}},
		{"/s/" + school.ID, []string{"Major gifts to follow up", "Dana Whitfield", "$1,000,000", "Giving Day", "giving day · live", "/give/" + c.ID, "100%", "Technology"}},
		{"/s/" + school.ID + "/people?q=stripe", []string{"Maya Chen", "Stripe", "/me/" + maya.ID}},
		{"/s/" + school.ID + "/people?kind=parent", []string{"Dana Whitfield", "Producer"}},
		{"/s/" + school.ID + "/people?industry=nope", []string{"No one matches"}},
		{"/s/" + school.ID + "/people.csv", []string{"name,email,kind", "Maya Chen,maya@x.com,Alumnus,2018", "Dana Whitfield,dana@x.com,Current parent,,3"}},
		{"/s/" + school.ID + "/gifts", []string{"Dana Whitfield", "dana@x.com", "major", "$40,100"}},
		{"/s/" + school.ID + "/gifts.csv", []string{"date,donor,email", "Maya Chen,maya@x.com,Alumnus,Giving Day,2500.00,2500.00,false", "1000000.00,7500.00,true"}},
		{"/c/" + c.ID, []string{"Giving Day", "One day.", "$1,012,500", "$100,000", "The Board", "Early Bird", "2</span> of 5 donors", "Maya Chen", "Go team", "Class of 2018", "Grade 3", "Halfway there", "Give now", "/api/campaigns/" + c.ID + "/live"}},
		{"/give/" + c.ID + "?amount=250&email=x@y.com", []string{"Giving Day", `value="250"`, `value="x@y.com"`, "Any amount", "$10,000 or more"}},
		{"/me/" + maya.ID, []string{"Hi Maya Chen", "Class of 2018", `value="Stripe"`}},
		{"/static/style.css", []string{"--signal"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, body := get(t, srv, tt.path)
			if status != http.StatusOK {
				t.Fatalf("status = %d", status)
			}
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Errorf("body missing %q", w)
				}
			}
		})
	}

	for _, p := range []string{"/c/nope", "/give/nope", "/me/nope", "/s/nope"} {
		if status, _ := get(t, srv, p); status != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", p, status)
		}
	}
}

func TestFormsRoundTrip(t *testing.T) {
	st, srv := setup(t)

	status, loc := post(t, srv, "/schools", url.Values{"name": {"Canyon Ridge"}})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/s/") {
		t.Fatalf("create school: %d %q", status, loc)
	}
	schoolID := strings.TrimPrefix(loc, "/s/")

	_, loc = post(t, srv, "/s/"+schoolID+"/people", url.Values{"kind": {"alumnus"}, "name": {"Maya Chen"}, "email": {"maya@x.com"}, "grad_year": {"2012"}})
	if !strings.Contains(loc, "ok=") {
		t.Fatalf("add alum: %q", loc)
	}
	_, loc = post(t, srv, "/s/"+schoolID+"/people", url.Values{"kind": {"parent"}, "name": {"No grade"}, "email": {"ng@x.com"}})
	if !strings.Contains(loc, "err=") {
		t.Errorf("parent without grade should fail: %q", loc)
	}
	people, _ := st.ListMembers(schoolID, lumni.MemberFilter{})
	maya := people[0]

	status, loc = post(t, srv, "/s/"+schoolID+"/campaigns", url.Values{
		"kind": {"giving_day"}, "title": {"Giving Day"}, "goal": {"$1,000,000"},
		"starts_at": {time.Now().Add(-time.Hour).Format("2006-01-02T15:04")}, "deadline": {time.Now().Add(time.Hour).Format("2006-01-02T15:04")},
		"match_sponsor": {"The Board"}, "match_cap": {"25,000"},
	})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/c/") {
		t.Fatalf("create campaign: %d %q", status, loc)
	}
	campaignID := strings.TrimPrefix(strings.SplitN(loc, "?", 2)[0], "/c/")

	_, loc = post(t, srv, "/c/"+campaignID+"/challenges", url.Values{"name": {"Alumni"}, "segment": {"alumni"}, "metric": {"donors"}, "threshold": {"1"}, "reward": {"5,000"}})
	if !strings.Contains(loc, "ok=") {
		t.Fatalf("add challenge: %q", loc)
	}

	// A gift through the share link, identified only by email, links to Maya and unlocks the alumni challenge.
	status, loc = post(t, srv, "/give/"+campaignID, url.Values{"amount": {"1,000.50"}, "donor_name": {"M. Chen"}, "donor_email": {"MAYA@x.com"}, "anonymous": {"1"}})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/c/"+campaignID+"?ok=") {
		t.Fatalf("give: %d %q", status, loc)
	}
	c, err := st.GetCampaign(campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if c.RaisedCents != 2*1_000_50+5_000_00 || c.Match.CapCents != 25_000_00 || !c.IsGivingDay() || !c.Challenges[0].Unlocked() {
		t.Errorf("campaign = raised %d cap %d kind %s unlocked %v", c.RaisedCents, c.Match.CapCents, c.Kind, c.Challenges[0].Unlocked())
	}
	ledger, _ := st.SchoolDonations(schoolID)
	if ledger[0].MemberID != maya.ID || ledger[0].DonorEmail != "maya@x.com" {
		t.Errorf("gift not linked by email: %+v", ledger[0])
	}
	wall, _ := st.ListDonations(campaignID)
	if wall[0].DonorName != "Anonymous" {
		t.Errorf("anonymous gift shown as %q", wall[0].DonorName)
	}

	// A major gift gets the follow-up note in its thank-you.
	_, loc = post(t, srv, "/give/"+campaignID, url.Values{"amount": {"1000000"}, "donor_name": {"Big"}, "donor_email": {"big@x.com"}})
	if !strings.Contains(loc, "reach+out+personally") {
		t.Errorf("major gift thank-you = %q", loc)
	}

	// Bad input redirects back to the give page with an error banner.
	_, loc = post(t, srv, "/give/"+campaignID, url.Values{"amount": {"abc"}})
	if !strings.HasPrefix(loc, "/give/"+campaignID+"?err=") {
		t.Errorf("bad amount location = %q", loc)
	}
	status, body := get(t, srv, loc)
	if status != http.StatusOK || !strings.Contains(body, `class="banner error"`) {
		t.Errorf("error banner missing: %d", status)
	}

	// Members update their own profile from their link.
	_, loc = post(t, srv, "/me/"+maya.ID, url.Values{"title": {"CTO"}, "employer": {"Acme"}, "location": {"LA"}})
	if !strings.Contains(loc, "ok=") {
		t.Fatalf("update me: %q", loc)
	}
	if m, _ := st.GetMember(schoolID, maya.ID); m.Title != "CTO" || m.Employer != "Acme" || m.Name != "Maya Chen" {
		t.Errorf("profile = %+v", m)
	}

	if status, _ := post(t, srv, "/c/"+campaignID+"/close", nil); status != http.StatusSeeOther {
		t.Errorf("close: %d", status)
	}
	_, loc = post(t, srv, "/give/"+campaignID, url.Values{"amount": {"5"}, "donor_name": {"Late"}})
	if !strings.Contains(loc, "err=") {
		t.Errorf("gift to closed campaign should error, got %q", loc)
	}
}
