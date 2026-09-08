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
	var db, out, address string
	demo := &cobra.Command{Use: "demo", Short: "Run a synthetic scenario; explicitly approves one demo deletion", RunE: func(c *cobra.Command, args []string) error {
		r, err := proxy.Demo(out)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), r.Summary())
		fmt.Fprintln(c.OutOrStdout(), "Evidence:", out)
		return nil
	}}
	demo.Flags().StringVar(&out, "out", "data/demo", "New output directory")
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
		handler, err := proxy.HTTP(proxy.NewEngine(s), &proxy.MockTool{}, proxy.Credentials{Admin: admin, Reviewer: reviewer, ReviewerID: "local-reviewer"})
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
