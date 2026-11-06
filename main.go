package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-gost/llm-gw/internal/admin"
	"github.com/go-gost/llm-gw/internal/bootstrap"
	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/gateway"
	"github.com/go-gost/llm-gw/internal/logging"
	"github.com/go-gost/llm-gw/internal/stats"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "llm-gw:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "hash-password" {
		return runHashPassword()
	}

	configPath, err := parseServeArgs(args)
	if err != nil {
		return err
	}

	res, err := bootstrap.Prepare(bootstrap.Options{
		ExplicitPath: configPath,
		Getenv:       os.Getenv,
		Stderr:       os.Stderr,
	})
	if err != nil {
		return err
	}
	cfg := res.Config

	log, err := logging.New(cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return err
	}
	log.SetDefault()

	for _, warning := range res.Warnings {
		log.Warnf("%s", warning)
	}
	if res.Generated {
		if res.GeneratedPassword != "" {
			log.Infof("generated config %s; admin password printed to stderr", res.ConfigPath)
		} else {
			log.Infof("generated config %s with the password from LLM_GW_ADMIN_PASSWORD", res.ConfigPath)
		}
	}
	if len(cfg.Entrypoints) == 0 {
		if cfg.Admin.PasswordHash == "" {
			log.Warnf("no entrypoints configured; admin console is disabled (admin.passwordHash is empty)")
		} else {
			log.Warnf("no entrypoints configured; use the admin console to add a provider, node, route and entrypoint")
		}
	}

	gw, err := gateway.New(cfg, log)
	if err != nil {
		return err
	}
	defer gw.Close()

	if err := resetAdminSessions(gw.Stats(), res.Generated); err != nil {
		return fmt.Errorf("reset admin sessions: %w", err)
	}
	if err := gw.Start(); err != nil {
		return err
	}

	var adminSrv *http.Server
	var adm *admin.Server
	if cfg.Admin.PasswordHash != "" {
		ln, err := net.Listen("tcp", cfg.Admin.Listen)
		if err != nil {
			return err
		}
		adm = admin.New(res.ConfigPath, cfg, gw, log)
		adminSrv = &http.Server{Handler: adm.Handler()}
		probeCtx, probeCancel := context.WithCancel(context.Background())
		defer probeCancel()
		adm.StartProbes(probeCtx)
		go func() {
			if err := adminSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Errorf("admin server: %v", err)
			}
		}()
		log.Infof("admin API listening on %s", ln.Addr())
	} else {
		log.Warnf("admin.passwordHash is empty; management API disabled")
	}
	defer func() {
		if adminSrv != nil {
			adminSrv.Close()
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	for s := range sig {
		if s != syscall.SIGHUP {
			log.Infof("signal %v, shutting down", s)
			return nil
		}

		newCfg, err := config.Load(res.ConfigPath)
		if err != nil {
			log.Errorf("reload: %v", err)
			continue
		}
		if err := gw.Reload(newCfg); err != nil {
			log.Errorf("reload: %v", err)
			continue
		}
		if adm != nil {
			adm.SetConfig(newCfg)
		}
		logging.Apply(log, newCfg.Log.Level, newCfg.Log.Format)
		log.Infof("config reloaded")
	}
	return nil
}

func runHashPassword() error {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	password := strings.TrimRight(string(b), "\r\n")
	if password == "" {
		return errors.New("empty password")
	}
	hash, err := config.HashPassword(password)
	if err != nil {
		return err
	}
	fmt.Println(hash)
	return nil
}

func parseServeArgs(args []string) (string, error) {
	if len(args) == 0 || args[0] != "serve" {
		return "", errors.New("usage: llm-gw serve [-c <config>]")
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var path string
	const usage = "config file (default: $LLM_GW_DATA/llm-gw.yaml)"
	fs.StringVar(&path, "c", "", usage)
	fs.StringVar(&path, "config", "", usage)
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	return path, nil
}

// resetAdminSessions clears persisted remember-me sessions when a first-run
// config generated a new admin credential.
func resetAdminSessions(store *stats.Store, generated bool) error {
	if !generated || store == nil {
		return nil
	}
	return store.DeleteAllSessions(context.Background())
}
