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
	if code := c.do("POST", "/schools", map[string]any{"name": "Westbrook"}, &school); code != http.StatusCreated {
		t.Fatalf("create school: %d", code)
	}
	base := "/schools/" + school.ID

	var maya idResp
	code := c.do("POST", base+"/alumni", map[string]any{
		"name": "Maya Chen", "email": "maya@x.com", "grad_year": 2012, "employer": "Stripe", "industry": "Technology",
	}, &maya)
	if code != http.StatusCreated {
		t.Fatalf("create alumnus: %d", code)
	}

	var dir []map[string]any
	if code := c.do("GET", base+"/alumni?q=stripe&grad_year=2012", nil, &dir); code != http.StatusOK || len(dir) != 1 {
		t.Fatalf("directory: %d %v", code, dir)
	}

	var campaign struct {
		ID            string  `json:"id"`
		RaisedCents   int64   `json:"raised_cents"`
		PercentFunded float64 `json:"percent_funded"`
		Updates       []any   `json:"updates"`
	}
	code = c.do("POST", base+"/campaigns", map[string]any{
		"title": "Science Library", "goal_cents": 1000000,
		"match": map[string]any{"sponsor": "Class of 1990", "cap_cents": 100000},
	}, &campaign)
	if code != http.StatusCreated {
		t.Fatalf("create campaign: %d", code)
	}
	cpath := "/campaigns/" + campaign.ID

	var don struct {
		DonorName    string `json:"donor_name"`
		MatchedCents int64  `json:"matched_cents"`
	}
	if code := c.do("POST", cpath+"/donations", map[string]any{"alumnus_id": maya.ID, "amount_cents": 50000}, &don); code != http.StatusCreated {
		t.Fatalf("donate: %d", code)
	}
	if don.DonorName != "Maya Chen" || don.MatchedCents != 50000 {
		t.Errorf("donation = %+v", don)
	}

	if code := c.do("POST", cpath+"/updates", map[string]any{"body": "10% there!"}, &campaign); code != http.StatusCreated {
		t.Fatalf("post update: %d", code)
	}
	c.do("GET", cpath, nil, &campaign)
	if campaign.RaisedCents != 100000 || campaign.PercentFunded != 10 || len(campaign.Updates) != 1 {
		t.Errorf("campaign = %+v", campaign)
	}

	var board []map[string]any
	if code := c.do("GET", cpath+"/leaderboard", nil, &board); code != http.StatusOK || len(board) != 1 {
		t.Errorf("leaderboard: %d %v", code, board)
	}

	var insights struct {
		ParticipationRate float64 `json:"participation_rate"`
	}
	c.do("GET", base+"/insights", nil, &insights)
	if insights.ParticipationRate != 100 {
		t.Errorf("participation = %v", insights.ParticipationRate)
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
	c.do("POST", "/schools", map[string]any{"name": "Westbrook"}, &school)

	tests := []struct {
		name, method, path string
		body               any
		want               int
	}{
		{"health", "GET", "/healthz", nil, http.StatusOK},
		{"missing school", "GET", "/schools/nope", nil, http.StatusNotFound},
		{"malformed json", "POST", "/schools", "{", http.StatusBadRequest},
		{"unknown field", "POST", "/schools", `{"name":"x","bogus":1}`, http.StatusBadRequest},
		{"invalid alumnus", "POST", "/schools/" + school.ID + "/alumni", map[string]any{"name": "x", "email": "nope", "grad_year": 2010}, http.StatusBadRequest},
		{"bad grad_year query", "GET", "/schools/" + school.ID + "/alumni?grad_year=abc", nil, http.StatusBadRequest},
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
