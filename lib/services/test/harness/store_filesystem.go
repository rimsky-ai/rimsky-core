// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// storeFilesystemImage is the locally-built production filesystem-store
// image. Built by `make service-images`.
const storeFilesystemImage = "rimsky-store-filesystem:latest"

// FilesystemStoreSpec is the in-test config for a peer filesystem-store
// container. Mirrors the YAML shape that
// `stores/filesystem/cmd/main.go` reads from
// STORE_FILESYSTEM_CONFIG, projected to the in-network endpoint a
// rimsky peer would dial.
type FilesystemStoreSpec struct {
	// PickPolicies are passed verbatim into the in-container YAML.
	PickPolicies map[string]FilesystemPickPolicy `yaml:"pick_policies"`
	// SweepIntervalSeconds defaults to 60 when zero.
	SweepIntervalSeconds int `yaml:"sweep_interval_seconds"`
	// SeedFolders are folder paths (relative to /workspace) that must
	// exist on the host bind-mount BEFORE the store starts. Required
	// for the store's startup pick-policy `root: <path>` stat check.
	// Each entry is a slice of path segments (e.g. {"docs","alpha"}).
	SeedFolders [][]string
}

// FilesystemPickPolicy mirrors `stores/filesystem/cmd/main.go::yamlPickPolicy`.
type FilesystemPickPolicy struct {
	Root                     string `yaml:"root"`
	FolderPattern            string `yaml:"folder_pattern,omitempty"`
	OnCommit                 string `yaml:"on_commit,omitempty"`
	OnGiveUp                 string `yaml:"on_give_up,omitempty"`
	VisibilityTimeoutSeconds int    `yaml:"visibility_timeout_seconds,omitempty"`
	SyncStrategy             string `yaml:"sync_strategy,omitempty"`
}

// FilesystemStoreEndpoint is the bring-up result for a peer fs-store.
type FilesystemStoreEndpoint struct {
	// InternalEndpoint is the in-network endpoint to pass to
	// harness.WithClaimProducer (e.g. "grpc://store-filesystem:9100").
	InternalEndpoint string
	// HostDir is the host-side directory mounted into the container.
	// Tests use this to seed pick-policy folders before drive.
	HostDir string
}

// StartFilesystemStore brings up the production filesystem-store image
// on the given docker network with the given alias. Returns the
// in-network endpoint and the host directory mounted at /workspace
// inside the container.
//
// Seed the directory BEFORE the container starts — the store's
// sweep_strategy=on_open discovers folders at Open time, but a fully
// stable test wants the seeded layout in place at startup.
func StartFilesystemStore(ctx context.Context, t testing.TB, networkName, alias string, spec FilesystemStoreSpec) FilesystemStoreEndpoint {
	t.Helper()

	hostDir := t.TempDir()
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatalf("harness: mkdir hostDir: %v", err)
	}
	// Seed folders before the store starts so pick-policy roots resolve.
	for _, parts := range spec.SeedFolders {
		all := append([]string{hostDir}, parts...)
		dir := filepath.Join(all...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("harness: seed folder %s: %v", dir, err)
		}
	}

	configYAML := renderFilesystemConfig(spec)

	c, err := testcontainers.Run(ctx, storeFilesystemImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"STORE_FILESYSTEM_CONFIG": "/etc/store/config.yml",
		}),
		testcontainers.WithExposedPorts("9100/tcp"),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(configYAML),
			ContainerFilePath: "/etc/store/config.yml",
			FileMode:          0o644,
		}),
		// Bind-mount the host directory at /workspace; the spec's
		// pick-policy roots resolve relative to the container's
		// configured `root: /workspace`.
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.Binds = append(hc.Binds, hostDir+":/workspace:rw,delegated")
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9100/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start store-filesystem: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	return FilesystemStoreEndpoint{
		InternalEndpoint: fmt.Sprintf("grpc://%s:9100", alias),
		HostDir:          hostDir,
	}
}

// SeedFolder creates an empty folder under the fs-store's host
// directory so pick-policy roots can auto-discover it. The path
// segments are joined under HostDir.
func (e FilesystemStoreEndpoint) SeedFolder(t testing.TB, parts ...string) {
	t.Helper()
	all := append([]string{e.HostDir}, parts...)
	dir := filepath.Join(all...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("harness: seed folder %s: %v", dir, err)
	}
}

// renderFilesystemConfig serializes the in-container YAML config the
// store binary reads. Inlines the small format rather than pulling in
// gopkg.in/yaml.v3 here.
func renderFilesystemConfig(spec FilesystemStoreSpec) string {
	var b strings.Builder
	b.WriteString("root: /workspace\n")
	b.WriteString("host: 0.0.0.0\n")
	b.WriteString("grpc_port: 9100\n")
	b.WriteString("http_port: 9110\n")
	if len(spec.PickPolicies) > 0 {
		b.WriteString("admin_port: 9120\n")
	}
	sweep := spec.SweepIntervalSeconds
	if sweep == 0 {
		sweep = 60
	}
	fmt.Fprintf(&b, "sweep_interval_seconds: %d\n", sweep)
	if len(spec.PickPolicies) > 0 {
		b.WriteString("pick_policies:\n")
		for sel, pp := range spec.PickPolicies {
			fmt.Fprintf(&b, "  %q:\n", sel)
			fmt.Fprintf(&b, "    root: %q\n", pp.Root)
			if pp.FolderPattern != "" {
				fmt.Fprintf(&b, "    folder_pattern: %q\n", pp.FolderPattern)
			}
			if pp.OnCommit != "" {
				fmt.Fprintf(&b, "    on_commit: %s\n", pp.OnCommit)
			}
			if pp.OnGiveUp != "" {
				fmt.Fprintf(&b, "    on_give_up: %s\n", pp.OnGiveUp)
			}
			if pp.VisibilityTimeoutSeconds > 0 {
				fmt.Fprintf(&b, "    visibility_timeout_seconds: %d\n", pp.VisibilityTimeoutSeconds)
			}
			if pp.SyncStrategy != "" {
				fmt.Fprintf(&b, "    sync_strategy: %q\n", pp.SyncStrategy)
			}
		}
	}
	return b.String()
}
