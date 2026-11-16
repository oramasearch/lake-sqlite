// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// queryInt runs a one-value query and returns it.
func (h *harness) queryInt(sql string) int64 {
	h.t.Helper()
	status, body := h.post("/v1/query", map[string]interface{}{"sql": sql})
	if status != http.StatusOK {
		h.t.Fatalf("query %q: %d %v", sql, status, body)
	}
	rows := body["rows"].([]interface{})
	n, err := rows[0].([]interface{})[0].(json.Number).Int64()
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// TestGuardRefusesFilesOutsideTheDatabase: no statement reaches a file other than the database.
func TestGuardRefusesFilesOutsideTheDatabase(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustExec("CREATE TABLE t (x)")
	outside := t.TempDir()
	for _, sql := range []string{
		fmt.Sprintf("ATTACH DATABASE '%s' AS e", filepath.Join(outside, "attach.db")),
		fmt.Sprintf("ATTACH 'file:%s?mode=rwc' AS e", filepath.Join(outside, "uri.db")),
		fmt.Sprintf("VACUUM INTO '%s'", filepath.Join(outside, "copy.db")),
		"VACUUM",
		fmt.Sprintf("PRAGMA temp_store_directory = '%s'", outside),
		"DETACH DATABASE main",
		"SELECT load_extension('x')",
	} {
		for _, path := range []string{"/v1/exec", "/v1/query"} {
			status, body := h.post(path, map[string]interface{}{"sql": sql})
			if status == http.StatusOK {
				t.Errorf("%s %q: answered 200, want refused", path, sql)
			}
			_ = body
		}
		status, _ := h.post("/v1/batch", map[string]interface{}{"statements": []map[string]interface{}{{"sql": sql}}})
		if status == http.StatusOK {
			t.Errorf("batch %q: answered 200, want refused", sql)
		}
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("files were written outside the database: %v", entries)
	}
}

func TestGuardAnswersNotAllowed(t *testing.T) {
	h := newHarness(t, Config{})
	status, body := h.post("/v1/exec", map[string]interface{}{"sql": "ATTACH ':memory:' AS m"})
	if status != http.StatusForbidden || errorCode(body) != "not_allowed" {
		t.Fatalf("ATTACH: %d %v, want 403 not_allowed", status, body)
	}
}

// TestGuardPragmas: reading pragmas and the file's own settings work; changing the connection does
// not.
func TestGuardPragmas(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustExec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	for _, sql := range []string{
		"PRAGMA table_info(t)", "PRAGMA foreign_keys", "PRAGMA journal_mode", "PRAGMA integrity_check",
		"PRAGMA user_version = 7", "PRAGMA user_version", "SELECT * FROM pragma_table_info('t')",
		"PRAGMA main.index_list(t)", "PRAGMA optimize", "PRAGMA auto_vacuum = INCREMENTAL",
	} {
		if status, body := h.post("/v1/query", map[string]interface{}{"sql": sql}); status != http.StatusOK {
			t.Errorf("%q: %d %v, want 200", sql, status, body)
		}
	}
	for _, sql := range []string{
		"PRAGMA foreign_keys = OFF", "PRAGMA journal_mode = OFF", "PRAGMA synchronous = OFF",
		"PRAGMA writable_schema = ON", "PRAGMA query_only = 1", "PRAGMA locking_mode = EXCLUSIVE",
		"PRAGMA cache_size = -1000000", "PRAGMA mmap_size = 1000000000", "PRAGMA soft_heap_limit = 1",
		"PRAGMA hard_heap_limit = 1", "PRAGMA busy_timeout = 0", "PRAGMA schema_version = 1",
		"PRAGMA data_store_directory = '/tmp'", "PRAGMA case_sensitive_like = 1",
	} {
		if status, body := h.post("/v1/exec", map[string]interface{}{"sql": sql}); status != http.StatusForbidden {
			t.Errorf("%q: %d %v, want 403", sql, status, body)
		}
	}
	if h.queryInt("PRAGMA foreign_keys") != 1 {
		t.Fatal("foreign_keys changed")
	}
}

// TestGuardKeepsTheBatchOneTransaction: a statement in a batch cannot commit part of it.
func TestGuardKeepsTheBatchOneTransaction(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustExec("CREATE TABLE t (x NOT NULL)")
	for _, control := range []string{"COMMIT", "END", "ROLLBACK", "SAVEPOINT s", "RELEASE s", "BEGIN"} {
		status, body := h.post("/v1/batch", map[string]interface{}{"statements": []map[string]interface{}{
			{"sql": "INSERT INTO t VALUES (1)"},
			{"sql": control},
			{"sql": "INSERT INTO t VALUES (NULL)"},
		}})
		if status == http.StatusOK {
			t.Errorf("batch with %q: answered 200 %v", control, body)
		}
		// The same, hidden in a multi-statement sql.
		status, _ = h.post("/v1/batch", map[string]interface{}{"statements": []map[string]interface{}{
			{"sql": "INSERT INTO t VALUES (1); " + control},
			{"sql": "INSERT INTO t VALUES (NULL)"},
		}})
		if status == http.StatusOK {
			t.Errorf("batch with \"INSERT; %s\": answered 200", control)
		}
	}
	if n := h.queryInt("SELECT count(*) FROM t"); n != 0 {
		t.Fatalf("%d rows committed by failed batches, want 0", n)
	}
}

// TestGuardKeepsRequestsApart: no request leaves a transaction open for the next one, and no
// acknowledged write is lost.
func TestGuardKeepsRequestsApart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	h := newHarness(t, Config{Path: path})
	h.mustExec("CREATE TABLE t (x)")
	for _, sql := range []string{"BEGIN", "BEGIN IMMEDIATE", "BEGIN EXCLUSIVE", "SAVEPOINT s"} {
		if status, _ := h.post("/v1/exec", map[string]interface{}{"sql": sql}); status != http.StatusForbidden {
			t.Errorf("%q: %d, want 403", sql, status)
		}
	}
	h.mustExec("INSERT INTO t VALUES (1)")
	for i := 0; i < 2*maxOpenConns; i++ {
		status, body := h.post("/v1/batch", map[string]interface{}{"statements": []map[string]interface{}{{"sql": "INSERT INTO t VALUES (2)"}}})
		if status != http.StatusOK {
			t.Fatalf("batch %d: %d %v", i, status, body)
		}
	}
	// Restart: every acknowledged write is on disk.
	if err := h.server.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newHarness(t, Config{Path: path})
	if n := reopened.queryInt("SELECT count(*) FROM t"); n != int64(1+2*maxOpenConns) {
		t.Fatalf("%d rows after a restart, want %d", n, 1+2*maxOpenConns)
	}
}

