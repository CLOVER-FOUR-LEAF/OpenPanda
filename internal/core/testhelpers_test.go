// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Xustalis/OpenPanda/internal/storage"
)

// testLogger returns a silent logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// verboseTestLogger returns a logger that writes to the test's log output.
func verboseTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// openTestDB returns an in-memory SQLite DB with the Phase 0 schema applied.
// The driver is registered via storage.Open.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

// waitLocalTasksSettled polls a core's store until no task sits in a
// non-terminal state. Wire tests that forward work assert on the routing
// decision and return while the peer is still executing — the executor's
// run writes its per-task work dir inside the node's t.TempDir(), and the
// test's RemoveAll cleanup then unlinks a directory mid-write ("directory
// not empty"). Draining execution first keeps that race out of CI.
func waitLocalTasksSettled(t *testing.T, c *Core) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		err := c.store.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM tasks WHERE state NOT IN ('done','failed','cancelled','expired')`).Scan(&n)
		if err != nil {
			t.Fatalf("count tasks: %v", err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node %s still has %d non-terminal tasks", c.nodeID, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
