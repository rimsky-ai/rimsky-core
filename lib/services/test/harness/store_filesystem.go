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

const storeFilesystemImage = "rimsky-store-filesystem:latest"

type FilesystemStoreSpec struct {
	PickPolicies map[string]FilesystemPickPolicy `yaml:"pick_policies"`
	SweepIntervalSeconds int `yaml:"sweep_interval_seconds"`
	SeedFolders [][]string
	AdvertiseHTTPBridge bool
}

type FilesystemPickPolicy struct {
	Root                     string `yaml:"root"`
	FolderPattern            string `yaml:"folder_pattern,omitempty"`
	OnCommit                 string `yaml:"on_commit,omitempty"`
	OnGiveUp                 string `yaml:"on_give_up,omitempty"`
	VisibilityTimeoutSeconds int    `yaml:"visibility_timeout_seconds,omitempty"`
	SyncStrategy             string `yaml:"sync_strategy,omitempty"`
}

type FilesystemStoreEndpoint struct {
	InternalEndpoint string
	HostDir string
	HostHTTPBridge string
}

func StartFilesystemStore(ctx context.Context, t testing.TB, networkName, alias string, spec FilesystemStoreSpec) FilesystemStoreEndpoint {
	t.Helper()

	hostDir := t.TempDir()
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatalf("harness: mkdir hostDir: %v", err)
	}
	for _, parts := range spec.SeedFolders {
		all := append([]string{hostDir}, parts...)
		dir := filepath.Join(all...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("harness: seed folder %s: %v", dir, err)
		}
	}

	httpBridgeURL := ""
	if spec.AdvertiseHTTPBridge {
		httpBridgeURL = fmt.Sprintf("http://%s:9110", alias)
	}
	configYAML := renderFilesystemConfig(spec, httpBridgeURL)

	exposedPorts := []string{"9100/tcp"}
	if spec.AdvertiseHTTPBridge {
		exposedPorts = append(exposedPorts, "9110/tcp")
	}

	c, err := testcontainers.Run(ctx, storeFilesystemImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"STORE_FILESYSTEM_CONFIG": "/etc/store/config.yml",
		}),
		testcontainers.WithExposedPorts(exposedPorts...),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(configYAML),
			ContainerFilePath: "/etc/store/config.yml",
			FileMode:          0o644,
		}),
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

	hostHTTPBridge := ""
	if spec.AdvertiseHTTPBridge {
		hostIP, err := c.Host(ctx)
		if err != nil {
			t.Fatalf("harness: store-filesystem host: %v", err)
		}
		mapped, err := c.MappedPort(ctx, "9110")
		if err != nil {
			t.Fatalf("harness: store-filesystem mapped http port: %v", err)
		}
		hostHTTPBridge = fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())
	}

	return FilesystemStoreEndpoint{
		InternalEndpoint: fmt.Sprintf("grpc://%s:9100", alias),
		HostDir:          hostDir,
		HostHTTPBridge:   hostHTTPBridge,
	}
}

func (e FilesystemStoreEndpoint) SeedFolder(t testing.TB, parts ...string) {
	t.Helper()
	all := append([]string{e.HostDir}, parts...)
	dir := filepath.Join(all...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("harness: seed folder %s: %v", dir, err)
	}
}

func renderFilesystemConfig(spec FilesystemStoreSpec, httpBridgeURL string) string {
	var b strings.Builder
	b.WriteString("root: /workspace\n")
	b.WriteString("host: 0.0.0.0\n")
	b.WriteString("grpc_port: 9100\n")
	b.WriteString("http_port: 9110\n")
	if httpBridgeURL != "" {
		fmt.Fprintf(&b, "http_bridge_url: %q\n", httpBridgeURL)
	}
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