// TestReleaseRollsBackAnOpenTransaction: the safety net under the authorizer.
func TestReleaseRollsBackAnOpenTransaction(t *testing.T) {
	h := newHarness(t, Config{})
	h.mustExec("CREATE TABLE t (x)")
	ctx := context.Background()
	conn, err := h.server.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := control(ctx, conn, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	release(conn)
	for i := 0; i < 2*maxOpenConns; i++ {
		h.mustExec("INSERT INTO t VALUES (2)")
	}
	if n := h.queryInt("SELECT count(*) FROM t WHERE x = 1"); n != 0 {
		t.Fatalf("the abandoned insert is visible (%d rows)", n)
	}
}

// TestValueLengthLimit: no value grows past MaxValueBytes, so no single cell can exhaust memory.
func TestValueLengthLimit(t *testing.T) {
	h := newHarness(t, Config{})
	for _, sql := range []string{
		fmt.Sprintf("SELECT zeroblob(%d)", MaxValueBytes+1),
		fmt.Sprintf("SELECT length(randomblob(%d))", MaxValueBytes+1),
		fmt.Sprintf("SELECT length(CAST(zeroblob(%d) AS TEXT) || 'xx')", MaxValueBytes),
	} {
		if status, body := h.post("/v1/query", map[string]interface{}{"sql": sql}); status != http.StatusBadRequest {
			t.Errorf("%q: %d %v, want 400 (string or blob too big)", sql, status, body)
		}
	}
	if n := h.queryInt(fmt.Sprintf("SELECT length(zeroblob(%d))", MaxValueBytes)); n != MaxValueBytes {
		t.Fatalf("a value of exactly MaxValueBytes: length %d", n)
	}
}

// TestResponseBudget: one response, a whole batch included, carries at most MaxResponseBytes.
func TestResponseBudget(t *testing.T) {
	h := newHarness(t, Config{MaxResponseBytes: 1000})
	h.mustExec("CREATE TABLE t (v TEXT)")
	h.mustExec("WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 50) INSERT INTO t SELECT printf('%.*c', 100, 'x') FROM n")

	status, body := h.post("/v1/query", map[string]interface{}{"sql": "SELECT v FROM t"})
	if status != http.StatusOK || body["truncated"] != true || len(body["rows"].([]interface{})) != 10 {
		t.Fatalf("query: %d truncated=%v rows=%d, want 10 rows truncated", status, body["truncated"], len(body["rows"].([]interface{})))
	}

	status, body = h.post("/v1/batch", map[string]interface{}{"statements": []map[string]interface{}{
		{"sql": "SELECT v FROM t LIMIT 6"}, {"sql": "SELECT v FROM t LIMIT 6"},
	}})
	if status != http.StatusOK {
		t.Fatalf("batch: %d %v", status, body)
	}
	results := body["results"].([]interface{})
	second := results[1].(map[string]interface{})
	if second["truncated"] != true || len(second["rows"].([]interface{})) != 4 {
		t.Fatalf("batch: second statement %v, want 4 rows truncated (the budget is shared)", second)
	}

	status, body = h.post("/v1/query", map[string]interface{}{"sql": "SELECT printf('%.*c', 2000, 'x')"})
	if status != http.StatusRequestEntityTooLarge || errorCode(body) != "response_too_large" {
		t.Fatalf("one row over the budget: %d %v, want 413 response_too_large", status, body)
	}
}

func TestBatchStatementCap(t *testing.T) {
	h := newHarness(t, Config{})
	statements := make([]map[string]interface{}, MaxBatchStatements+1)
	for i := range statements {
		statements[i] = map[string]interface{}{"sql": "SELECT 1"}
	}
	status, body := h.post("/v1/batch", map[string]interface{}{"statements": statements})
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body), "more than") {
		t.Fatalf("%d statements: %d %v, want 400", len(statements), status, body)
	}
	status, _ = h.post("/v1/batch", map[string]interface{}{"statements": statements[:MaxBatchStatements]})
	if status != http.StatusOK {
		t.Fatalf("%d statements: %d, want 200", MaxBatchStatements, status)
	}
}
