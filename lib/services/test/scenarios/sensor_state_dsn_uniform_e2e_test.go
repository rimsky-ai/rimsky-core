// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
// @story: sensor-cron
// @story: sensor-http
// @story: sensor-object-store
// @story: sensor-webhook
package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

type sensorStateOwner struct {
	sensor      string
	image       string
	dsnVar      string
	tablePrefix string
	stateTables []string
}

var sensorStateOwners = []sensorStateOwner{
	{
		sensor: "sensor-cron", image: "rimsky-sensor-cron",
		dsnVar: "RIMSKY_SENSOR_CRON_STATE_DSN", tablePrefix: "sensor_cron",
		stateTables: []string{"sensor_cron_state"},
	},
	{
		sensor: "sensor-http", image: "rimsky-sensor-http",
		dsnVar: "RIMSKY_SENSOR_HTTP_STATE_DSN", tablePrefix: "sensor_http",
		stateTables: []string{"sensor_http_state"},
	},
	{
		sensor: "sensor-object-store", image: "rimsky-sensor-object-store",
		dsnVar: "RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", tablePrefix: "sensor_object_store",
		stateTables: []string{"sensor_object_store_seen_names", "sensor_object_store_state"},
	},
	{
		sensor: "sensor-webhook", image: "rimsky-sensor-webhook",
		dsnVar: "RIMSKY_SENSOR_WEBHOOK_STATE_DSN", tablePrefix: "sensor_webhook",
		stateTables: []string{"sensor_webhook_state"},
	},
}

const unusedControlAPIForStateOnlySensor = "http://127.0.0.1:8080"

func TestEverySensorTakesOneStateDSNShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.SharedNetworkName(ctx, t)

	requireEveryBundledSensorIsInTheTable(t)
	requireNoSensorPrefixCoversAnother(t)

	t.Run("one_database_behind_all_four_sensors_collides_on_nothing", func(t *testing.T) {
		statePG := startSensorStatePostgres(ctx, t, netName, "sensor-dsn-pg")
		startEverySensorOnOneStateDSN(ctx, t, netName, statePG.internalDSN)

		pool := connectSensorStatePostgres(ctx, t, statePG.hostDSN)
		defer pool.Close()
		found := sensorStateTables(ctx, t, pool)

		want := []string{}
		for _, owner := range sensorStateOwners {
			want = append(want, owner.stateTables...)
		}
		slices.Sort(want)
		if !slices.Equal(found, want) {
			t.Fatalf("state tables in the one shared database: got %v, want %v "+
				"(each sensor bootstraps its own tables against the same connection string)", found, want)
		}
	})

	t.Run("every_sensor_refuses_to_run_when_its_state_database_is_unreachable", func(t *testing.T) {
		for _, owner := range sensorStateOwners {
			dsn := unreachableStateDSN(owner.sensor)
			exited := harness.RunImageToExit(ctx, t, "dsn-unreachable-"+owner.sensor, owner.image, netName,
				map[string]string{
					owner.dsnVar:             dsn,
					"RIMSKY_CONTROL_API_URL": unusedControlAPIForStateOnlySensor,
				})
			if exited.ExitCode == 0 {
				t.Errorf("%s exited 0 on an unreachable %s; a sensor that cannot reach its state "+
					"database must refuse to run rather than run stateless", owner.sensor, owner.dsnVar)
			}
			if !strings.Contains(exited.Logs, "state db") {
				t.Errorf("%s exited %d without naming its state database; logs:\n%s",
					owner.sensor, exited.ExitCode, exited.Logs)
			}
			if !strings.Contains(exited.Logs, unreachableStateHost(owner.sensor)) {
				t.Errorf("%s exited %d without naming the host from its own %s, so nothing shows it "+
					"read that variable; logs:\n%s", owner.sensor, exited.ExitCode, owner.dsnVar, exited.Logs)
			}
		}
	})
}

func requireEveryBundledSensorIsInTheTable(t *testing.T) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "lib", "services", "sensors")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the bundled sensor directory %s: %v", dir, err)
	}
	shipped := []string{}
	for _, e := range entries {
		if e.IsDir() {
			shipped = append(shipped, e.Name())
		}
	}
	declared := []string{}
	for _, owner := range sensorStateOwners {
		declared = append(declared, owner.sensor)
	}
	slices.Sort(shipped)
	slices.Sort(declared)
	if !slices.Equal(shipped, declared) {
		t.Fatalf("the bundled sensors under %s are %v, and this test drives %v. Nothing here holds a sensor "+
			"this table does not name to the one state-DSN shape", dir, shipped, declared)
	}
}

func requireNoSensorPrefixCoversAnother(t *testing.T) {
	t.Helper()
	for _, owner := range sensorStateOwners {
		for _, other := range sensorStateOwners {
			if owner.sensor == other.sensor {
				continue
			}
			if strings.HasPrefix(other.tablePrefix, owner.tablePrefix) {
				t.Fatalf("%s's table prefix %q covers %s's %q, so one table could belong to both and set "+
					"equality would no longer say which sensor owns what",
					owner.sensor, owner.tablePrefix, other.sensor, other.tablePrefix)
			}
		}
	}
}

func startEverySensorOnOneStateDSN(ctx context.Context, t *testing.T, netName, stateDSN string) {
	t.Helper()
	harness.StartSensorCron(ctx, t, netName, "dsn-sensor-cron", unusedControlAPIForStateOnlySensor, stateDSN)
	harness.StartSensorHTTPHandle(ctx, t, netName, "dsn-sensor-http", unusedControlAPIForStateOnlySensor, stateDSN)
	harness.StartSensorObjectStoreHandle(ctx, t, netName, "dsn-sensor-object-store",
		unusedControlAPIForStateOnlySensor, stateDSN)
	harness.StartSensorWebhookWithState(ctx, t, netName, "dsn-sensor-webhook",
		unusedControlAPIForStateOnlySensor, stateDSN)
}

func unreachableStateDSN(sensor string) string {
	return fmt.Sprintf("postgres://test:test@%s:5432/rimsky_test?sslmode=disable", unreachableStateHost(sensor))
}

func unreachableStateHost(sensor string) string {
	return "no-such-host-for-" + sensor
}

func sensorStateTables(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename LIKE 'sensor\_%' ORDER BY tablename`)
	if err != nil {
		t.Fatalf("list sensor state tables: %v", err)
	}
	defer rows.Close()
	tables := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan sensor state table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list sensor state tables: %v", err)
	}
	return tables
}
