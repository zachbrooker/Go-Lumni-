package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zachbrooker/go-lumni/internal/store"
)

type client struct {
	t   *testing.T
	srv *httptest.Server
}

func newClient(t *testing.T) *client {
	t.Helper()
	srv := httptest.NewServer(New(store.NewMemory(nil), slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)
	return &client{t: t, srv: srv}
}

// do sends a request and decodes the JSON response into out (if non-nil), returning the status.
func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			r = bytes.NewBufferString(s)
		} else {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

type idResp struct {
	ID string `json:"id"`
}

func TestFundraisingFlow(t *testing.T) {
	c := newClient(t)

	var school idResp
	if code := c.do("POST", "/schools", map[string]any{"name": "Canyon Ridge"}, &school); code != http.StatusCreated {
		t.Fatalf("create school: %d", code)
	}
	base := "/schools/" + school.ID

	var maya idResp
	code := c.do("POST", base+"/members", map[string]any{
		"kind": "alumnus", "name": "Maya Chen", "email": "maya@x.com", "grad_year": 2012, "employer": "Stripe", "industry": "Technology",
	}, &maya)
	if code != http.StatusCreated {
		t.Fatalf("create member: %d", code)
	}
	if code := c.do("POST", base+"/members", map[string]any{"kind": "parent", "name": "Dana", "email": "dana@x.com", "grade": "3"}, nil); code != http.StatusCreated {
		t.Fatalf("create parent: %d", code)
	}

	var dir []map[string]any
	if code := c.do("GET", base+"/members?q=stripe&grad_year=2012", nil, &dir); code != http.StatusOK || len(dir) != 1 {
		t.Fatalf("directory: %d %v", code, dir)
	}
	if code := c.do("GET", base+"/members?kind=parent", nil, &dir); code != http.StatusOK || len(dir) != 1 {
		t.Fatalf("directory by kind: %d %v", code, dir)
	}

	var campaign struct {
		ID            string  `json:"id"`
		RaisedCents   int64   `json:"raised_cents"`
		PercentFunded float64 `json:"percent_funded"`
		Open          bool    `json:"open"`
		Updates       []any   `json:"updates"`
		Challenges    []struct {
			Progress   int64  `json:"progress"`
			UnlockedAt string `json:"unlocked_at"`
		} `json:"challenges"`
	}
	code = c.do("POST", base+"/campaigns", map[string]any{
		"title": "Science Library", "goal_cents": 1000000,
		"match":      map[string]any{"sponsor": "Class of 1990", "cap_cents": 100000},
		"challenges": []map[string]any{{"name": "Alumni", "segment": "alumni", "metric": "donors", "threshold": 1, "reward_cents": 50000}},
	}, &campaign)
	if code != http.StatusCreated || !campaign.Open {
		t.Fatalf("create campaign: %d open=%v", code, campaign.Open)
	}
	cpath := "/campaigns/" + campaign.ID

	var don struct {
		DonorName    string `json:"donor_name"`
		MemberID     string `json:"member_id"`
		MatchedCents int64  `json:"matched_cents"`
		FeeCents     int64  `json:"fee_cents"`
	}
	if code := c.do("POST", cpath+"/donations", map[string]any{"donor_email": "maya@x.com", "donor_name": "Maya", "amount_cents": 50000}, &don); code != http.StatusCreated {
		t.Fatalf("donate: %d", code)
	}
	if don.MemberID != maya.ID || don.MatchedCents != 50000 || don.FeeCents != 2000 {
		t.Errorf("donation = %+v", don)
	}

	if code := c.do("POST", cpath+"/updates", map[string]any{"body": "10% there!"}, &campaign); code != http.StatusCreated {
		t.Fatalf("post update: %d", code)
	}
	c.do("GET", cpath, nil, &campaign)
	// $500 gift + $500 match + $500 alumni challenge unlock = $1,500 of $10,000.
	if campaign.RaisedCents != 150000 || campaign.PercentFunded != 15 || len(campaign.Updates) != 1 || campaign.Challenges[0].UnlockedAt == "" {
		t.Errorf("campaign = %+v", campaign)
	}

	var live struct {
		RaisedCents int64 `json:"raised_cents"`
		ByClass     []struct {
			Participation float64 `json:"participation"`
		} `json:"by_class"`
	}
	if code := c.do("GET", cpath+"/live", nil, &live); code != http.StatusOK || live.RaisedCents != 150000 || len(live.ByClass) != 1 || live.ByClass[0].Participation != 100 {
		t.Errorf("live: %d %+v", code, live)
	}

	var board []map[string]any
	if code := c.do("GET", cpath+"/leaderboard?by=grade", nil, &board); code != http.StatusOK || len(board) != 1 {
		t.Errorf("grade leaderboard: %d %v", code, board)
	}
	if code := c.do("GET", cpath+"/leaderboard?by=zodiac", nil, nil); code != http.StatusBadRequest {
		t.Errorf("bad leaderboard: %d", code)
	}

	var insights struct {
		AlumniParticipation float64 `json:"alumni_participation"`
		PlatformFeeCents    int64   `json:"platform_fee_cents"`
	}
	c.do("GET", base+"/insights", nil, &insights)
	if insights.AlumniParticipation != 100 || insights.PlatformFeeCents != 2000 {
		t.Errorf("insights = %+v", insights)
	}

	if code := c.do("POST", cpath+"/close", nil, nil); code != http.StatusOK {
		t.Fatalf("close: %d", code)
	}
	if code := c.do("POST", cpath+"/donations", map[string]any{"donor_name": "Late", "amount_cents": 500}, nil); code != http.StatusConflict {
		t.Errorf("donate to closed campaign: %d, want 409", code)
	}
}

func TestErrorStatuses(t *testing.T) {
	c := newClient(t)
	var school idResp
	c.do("POST", "/schools", map[string]any{"name": "Canyon Ridge"}, &school)

	tests := []struct {
		name, method, path string
		body               any
		want               int
	}{
		{"health", "GET", "/healthz", nil, http.StatusOK},
		{"missing school", "GET", "/schools/nope", nil, http.StatusNotFound},
		{"malformed json", "POST", "/schools", "{", http.StatusBadRequest},
		{"unknown field", "POST", "/schools", `{"name":"x","bogus":1}`, http.StatusBadRequest},
		{"invalid member", "POST", "/schools/" + school.ID + "/members", map[string]any{"name": "x", "email": "nope", "grad_year": 2010}, http.StatusBadRequest},
		{"bad grad_year query", "GET", "/schools/" + school.ID + "/members?grad_year=abc", nil, http.StatusBadRequest},
		{"giving day without window", "POST", "/schools/" + school.ID + "/campaigns", map[string]any{"kind": "giving_day", "title": "x", "goal_cents": 100}, http.StatusBadRequest},
		{"missing campaign", "GET", "/campaigns/nope", nil, http.StatusNotFound},
		{"wrong method", "DELETE", "/schools", nil, http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.do(tt.method, tt.path, tt.body, nil); got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}
