// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const sensorObjectStoreImage = "rimsky-sensor-object-store"

const sensorObjectStoreBucketRoot = "/data/object-store"

type SensorObjectStoreHandle struct {
	Endpoint string
	FsRoot   string

	t              testing.TB
	networkName    string
	alias          string
	rimskyEndpoint string
	stateDSN       string

	container testcontainers.Container
}

func StartSensorObjectStoreHandle(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string) *SensorObjectStoreHandle {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	h := &SensorObjectStoreHandle{
		t:              t,
		networkName:    networkName,
		alias:          uniqueAlias,
		rimskyEndpoint: rimskyEndpoint,
		stateDSN:       stateDSN,
		FsRoot:         sensorObjectStoreBucketRoot,
	}
	h.container = runSensorObjectStoreContainer(ctx, t, networkName, uniqueAlias, rimskyEndpoint, stateDSN)
	h.Endpoint = fmt.Sprintf("%s:9083", uniqueAlias)
	t.Cleanup(func() {
		if h.container == nil {
			return
		}
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.container.Terminate(termCtx)
	})
	return h
}

func (h *SensorObjectStoreHandle) Stop(ctx context.Context) {
	h.t.Helper()
	if h.container == nil {
		return
	}
	termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = h.container.Terminate(termCtx)
	h.container = nil
}

func (h *SensorObjectStoreHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container = runSensorObjectStoreContainer(ctx, h.t, h.networkName, h.alias, h.rimskyEndpoint, h.stateDSN)
}

func (h *SensorObjectStoreHandle) PutObject(ctx context.Context, bucket, objectName string, content []byte) {
	h.t.Helper()
	if h.container == nil {
		h.t.Fatalf("sensor-object-store: PutObject called against a stopped container")
	}
	containerPath := fmt.Sprintf("%s/%s/%s", h.FsRoot, bucket, objectName)
	if err := h.container.CopyToContainer(ctx, content, containerPath, 0o644); err != nil {
		h.t.Fatalf("sensor-object-store: copy %q into container: %v", containerPath, err)
	}
}

func runSensorObjectStoreContainer(ctx context.Context, t testing.TB, networkName, alias, rimskyEndpoint, stateDSN string) testcontainers.Container {
	t.Helper()
	if rimskyEndpoint == "" {
		rimskyEndpoint = "http://rimsky:8080"
	}
	env := map[string]string{
		"RIMSKY_SENSOR_OBJECT_STORE_PORT":    "9083",
		"RIMSKY_CONTROL_API_URL":             rimskyEndpoint,
		"RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT": sensorObjectStoreBucketRoot,
	}
	if stateDSN != "" {
		// @story: sensor-object-store
		env["RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN"] = stateDSN
	}
	c, err := runWithRetry(ctx, ImageRef(sensorObjectStoreImage),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9083/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9083/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start sensor-object-store: %v", err)
	}
	return c
}
