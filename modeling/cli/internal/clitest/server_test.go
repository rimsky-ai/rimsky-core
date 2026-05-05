// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package clitest_test

import (
	"context"
	"testing"
	"time"

	"github.com/fallguy/rimsky/modeling/cli"
	"github.com/fallguy/rimsky/modeling/cli/internal/clitest"
	"github.com/fallguy/rimsky/modeling/node"
)

func minimalSpecTyped() node.TemplateSpec {
	return node.TemplateSpec{
		Name:            "x",
		Version:         "1.0",
		FrameResolution: "coalesce",
		Nodes: []node.TemplateNodeDef{
			{Type: "a", Executor: "http-node"},
		},
	}
}

func TestServer_RegisterAndDeploy(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)

	tpl, err := c.RegisterTemplate(context.Background(), cli.RegisterTemplateRequest{
		Spec: minimalSpecTyped(),
		Tag:  "ingest@1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Hash() == "" {
		t.Fatal("hash empty")
	}
	if _, err := c.DeployTemplate(context.Background(), tpl.Hash()); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetTemplate(context.Background(), tpl.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "deployed" {
		t.Errorf("state %q", got.State)
	}
}

func TestServer_DeleteDeployedRefused(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	tpl, _ := c.RegisterTemplate(context.Background(), cli.RegisterTemplateRequest{Spec: minimalSpecTyped()})
	_, _ = c.DeployTemplate(context.Background(), tpl.Hash())
	err := c.DeleteTemplate(context.Background(), tpl.Hash())
	if !cli.IsConflict(err) {
		t.Errorf("want 409, got %v", err)
	}
}

func TestServer_InstanceLifecycle(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	tpl, _ := c.RegisterTemplate(context.Background(), cli.RegisterTemplateRequest{Spec: minimalSpecTyped(), Tag: "t1"})
	_, _ = c.DeployTemplate(context.Background(), "t1")
	key := "compose:p:n"
	inst, err := c.CreateInstance(context.Background(), cli.CreateInstanceRequest{Template: "t1", InstanceKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	if inst.UUID() == "" {
		t.Fatal("uuid empty")
	}
	// Deleting non-terminal instance fails 409.
	if err := c.DeleteInstance(context.Background(), inst.UUID()); !cli.IsConflict(err) {
		t.Errorf("want 409, got %v", err)
	}
	now := time.Now()
	srv.State.SetInstanceTerminated(inst.UUID(), &now)
	if err := c.DeleteInstance(context.Background(), inst.UUID()); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-create on same key returns existing if not deleted;
	// since we just deleted, recreating works.
	_, err = c.CreateInstance(context.Background(), cli.CreateInstanceRequest{Template: "t1", InstanceKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	_ = tpl
}

func TestServer_FailureInjection(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	srv.SetFailure("GET", "/health", clitest.FailureSpec{Status: 500, Body: map[string]any{"error": "boom"}})
	c := cli.NewClient(srv.URL)
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("want error")
	}
	// Second call recovers.
	if _, err := c.Health(context.Background()); err != nil {
		t.Errorf("second call should pass: %v", err)
	}
}

func TestServer_TagCRUD(t *testing.T) {
	srv := clitest.NewServer(t)
	defer srv.Close()
	c := cli.NewClient(srv.URL)
	tpl, _ := c.RegisterTemplate(context.Background(), cli.RegisterTemplateRequest{Spec: minimalSpecTyped()})
	_, err := c.CreateTag(context.Background(), cli.CreateTagRequest{Tag: "v1", Template: tpl.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	// Hash-shape tag is rejected.
	_, err = c.CreateTag(context.Background(), cli.CreateTagRequest{Tag: "sha256-" + makeHex64(), Template: tpl.Hash()})
	if !cli.IsBadRequest(err) {
		t.Errorf("want 400, got %v", err)
	}
}

func makeHex64() string {
	const h = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = h[i%len(h)]
	}
	return string(out)
}
