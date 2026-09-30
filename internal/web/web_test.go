package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
	school, _ := st.CreateSchool(lumni.School{Name: "Sierra Canyon"})
	alum, _ := st.CreateAlumnus(lumni.Alumnus{SchoolID: school.ID, Name: "Maya Chen", Email: "maya@x.com", GradYear: 2018, Employer: "Stripe", Industry: "Technology"})
	c, _ := st.CreateCampaign(lumni.Campaign{SchoolID: school.ID, Title: "New Gym", Story: "A gym for all.", GoalCents: 100_000_00, Match: &lumni.Match{Sponsor: "Class of 2010", CapCents: 10_000_00}})
	_, _ = st.Donate(c.ID, lumni.Donation{AlumnusID: alum.ID, AmountCents: 2_500_00, Message: "Go team"})
	_, _ = st.PostUpdate(c.ID, "Halfway there")

	tests := []struct {
		path string
		want []string
	}{
		{"/", []string{"Sierra Canyon", "/s/" + school.ID}},
		{"/s/" + school.ID, []string{"$5,000", "100.0%", "New Gym", "Technology"}},
		{"/s/" + school.ID + "/alumni?q=stripe", []string{"Maya Chen", "Stripe"}},
		{"/s/" + school.ID + "/alumni?industry=nope", []string{"No alumni match"}},
		{"/c/" + c.ID, []string{"New Gym", "A gym for all.", "$5,000", "$100,000", "Class of 2010", "$7,500 in matching", "Maya Chen", "Go team", "Class of 2018", "Halfway there", "Give now"}},
		{"/static/style.css", []string{"--accent"}},
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

	if status, _ := get(t, srv, "/c/nope"); status != http.StatusNotFound {
		t.Errorf("missing campaign status = %d", status)
	}
}

func TestFormsRoundTrip(t *testing.T) {
	st, srv := setup(t)

	status, loc := post(t, srv, "/schools", url.Values{"name": {"Sierra Canyon"}})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/s/") {
		t.Fatalf("create school: %d %q", status, loc)
	}
	schoolID := strings.TrimPrefix(loc, "/s/")

	status, loc = post(t, srv, "/s/"+schoolID+"/campaigns", url.Values{
		"title": {"New Gym"}, "goal": {"250000"}, "deadline": {"2030-06-30"},
		"match_sponsor": {"Class of 2010"}, "match_cap": {"25,000"},
	})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/c/") {
		t.Fatalf("create campaign: %d %q", status, loc)
	}
	campaignID := strings.TrimPrefix(loc, "/c/")

	status, loc = post(t, srv, "/c/"+campaignID+"/give", url.Values{"amount": {"1,000.50"}, "donor_name": {"Zach"}, "anonymous": {"1"}})
	if status != http.StatusSeeOther || strings.Contains(loc, "err=") {
		t.Fatalf("give: %d %q", status, loc)
	}
	c, err := st.GetCampaign(campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if c.RaisedCents != 2*1_000_50 || c.Match.CapCents != 25_000_00 || c.Deadline.Year() != 2030 {
		t.Errorf("campaign = raised %d cap %d deadline %v", c.RaisedCents, c.Match.CapCents, c.Deadline)
	}
	wall, _ := st.ListDonations(campaignID)
	if wall[0].DonorName != "Anonymous" {
		t.Errorf("anonymous gift shown as %q", wall[0].DonorName)
	}

	// Bad input redirects back with an error banner rather than a bare error.
	_, loc = post(t, srv, "/c/"+campaignID+"/give", url.Values{"amount": {"abc"}})
	if !strings.Contains(loc, "err=") {
		t.Errorf("bad amount location = %q, want err param", loc)
	}
	status, body := get(t, srv, loc)
	if status != http.StatusOK || !strings.Contains(body, `class="banner error"`) {
		t.Errorf("error banner missing: %d", status)
	}

	if status, _ := post(t, srv, "/c/"+campaignID+"/close", nil); status != http.StatusSeeOther {
		t.Errorf("close: %d", status)
	}
	_, loc = post(t, srv, "/c/"+campaignID+"/give", url.Values{"amount": {"5"}, "donor_name": {"Late"}})
	if !strings.Contains(loc, "err=") {
		t.Errorf("gift to closed campaign should error, got %q", loc)
	}
}
