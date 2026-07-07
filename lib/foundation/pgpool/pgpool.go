// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package pgpool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	portMappingMaxAttempts = 8
	portMappingMaxBackoff  = 2 * time.Second
	startupTimeout         = 300 * time.Second
	bootMaxAttempts        = 3
	bootRetryBackoff       = 5 * time.Second
)

type Config struct {
	Image    string
	Database string
	User     string
	Password string

	InitTemplate func(ctx context.Context, dsn string) error
}

type Pool struct {
	cfg Config

	bootOnce  sync.Once
	bootErr   error
	baseDSN   string
	templateN string
	cloneN    atomic.Uint64
	adminMu   sync.Mutex
	admin     *pgxpool.Pool
}

func New(cfg Config) *Pool {
	if cfg.Image == "" {
		panic("pgpool: Image required")
	}
	if cfg.Database == "" {
		panic("pgpool: Database required")
	}
	if cfg.User == "" {
		cfg.User = cfg.Database
	}
	if cfg.Password == "" {
		cfg.Password = cfg.Database
	}
	return &Pool{cfg: cfg}
}

func (p *Pool) Acquire(ctx context.Context, t testing.TB) string {
	t.Helper()
	if err := p.boot(ctx); err != nil {
		t.Fatalf("pgpool: boot: %v", err)
	}
	if p.templateN == "" {
		t.Fatalf("pgpool: Acquire requires InitTemplate; use AcquireFresh for an empty database")
	}
	return p.cloneAndRegisterCleanup(ctx, t, p.templateN)
}

func (p *Pool) AcquireFresh(ctx context.Context, t testing.TB) string {
	t.Helper()
	if err := p.boot(ctx); err != nil {
		t.Fatalf("pgpool: boot: %v", err)
	}
	return p.cloneAndRegisterCleanup(ctx, t, "")
}

func (p *Pool) cloneAndRegisterCleanup(ctx context.Context, t testing.TB, fromTemplate string) string {
	t.Helper()
	cloneName := p.nextCloneName()
	if err := p.createDatabase(ctx, cloneName, fromTemplate); err != nil {
		t.Fatalf("pgpool: create %q: %v", cloneName, err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := p.dropDatabase(dropCtx, cloneName); err != nil {
			t.Logf("pgpool: drop %q warn: %v", cloneName, err)
		}
	})
	dsn, err := dsnForDatabase(p.baseDSN, cloneName)
	if err != nil {
		t.Fatalf("pgpool: rewrite dsn: %v", err)
	}
	return dsn
}

func runPostgresWithRetry(ctx context.Context, cfg Config) (*pgmodule.PostgresContainer, error) {
	var lastErr error
	for attempt := 1; attempt <= bootMaxAttempts; attempt++ {
		container, err := pgmodule.Run(ctx,
			cfg.Image,
			pgmodule.WithDatabase(cfg.Database),
			pgmodule.WithUsername(cfg.User),
			pgmodule.WithPassword(cfg.Password),
			testcontainers.WithCmdArgs(
				"-c", "synchronous_commit=off",
				"-c", "full_page_writes=off",
				"-c", "autovacuum=off",
			),
			testcontainers.WithWaitStrategy(
				wait.ForAll(
					wait.ForLog("database system is ready to accept connections").
						WithOccurrence(2).WithStartupTimeout(startupTimeout),
					wait.ForListeningPort("5432/tcp").WithStartupTimeout(startupTimeout),
				),
			),
		)
		if err == nil {
			return container, nil
		}
		if container != nil {
			termCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			_ = container.Terminate(termCtx)
			cancel()
		}
		lastErr = err
		if attempt < bootMaxAttempts {
			time.Sleep(time.Duration(attempt) * bootRetryBackoff)
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", bootMaxAttempts, lastErr)
}

func (p *Pool) boot(ctx context.Context) error {
	p.bootOnce.Do(func() {
		container, err := runPostgresWithRetry(ctx, p.cfg)
		if err != nil {
			p.bootErr = fmt.Errorf("start postgres: %w", err)
			return
		}
		baseDSN, err := resolveConnectionString(ctx, container)
		if err != nil {
			p.bootErr = fmt.Errorf("connection string: %w", err)
			return
		}
		p.baseDSN = baseDSN

		adminDSN, err := dsnForDatabase(baseDSN, "postgres")
		if err != nil {
			p.bootErr = fmt.Errorf("admin dsn: %w", err)
			return
		}
		adminPool, err := pgxpool.New(ctx, adminDSN)
		if err != nil {
			p.bootErr = fmt.Errorf("admin pool: %w", err)
			return
		}
		p.admin = adminPool

		if p.cfg.InitTemplate != nil {
			templateName := p.cfg.Database + "_tmpl"
			if _, err := adminPool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, quoteIdent(templateName))); err != nil {
				p.bootErr = fmt.Errorf("create template db: %w", err)
				return
			}
			templateDSN, err := dsnForDatabase(baseDSN, templateName)
			if err != nil {
				p.bootErr = fmt.Errorf("template dsn: %w", err)
				return
			}
			if err := p.cfg.InitTemplate(ctx, templateDSN); err != nil {
				p.bootErr = fmt.Errorf("init template: %w", err)
				return
			}
			p.templateN = templateName
		}
	})
	return p.bootErr
}

func (p *Pool) createDatabase(ctx context.Context, name, fromTemplate string) error {
	p.adminMu.Lock()
	defer p.adminMu.Unlock()
	var sql string
	if fromTemplate == "" {
		sql = fmt.Sprintf(`CREATE DATABASE %s`, quoteIdent(name))
	} else {
		sql = fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`,
			quoteIdent(name), quoteIdent(fromTemplate))
	}
	_, err := p.admin.Exec(ctx, sql)
	return err
}

func (p *Pool) dropDatabase(ctx context.Context, name string) error {
	p.adminMu.Lock()
	defer p.adminMu.Unlock()
	sql := fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, quoteIdent(name))
	_, err := p.admin.Exec(ctx, sql)
	return err
}

func (p *Pool) nextCloneName() string {
	n := p.cloneN.Add(1)
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("%s_t_%d_%s", p.cfg.Database, n, hex.EncodeToString(buf[:]))
}

func dsnForDatabase(baseDSN, dbName string) (string, error) {
	u, err := url.Parse(baseDSN)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func resolveConnectionString(ctx context.Context, container *pgmodule.PostgresContainer) (string, error) {
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= portMappingMaxAttempts; attempt++ {
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err == nil {
			return dsn, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "port") || !strings.Contains(err.Error(), "not found") {
			return "", err
		}
		if attempt < portMappingMaxAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > portMappingMaxBackoff {
				backoff = portMappingMaxBackoff
			}
		}
	}
	return "", lastErr
}
