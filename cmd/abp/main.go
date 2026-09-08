package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jpequegn/agent-budget-proxy/internal/proxy"
	"github.com/spf13/cobra"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func command() *cobra.Command {
	root := &cobra.Command{Use: "abp", Short: "Local synthetic agent-run budget proxy", SilenceUsage: true}
	var db, out, address, experimentOut string
	var waitCollector bool
	demo := &cobra.Command{Use: "demo", Short: "Run a synthetic scenario; explicitly approves one demo deletion", RunE: func(c *cobra.Command, args []string) error {
		if waitCollector {
			ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
			defer cancel()
			if err := proxy.WaitCollector(ctx, os.Getenv("ABP_OTLP_ENDPOINT")); err != nil {
				return err
			}
		}
		r, err := proxy.DemoWithEndpoint(out, os.Getenv("ABP_OTLP_ENDPOINT"))
		if err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), r.Summary())
		fmt.Fprintln(c.OutOrStdout(), "Evidence:", out)
		return nil
	}}
	demo.Flags().StringVar(&out, "out", "data/demo", "New output directory")
	demo.Flags().BoolVar(&waitCollector, "wait-collector", false, "Wait up to ten seconds for the configured collector before demo startup")
	root.AddCommand(demo)
	inspect := &cobra.Command{Use: "inspect", Short: "Verify the ledger and print a secret-free report", RunE: func(c *cobra.Command, args []string) error {
		if _, err := os.Stat(db); err != nil {
			return err
		}
		s, err := proxy.Open(db)
		if err != nil {
			return err
		}
		defer s.Close()
		r, err := s.Report()
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(r)
	}}
	inspect.Flags().StringVar(&db, "db", "data/ledger.db", "SQLite ledger")
	root.AddCommand(inspect)
	var customer string
	billing := &cobra.Command{Use: "billing-export", Short: "Export settled synthetic usage for metered-billing-sandbox", RunE: func(c *cobra.Command, args []string) error {
		if _, err := os.Stat(db); err != nil {
			return err
		}
		s, err := proxy.Open(db)
		if err != nil {
			return err
		}
		defer s.Close()
		events, err := s.BillingEvents(customer)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(events)
	}}
	billing.Flags().StringVar(&db, "db", "data/ledger.db", "SQLite ledger")
	billing.Flags().StringVar(&customer, "customer", "local_lab", "Billing sandbox customer identifier")
	root.AddCommand(billing)
	replay := &cobra.Command{Use: "replay", Short: "Dry-run saved policy and accounting decisions without calling tools", RunE: func(c *cobra.Command, args []string) error {
		if _, err := os.Stat(db); err != nil {
			return err
		}
		s, err := proxy.Open(db)
		if err != nil {
			return err
		}
		defer s.Close()
		result, err := proxy.Replay(s)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(result)
	}}
	replay.Flags().StringVar(&db, "db", "data/ledger.db", "SQLite ledger")
	root.AddCommand(replay)
	experiment := &cobra.Command{Use: "experiment", Short: "Compare fixed synthetic incidents under three policies", RunE: func(c *cobra.Command, args []string) error {
		result, err := proxy.Experiment(experimentOut)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(result)
	}}
	experiment.Flags().StringVar(&experimentOut, "out", "reports/incident", "New output directory")
	root.AddCommand(experiment)
	serve := &cobra.Command{Use: "serve", Short: "Serve mock tools on loopback only", RunE: func(c *cobra.Command, args []string) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("listen address must be a loopback IP")
		}
		admin, reviewer := os.Getenv("ABP_ADMIN_TOKEN"), os.Getenv("ABP_REVIEW_TOKEN")
		if admin == "" && reviewer == "" {
			admin = proxy.ID()
			reviewer = proxy.ID()
			fmt.Fprintln(c.OutOrStdout(), "Ephemeral operator token:", admin)
			fmt.Fprintln(c.OutOrStdout(), "Ephemeral reviewer token:", reviewer)
		}
		s, err := proxy.Open(db)
		if err != nil {
			return err
		}
		defer s.Close()
		traceFile, err := os.OpenFile(db+".traces.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer traceFile.Close()
		provider, err := proxy.Telemetry(c.Context(), os.Getenv("ABP_OTLP_ENDPOINT"), traceFile)
		if err != nil {
			return err
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			provider.Shutdown(ctx)
		}()
		engine := proxy.NewEngine(s)
		engine.Tracer = provider.Tracer("agent-budget-proxy")
		handler, err := proxy.HTTP(engine, &proxy.MockTool{}, proxy.Credentials{Admin: admin, Reviewer: reviewer, ReviewerID: "local-reviewer"})
		if err != nil {
			return err
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		fmt.Fprintln(c.OutOrStdout(), "Listening on http://"+listener.Addr().String())
		select {
		case err = <-done:
			if err != http.ErrServerClosed {
				return err
			}
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err = server.Shutdown(shutdown); err != nil {
				server.Close()
				return err
			}
			<-done
		}
		return nil
	}}
	serve.Flags().StringVar(&db, "db", "data/ledger.db", "SQLite ledger")
	serve.Flags().StringVar(&address, "listen", "127.0.0.1:4320", "Loopback bind address")
	root.AddCommand(serve)
	root.SetArgs(os.Args[1:])
	return root
}
func main() {
	if err := command().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
}
