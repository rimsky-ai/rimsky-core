// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service
// @decision: bundled-registry-entrypoint
package bundledwire

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	rtclaimproducer "github.com/rimsky-ai/rimsky-core/lib/runtime/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/services/bundled"
)

func CollectBundled(ctx context.Context, logger *slog.Logger) (*config.BundledRegistrations, error) {
	regs := &config.BundledRegistrations{
		ExecutorHandlers:      map[string]executor.InProcessHandler{},
		ExecutorAliases:       map[string]executor.Endpoint{},
		ExecutorAdverts:       map[string]config.BundledExecutorAdvertisement{},
		ClaimProducerAdverts:  map[string]protocol.Capabilities{},
		ClaimProducerRegistry: rtclaimproducer.NewInProcessRegistry(),
	}
	col := &bundledCollector{regs: regs}
	if err := bundled.RegisterAll(ctx, col, col, col, col, bundled.Opts{Logger: logger}); err != nil {
		return nil, err
	}
	return regs, nil
}

type bundledCollector struct {
	regs *config.BundledRegistrations
}

type inprocExecutorAdapter struct {
	h bundled.ExecutorHandler
}

func (a inprocExecutorAdapter) Execute(ctx context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error) {
	return a.h.Execute(ctx, req)
}

func (c *bundledCollector) RegisterExecutor(inprocURL string, h bundled.ExecutorHandler) error {
	if _, exists := c.regs.ExecutorHandlers[inprocURL]; exists {
		return fmt.Errorf("duplicate in-proc handler registration for %q", inprocURL)
	}
	c.regs.ExecutorHandlers[inprocURL] = inprocExecutorAdapter{h: h}
	return nil
}

func (c *bundledCollector) RegisterExecutorAlias(name, inprocURL string) error {
	if _, exists := c.regs.ExecutorAliases[name]; exists {
		return fmt.Errorf("duplicate executor alias registration for %q", name)
	}
	c.regs.ExecutorAliases[name] = executor.Endpoint{Transport: "inproc", URL: inprocURL}
	return nil
}

func (c *bundledCollector) RegisterClaimProducer(name string, handler protocol.ClaimProducer, caps protocol.Capabilities) error {
	return c.regs.ClaimProducerRegistry.Register(name, rtclaimproducer.Registration{
		Handler:      handler,
		Capabilities: caps,
	})
}

func (c *bundledCollector) AdvertiseExecutor(name string, schema []byte, tags, errorClasses []string) {
	c.regs.ExecutorAdverts[name] = config.BundledExecutorAdvertisement{
		Schema:       schema,
		Tags:         tags,
		ErrorClasses: errorClasses,
	}
}

func (c *bundledCollector) AdvertiseClaimProducer(name string, caps protocol.Capabilities) {
	c.regs.ClaimProducerAdverts[name] = caps
}
