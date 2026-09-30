// Command lumni runs the Lumni server: the HTML UI at / and the JSON API at /api.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zachbrooker/go-lumni/internal/api"
	"github.com/zachbrooker/go-lumni/internal/lumni"
	"github.com/zachbrooker/go-lumni/internal/store"
	"github.com/zachbrooker/go-lumni/internal/web"
)

func main() {
	addr := flag.String("addr", envOr("LUMNI_ADDR", ":8080"), "listen address")
	seed := flag.Bool("seed", false, "load demo data on startup")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	st := store.NewMemory(nil)
	if *seed {
		if err := seedDemo(st, log); err != nil {
			log.Error("seed failed", "err", err)
			os.Exit(1)
		}
	}

	// The JSON API lives under /api; the HTML UI owns everything else.
	root := http.NewServeMux()
	root.Handle("/api/", http.StripPrefix("/api", api.New(st, log)))
	root.Handle("/", web.New(st, log))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("lumni listening", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("lumni stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// seedDemo loads a fictional PreK–12 independent school whose first senior
// class graduated in 2009, so its alumni are young and its parents give most,
// plus a giving day that is live right now.
func seedDemo(st *store.Memory, log *slog.Logger) error {
	school, err := st.CreateSchool(lumni.School{Name: "Canyon Ridge School"})
	if err != nil {
		return err
	}
	type person struct {
		kind                                  lumni.MemberKind
		name, email                           string
		year                                  int
		grade, title, employer, industry, loc string
	}
	people := []person{
		{lumni.KindAlumnus, "Marcus Bell", "marcus@example.com", 2012, "", "Point guard", "Sacramento Kings", "Sports", "Sacramento"},
		{lumni.KindAlumnus, "Maya Chen", "maya@example.com", 2012, "", "Engineering Manager", "Stripe", "Technology", "San Francisco"},
		{lumni.KindAlumnus, "Willa Park", "willa@example.com", 2015, "", "Recording artist", "Independent", "Entertainment", "Los Angeles"},
		{lumni.KindAlumnus, "Jordan Ellis", "jordan@example.com", 2015, "", "Resident physician", "Cedars-Sinai", "Healthcare", "Los Angeles"},
		{lumni.KindAlumnus, "Priya Nair", "priya@example.com", 2018, "", "Associate", "Goldman Sachs", "Finance", "New York"},
		{lumni.KindAlumnus, "Sam Okafor", "sam@example.com", 2018, "", "Product Designer", "Figma", "Technology", "New York"},
		{lumni.KindAlumnus, "Leo Ramirez", "leo@example.com", 2021, "", "Wide receiver", "USC (NIL)", "Sports", "Los Angeles"},
		{lumni.KindAlumnus, "Ava Goldstein", "ava@example.com", 2024, "", "Student", "Stanford", "Education", "Palo Alto"},
		{lumni.KindParent, "Dana Whitfield", "dana@example.com", 0, "3", "Producer", "Warner Bros.", "Entertainment", "Los Angeles"},
		{lumni.KindParent, "Chris Tanaka", "chris@example.com", 0, "3", "Partner", "Kirkland & Ellis", "Law", "Los Angeles"},
		{lumni.KindParent, "Nadia Rahman", "nadia@example.com", 0, "7", "Founder", "Rahman Capital", "Finance", "Calabasas"},
		{lumni.KindParent, "Tom Beckett", "tom@example.com", 0, "7", "Head of Studio", "Riot Games", "Technology", "Los Angeles"},
		{lumni.KindParent, "Elena Vasquez", "elena@example.com", 0, "11", "Surgeon", "UCLA Health", "Healthcare", "Los Angeles"},
		{lumni.KindParent, "Grant Holloway", "grant@example.com", 0, "11", "Agent", "CAA", "Entertainment", "Beverly Hills"},
		{lumni.KindAlumniParent, "Ruth Bell", "ruth@example.com", 2012, "", "Retired", "", "", "Chatsworth"},
		{lumni.KindGrandparent, "Harold Whitfield", "harold@example.com", 0, "3", "", "", "", "Malibu"},
	}
	ids := map[string]string{}
	for _, p := range people {
		m, err := st.CreateMember(lumni.Member{
			SchoolID: school.ID, Kind: p.kind, Name: p.name, Email: p.email, GradYear: p.year, Grade: p.grade,
			Title: p.title, Employer: p.employer, Industry: p.industry, Location: p.loc,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		ids[p.email] = m.ID
	}

	now := time.Now()
	day, err := st.CreateCampaign(lumni.Campaign{
		SchoolID:  school.ID,
		Kind:      lumni.KindGivingDay,
		Title:     "Giving Day 2026",
		Story:     "24 hours. One community. Every gift to the Trailblazer Fund today is matched by the Board, and every grade and class that shows up unlocks more.",
		GoalCents: 1_000_000_00,
		StartsAt:  now.Add(-6 * time.Hour),
		Deadline:  now.Add(18 * time.Hour),
		Match:     &lumni.Match{Sponsor: "The Board of Trustees", CapCents: 250_000_00},
		Challenges: []lumni.Challenge{
			{Name: "Early Bird", Sponsor: "The Whitfield Family", Metric: lumni.MetricDonors, Threshold: 5, RewardCents: 25_000_00},
			{Name: "Alumni Challenge", Sponsor: "Marcus Bell ’12", Segment: "alumni", Metric: lumni.MetricDonors, Threshold: 10, RewardCents: 50_000_00},
			{Name: "Grade 7 Challenge", Sponsor: "An anonymous 7th-grade family", Segment: "grade:7", Metric: lumni.MetricDollars, Threshold: 20_000_00, RewardCents: 20_000_00},
		},
	})
	if err != nil {
		return err
	}
	gifts := []struct {
		email  string
		amount int64
		msg    string
	}{
		{"dana@example.com", 25_000_00, "For the arts center."},
		{"chris@example.com", 10_000_00, ""},
		{"nadia@example.com", 50_000_00, "Proud 7th-grade family."},
		{"elena@example.com", 5_000_00, ""},
		{"marcus@example.com", 1_000_000_00, "This school changed my life. Let's build the gym."},
		{"maya@example.com", 500_00, "Class of 2012!"},
		{"leo@example.com", 250_00, "First NIL check, first gift back."},
		{"ava@example.com", 100_00, ""},
		{"ruth@example.com", 2_500_00, ""},
	}
	for _, g := range gifts {
		if _, err := st.Donate(day.ID, lumni.Donation{MemberID: ids[g.email], AmountCents: g.amount, Message: g.msg}); err != nil {
			return err
		}
	}
	if _, err := st.PostUpdate(day.ID, "Six hours in and we've passed $1.3M thanks to a transformational gift from Marcus Bell ’12. Alumni: 8 more of you unlock another $50K."); err != nil {
		return err
	}

	arts, err := st.CreateCampaign(lumni.Campaign{
		SchoolID:  school.ID,
		Title:     "Center for the Arts",
		Story:     "A 600-seat theater, recording studios and galleries for the next generation of Canyon Ridge artists.",
		GoalCents: 24_000_000_00,
	})
	if err != nil {
		return err
	}
	if _, err := st.Donate(arts.ID, lumni.Donation{DonorName: "Friend of Canyon Ridge", DonorEmail: "friend@example.com", AmountCents: 2_500_000_00, Anonymous: true}); err != nil {
		return err
	}
	log.Info("seeded demo data", "school_id", school.ID, "giving_day_id", day.ID)
	return nil
}
