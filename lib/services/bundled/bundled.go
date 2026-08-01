// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service
package bundled

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	fsserver "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/server"
	pgserver "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/server"
	claudeagent "github.com/rimsky-ai/rimsky-core/lib/services/executors/claude-agent"
	httpnode "github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node"
	verifierhttp "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http"
	verifiershapechecks "github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks"
)

const ProducerNameFilesystem = "claim-producer-filesystem"

const ProducerNamePostgres = "claim-producer-postgres"

type ExecutorHandler interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error)
}

type ExecutorRegistry interface {
	RegisterExecutor(inprocURL string, h ExecutorHandler) error
}

type ExecutorAliasSink interface {
	RegisterExecutorAlias(name, inprocURL string) error
}

type ClaimProducerRegistry interface {
	RegisterClaimProducer(name string, handler claimproducer.ClaimProducer, caps claimproducer.Capabilities) error
}

type DiscoverySink interface {
	AdvertiseExecutor(name string, schema []byte, tags, errorClasses []string)
	AdvertiseClaimProducer(name string, caps claimproducer.Capabilities)
}

type Opts struct {
	Logger *slog.Logger
}

// @decision: handler-package-in-service-directory
func RegisterAll(ctx context.Context, execReg ExecutorRegistry, cpReg ClaimProducerRegistry, aliases ExecutorAliasSink, discovery DiscoverySink, opts Opts) error {
	if execReg == nil {
		return errors.New("bundled.RegisterAll: executor registry required")
	}
	if cpReg == nil {
		return errors.New("bundled.RegisterAll: claim-producer registry required")
	}
	if aliases == nil {
		return errors.New("bundled.RegisterAll: alias sink required")
	}
	if discovery == nil {
		return errors.New("bundled.RegisterAll: discovery sink required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err := registerExecutors(execReg, aliases, discovery, logger); err != nil {
		return err
	}
	return registerClaimProducers(ctx, cpReg, discovery, logger)
}

type executorEntry struct {
	name         string
	inprocURL    string
	construct    func(logger *slog.Logger) (ExecutorHandler, error)
	schema       []byte
	tags         []string
	errorClasses []string
}

func executorEntries() []executorEntry {
	return []executorEntry{
		{
			name:      claudeagent.ExecutorName,
			inprocURL: claudeagent.InProcURL,
			construct: func(logger *slog.Logger) (ExecutorHandler, error) {
				o, err := claudeagent.LoadOptsFromEnv()
				if err != nil {
					return nil, err
				}
				if !o.CredentialsConfigured() {
					return nil, fmt.Errorf("%w: %v", errExecutorSkipped, claudeagent.ErrCredentialsMissing)
				}
				return claudeagent.NewExecutorServer(claudeagent.ServerConfig{Opts: o, Logger: logger}), nil
			},
			schema:       claudeagent.SchemaBytes(),
			tags:         claudeagent.DeclaredTags(),
			errorClasses: claudeagent.DeclaredErrorClasses(),
		},
		{
			name:      httpnode.ExecutorName,
			inprocURL: httpnode.InProcURL,
			construct: func(*slog.Logger) (ExecutorHandler, error) {
				o, err := httpnode.LoadOptsFromEnv()
				if err != nil {
					return nil, err
				}
				return httpnode.NewServer(o), nil
			},
			schema:       httpnode.SchemaBytes(),
			tags:         httpnode.DeclaredTags(),
			errorClasses: httpnode.DeclaredErrorClasses(),
		},
		{
			name:      verifierhttp.ExecutorName,
			inprocURL: verifierhttp.InProcURL,
			construct: func(*slog.Logger) (ExecutorHandler, error) {
				o, err := verifierhttp.LoadOptsFromEnv()
				if err != nil {
					return nil, err
				}
				return verifierhttp.NewServer(o.StubMode), nil
			},
			schema:       verifierhttp.SchemaBytes(),
			tags:         verifierhttp.DeclaredTags(),
			errorClasses: verifierhttp.DeclaredErrorClasses(),
		},
		{
			name:      verifiershapechecks.ExecutorName,
			inprocURL: verifiershapechecks.InProcURL,
			construct: func(*slog.Logger) (ExecutorHandler, error) {
				o, err := verifiershapechecks.LoadOptsFromEnv()
				if err != nil {
					return nil, err
				}
				return verifiershapechecks.NewServer(o.StubMode), nil
			},
			schema:       verifiershapechecks.SchemaBytes(),
			tags:         verifiershapechecks.DeclaredTags(),
			errorClasses: verifiershapechecks.DeclaredErrorClasses(),
		},
	}
}

var errExecutorSkipped = errors.New("executor not configured")

// @decision: per-service-load-opts-from-env
func registerExecutors(execReg ExecutorRegistry, aliases ExecutorAliasSink, discovery DiscoverySink, logger *slog.Logger) error {
	for _, e := range executorEntries() {
		h, err := e.construct(logger)
		if errors.Is(err, errExecutorSkipped) {
			logger.Info("bundled executor skipped: not configured", "executor", e.name, "reason", err.Error())
			continue
		}
		if err != nil {
			return fmt.Errorf("bundled.RegisterAll: construct executor %q: %w", e.name, err)
		}
		if err := execReg.RegisterExecutor(e.inprocURL, h); err != nil {
			return fmt.Errorf("bundled.RegisterAll: register executor %q: %w", e.name, err)
		}
		if err := aliases.RegisterExecutorAlias(e.name, e.inprocURL); err != nil {
			return fmt.Errorf("bundled.RegisterAll: register executor alias %q: %w", e.name, err)
		}
		discovery.AdvertiseExecutor(e.name, e.schema, e.tags, e.errorClasses)
		logger.Info("bundled executor registered in-process", "executor", e.name, "inproc_url", e.inprocURL)
	}
	return nil
}

func registerClaimProducers(ctx context.Context, cpReg ClaimProducerRegistry, discovery DiscoverySink, logger *slog.Logger) error {
	fsOpts, err := fsserver.LoadOptsFromEnv()
	if err != nil {
		return fmt.Errorf("bundled.RegisterAll: construct claim producer %q: %w", ProducerNameFilesystem, err)
	}
	if !fsOpts.Configured {
		logger.Info("bundled claim producer skipped: not configured", "producer", ProducerNameFilesystem, "config_env", fsserver.ConfigEnv)
	} else {
		for selector, pp := range fsOpts.PickPolicies {
			if pp.SyncStrategy == "explicit" {
				return fmt.Errorf("bundled.RegisterAll: claim producer %q: pick_policies[%q].sync_strategy is %q, "+
					"which requires the admin HTTP sync route; the bundled in-process deployment never binds an admin listener "+
					"— run %s as its own service (with admin_port bound) instead of bundling it, or switch sync_strategy to on_open|on_drain|never",
					ProducerNameFilesystem, selector, "explicit", ProducerNameFilesystem)
			}
		}
		srv, err := fsserver.New(fsOpts.ServerConfig())
		if err != nil {
			return fmt.Errorf("bundled.RegisterAll: construct claim producer %q: %w", ProducerNameFilesystem, err)
		}
		go srv.RunSweep(ctx, fsOpts.SweepInterval)
		if err := registerClaimProducer(ctx, cpReg, discovery, logger, ProducerNameFilesystem, srv); err != nil {
			return err
		}
	}

	pgOpts, err := pgserver.LoadOptsFromEnv()
	if err != nil {
		return fmt.Errorf("bundled.RegisterAll: construct claim producer %q: %w", ProducerNamePostgres, err)
	}
	if !pgOpts.Configured {
		logger.Info("bundled claim producer skipped: not configured", "producer", ProducerNamePostgres, "config_env", pgserver.ConfigEnv)
		return nil
	}
	srv, err := pgserver.New(ctx, pgOpts.ServerConfig())
	if err != nil {
		return fmt.Errorf("bundled.RegisterAll: construct claim producer %q: %w", ProducerNamePostgres, err)
	}
	go srv.RunSweep(ctx, pgOpts.SweepInterval)
	return registerClaimProducer(ctx, cpReg, discovery, logger, ProducerNamePostgres, srv)
}

func registerClaimProducer(ctx context.Context, cpReg ClaimProducerRegistry, discovery DiscoverySink, logger *slog.Logger, name string, srv genv1.ClaimProducerServer) error {
	producer, err := serverkit.NewServerBackedClaimProducer(ctx, name, srv)
	if err != nil {
		return fmt.Errorf("bundled.RegisterAll: construct claim producer %q: %w", name, err)
	}
	caps, err := producer.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("bundled.RegisterAll: claim producer %q capabilities: %w", name, err)
	}
	if err := cpReg.RegisterClaimProducer(name, producer, caps); err != nil {
		return fmt.Errorf("bundled.RegisterAll: register claim producer %q: %w", name, err)
	}
	discovery.AdvertiseClaimProducer(name, caps)
	logger.Info("bundled claim producer registered in-process", "producer", name)
	return nil
}
