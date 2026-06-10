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

// sensorObjectStoreImage is the locally-built production object-store
// sensor image. Built by `make service-images`.
const sensorObjectStoreImage = "rimsky-sensor-object-store:latest"

// sensorObjectStoreBucketRoot is the in-container path the filesystem
// backend reads from. The harness wires RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT
// to this value so the sensor's `filesystem` lister advertises and
// services this root for every Subscribe whose backend is "filesystem".
// Tests drop "objects" into the bucket by writing files under
// <FsRoot>/<bucket>/ via the returned handle's PutObject helper.
const sensorObjectStoreBucketRoot = "/data/object-store"

// SensorObjectStoreHandle is the restart-capable bring-up handle for a
// rimsky-sensor-object-store peer container. Endpoint is the in-network
// gRPC endpoint (the value to pass to BringUpRimsky via WithPublisher).
// Stop terminates the live container; Restart brings up a fresh one
// with IDENTICAL env so the test can exercise the restart-recovery
// path: durable subscriptions + watermark cursors survive in the
// configured RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN, but the in-memory
// watches AND any objects that lived only in the container's
// filesystem are dropped on terminate and rebuilt by the fresh
// container's AttachStateDB.
//
// FsRoot exposes the in-container bucket root the harness wired into
// RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT, so the test can name the bucket
// directory explicitly in its Subscribe config without re-deriving the
// path. PutObject writes a file under that root inside the LIVE
// container — the load-bearing externally-mutable surface the cross-
// stack proof drives.
type SensorObjectStoreHandle struct {
	Endpoint string
	FsRoot   string

	t           testing.TB
	networkName string
	alias       string
	stateDSN    string

	container testcontainers.Container
}

// StartSensorObjectStoreHandle brings up the rimsky-sensor-object-store
// image on the given docker network with the given alias and a durable
// state DSN, returning a restart-capable handle. Pass stateDSN="" for
// the in-memory default (no durability proof possible — the watermark
// vanishes with the process).
//
// rimsky's stable in-network alias `http://rimsky:8080` is the message
// endpoint; the sensor's gRPC Publisher server listens on port 9083
// (matching the binary's default + the Dockerfile EXPOSE).
//
// The filesystem backend is auto-registered in the sensor binary when
// RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT is non-empty in the container env;
// this helper always sets it (to `sensorObjectStoreBucketRoot`) so a
// Subscribe naming `backend: "filesystem"` is serviceable end-to-end.
// The memory backend stays registered too, so existing in-memory
// scenarios are not disturbed.
//
// Cleanup is registered via t.Cleanup; fails hard (t.Fatal) when the
// image is missing — the harness never t.Skip's.
func StartSensorObjectStoreHandle(ctx context.Context, t testing.TB, networkName, alias, stateDSN string) *SensorObjectStoreHandle {
	t.Helper()
	h := &SensorObjectStoreHandle{
		t:           t,
		networkName: networkName,
		alias:       alias,
		stateDSN:    stateDSN,
		FsRoot:      sensorObjectStoreBucketRoot,
	}
	h.container = runSensorObjectStoreContainer(ctx, t, networkName, alias, stateDSN)
	h.Endpoint = fmt.Sprintf("%s:9083", alias)
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

// Stop terminates the live sensor-object-store container without
// bringing a fresh one up. Used by restart-recovery tests to drop the
// in-memory watches AND the in-container bucket contents between
// Subscribe (persisted via state DSN) and the recovered post-restart
// poll. The sibling Postgres holding the state DSN is NOT torn down
// by this call — durable rows survive across Stop+Restart.
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

// Restart brings up a fresh sensor-object-store container with
// IDENTICAL network / alias / state DSN / FsRoot, replacing any live
// container. The fresh container's AttachStateDB rebuilds in-memory
// watches from durable rows — recovering each subscription's
// ORIGINALLY-resolved bucket, prefix, watermark_field, and watermark
// CURSOR (watermark_name or watermark_time) so the first post-restart
// poll skips objects already emitted before the restart. The bucket
// directory inside the fresh container starts EMPTY (the previous
// container's filesystem is gone with the terminate); the test re-
// drops the prior objects post-restart to verify the recovered
// watermark suppresses re-emit.
func (h *SensorObjectStoreHandle) Restart(ctx context.Context) {
	h.t.Helper()
	h.Stop(ctx)
	h.container = runSensorObjectStoreContainer(ctx, h.t, h.networkName, h.alias, h.stateDSN)
}

// PutObject writes a file under <FsRoot>/<bucket>/<objectName> inside
// the LIVE sensor container, via the Docker copy-to-container API.
// Intermediate directories are created as needed by the Docker
// daemon's tar extract. Returns once the file has been written;
// callers do their own wait-for-emit polling against rimsky.
//
// Object content is verbatim — the sensor's filesystem lister
// computes ETag from the file bytes via FNV-64a, so two PutObject
// calls with the same bytes produce the same ETag (and therefore the
// same rimsky-side idempotency key), while different bytes produce a
// distinct ETag (and a fresh emit).
//
// The sensor container runs as distroless `nonroot`; the Docker
// daemon writes files as root with the requested mode. 0644 makes
// them world-readable so the sensor process can read them.
//
// Fails hard (t.Fatal) on any IO error — the harness never returns
// a soft error from a write the test relied on.
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

// runSensorObjectStoreContainer starts one rimsky-sensor-object-store
// container with the given env wiring. Shared by
// StartSensorObjectStoreHandle and SensorObjectStoreHandle.Restart so
// the bring-up shape is identical on initial boot and after a
// restart.
func runSensorObjectStoreContainer(ctx context.Context, t testing.TB, networkName, alias, stateDSN string) testcontainers.Container {
	t.Helper()
	env := map[string]string{
		"RIMSKY_SENSOR_OBJECT_STORE_PORT": "9083",
		// rimsky's stable in-network alias; the sensor POSTs message
		// envelopes here.
		"RIMSKY_ENDPOINT": "http://rimsky:8080",
		// Auto-register the filesystem backend in the sensor binary.
		// The default image otherwise registers only "memory". With
		// this env set, Capabilities advertises and Subscribe accepts
		// "filesystem", and the lister reads objects out of the
		// container-local directory tree under this root.
		"RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT": sensorObjectStoreBucketRoot,
	}
	if stateDSN != "" {
		// Durability gate. When set, sensor-object-store persists
		// active publisher-subscriptions + their watermark cursors
		// (watermark_name | watermark_time) to the configured Postgres
		// so a process restart resumes the cursor instead of treating
		// every already-listed object as new. Empty → in-memory
		// default (loses watches AND cursors on restart; the
		// STORY-sensor-object-store durability acceptance is not
		// observable).
		env["RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN"] = stateDSN
	}
	c, err := testcontainers.Run(ctx, sensorObjectStoreImage,
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
