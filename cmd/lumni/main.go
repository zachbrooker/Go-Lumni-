// Command lumni runs the Lumni API server.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zachbrooker/go-lumni/internal/api"
	"github.com/zachbrooker/go-lumni/internal/lumni"
	"github.com/zachbrooker/go-lumni/internal/store"
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

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(st, log),
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

func seedDemo(st *store.Memory, log *slog.Logger) error {
	school, err := st.CreateSchool(lumni.School{Name: "Westbrook University"})
	if err != nil {
		return err
	}
	people := []lumni.Alumnus{
		{Name: "Maya Chen", Email: "maya@example.com", GradYear: 2012, Employer: "Stripe", Title: "Engineering Manager", Industry: "Technology", Location: "San Francisco"},
		{Name: "Jordan Ellis", Email: "jordan@example.com", GradYear: 2012, Employer: "Mayo Clinic", Title: "Surgeon", Industry: "Healthcare", Location: "Rochester"},
		{Name: "Priya Nair", Email: "priya@example.com", GradYear: 2018, Employer: "Goldman Sachs", Title: "Associate", Industry: "Finance", Location: "New York"},
		{Name: "Sam Okafor", Email: "sam@example.com", GradYear: 2018, Employer: "Stripe", Title: "Product Designer", Industry: "Technology", Location: "New York"},
	}
	var ids []string
	for _, p := range people {
		p.SchoolID = school.ID
		a, err := st.CreateAlumnus(p)
		if err != nil {
			return err
		}
		ids = append(ids, a.ID)
	}
	c, err := st.CreateCampaign(lumni.Campaign{
		SchoolID:  school.ID,
		Title:     "New Science Library",
		Story:     "Help us build a 24-hour library for the next generation of Westbrook scientists.",
		GoalCents: 50_000_00,
		Match:     &lumni.Match{Sponsor: "Class of 1990", CapCents: 10_000_00},
	})
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := st.Donate(c.ID, lumni.Donation{AlumnusID: id, AmountCents: int64(i+1) * 250_00}); err != nil {
			return err
		}
	}
	log.Info("seeded demo data", "school_id", school.ID, "campaign_id", c.ID)
	return nil
}
