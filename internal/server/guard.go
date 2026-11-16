// SPDX-License-Identifier: MIT

package server

// The guard keeps a customer's SQL inside its own database file and its own request. Every
// connection the pool opens gets, before it serves a request:
//
//   - an SQLite authorizer (sqlite3_set_authorizer) that refuses ATTACH and DETACH, transaction
//     control (BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE) unless the server itself is issuing it,
//     load_extension, and every pragma outside an allowlist;
//   - SQLITE_LIMIT_ATTACHED = 0, so no database can be attached, VACUUM's own included (VACUUM
//     INTO has no authorizer check, and it attaches its target);
//   - SQLITE_LIMIT_LENGTH = MaxValueBytes, so no string, blob or row outgrows what one response
//     can carry.
//
// modernc.org/sqlite exposes neither an authorizer nor the connection's sqlite3 handle, so
// connHandle reads the handle from the driver's connection by reflection. It checks the layout it
// expects and fails the connection (and so Open) when a driver upgrade changes it, rather than
// running unguarded.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// driverName is the guarded driver the server opens its database with.
const driverName = "lake-sqlite"

// MaxValueBytes is SQLITE_LIMIT_LENGTH on every connection: the largest string, blob or row, 8 MiB.
const MaxValueBytes = 8 << 20

func init() {
	guarded := &sqlite.Driver{}
	guarded.RegisterConnectionHook(guardConnection)
	sql.Register(driverName, guarded)
}

// guard is one connection's authorizer state.
type guard struct {
	// control is set while the server runs its own BEGIN, COMMIT or ROLLBACK.
	control atomic.Bool
}

// guards maps a connection's sqlite3 handle to its guard; the handle is the authorizer's pArg.
var guards sync.Map // uintptr -> *guard

// connHandle returns the sqlite3 handle and TLS of a modernc.org/sqlite connection.
func connHandle(driverConn any) (uintptr, *libc.TLS, error) {
	v := reflect.ValueOf(driverConn)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return 0, nil, fmt.Errorf("guard: unexpected driver connection %T", driverConn)
	}
	db := v.Elem().FieldByName("db")
	tls := v.Elem().FieldByName("tls")
	if !db.IsValid() || db.Kind() != reflect.Uintptr ||
		!tls.IsValid() || tls.Type() != reflect.TypeOf((*libc.TLS)(nil)) {
		return 0, nil, fmt.Errorf("guard: the layout of %T changed; update connHandle", driverConn)
	}
	handle := uintptr(db.Uint())
	t := (*libc.TLS)(unsafe.Pointer(tls.Pointer()))
	if handle == 0 || t == nil {
		return 0, nil, errors.New("guard: the connection is not open")
	}
	return handle, t, nil
}

// guardConnection is the connection hook: it runs after the DSN's pragmas, before any request.
func guardConnection(conn sqlite.ExecQuerierContext, _ string) error {
	handle, tls, err := connHandle(conn)
	if err != nil {
		return err
	}
	guards.Store(handle, &guard{})
	if rc := sqlite3.Xsqlite3_set_authorizer(tls, handle, authorizerPointer(authorize), handle); rc != sqlite3.SQLITE_OK {
		return fmt.Errorf("guard: sqlite3_set_authorizer: %d", rc)
	}
	sqlite3.Xsqlite3_limit(tls, handle, sqlite3.SQLITE_LIMIT_ATTACHED, 0)
	sqlite3.Xsqlite3_limit(tls, handle, sqlite3.SQLITE_LIMIT_LENGTH, MaxValueBytes)
	return nil
}

type authorizerFunc = func(tls *libc.TLS, pArg uintptr, action int32, arg1, arg2, dbName, trigger uintptr) int32

// authorizerPointer turns a function declaration into the C function pointer modernc's libc calls,
// the same conversion the driver uses for its own callbacks.
func authorizerPointer(f authorizerFunc) uintptr {
	return *(*uintptr)(unsafe.Pointer(&struct{ f authorizerFunc }{f}))
}

// authorize is the SQLite authorizer. It runs while a statement is prepared.
func authorize(_ *libc.TLS, pArg uintptr, action int32, arg1, arg2, _, _ uintptr) int32 {
	switch action {
	case sqlite3.SQLITE_ATTACH, sqlite3.SQLITE_DETACH:
		return sqlite3.SQLITE_DENY
	case sqlite3.SQLITE_TRANSACTION, sqlite3.SQLITE_SAVEPOINT:
		if g, ok := guards.Load(pArg); ok && g.(*guard).control.Load() {
			return sqlite3.SQLITE_OK
		}
		return sqlite3.SQLITE_DENY
	case sqlite3.SQLITE_FUNCTION:
		if libc.GoString(arg2) == "load_extension" {
			return sqlite3.SQLITE_DENY
		}
	case sqlite3.SQLITE_PRAGMA:
		if !pragmaAllowed(libc.GoString(arg1), arg2 != 0) {
			return sqlite3.SQLITE_DENY
		}
	}
	return sqlite3.SQLITE_OK
}

// pragmaAnyArgument are pragmas that only read the schema or the file, or change something that
// lives in the database file itself, so any argument is fine.
var pragmaAnyArgument = map[string]bool{
	"application_id": true, "auto_vacuum": true, "collation_list": true, "compile_options": true,
	"data_version": true, "database_list": true, "defer_foreign_keys": true,
	"foreign_key_check": true, "foreign_key_list": true, "freelist_count": true,
	"function_list": true, "incremental_vacuum": true, "index_info": true, "index_list": true,
	"index_xinfo": true, "integrity_check": true, "module_list": true, "optimize": true,
	"page_count": true, "pragma_list": true, "quick_check": true, "table_info": true,
	"table_list": true, "table_xinfo": true, "user_version": true, "wal_checkpoint": true,
}

// pragmaReadOnly are settings a request may read but not change: changing them would change the
// connection for whichever request uses it next, or the server's durability.
var pragmaReadOnly = map[string]bool{
	"automatic_index": true, "busy_timeout": true, "cache_size": true,
	"cache_spill": true, "cell_size_check": true, "encoding": true, "foreign_keys": true,
	"hard_heap_limit": true, "journal_mode": true, "journal_size_limit": true,
	"legacy_alter_table": true, "locking_mode": true, "max_page_count": true, "mmap_size": true,
	"page_size": true, "query_only": true, "recursive_triggers": true,
	"reverse_unordered_selects": true, "schema_version": true, "secure_delete": true,
	"soft_heap_limit": true, "synchronous": true, "temp_store": true, "threads": true,
	"trusted_schema": true, "wal_autocheckpoint": true,
}

func pragmaAllowed(name string, hasArgument bool) bool {
	if pragmaAnyArgument[name] {
		return true
	}
	return pragmaReadOnly[name] && !hasArgument
}

// control runs one of the server's own transaction statements on conn.
func control(ctx context.Context, conn *sql.Conn, statement string) error {
	return conn.Raw(func(driverConn any) error {
		handle, _, err := connHandle(driverConn)
		if err != nil {
			return err
		}
		g, ok := guards.Load(handle)
		if !ok {
			return errors.New("guard: the connection has no guard")
		}
		execer, ok := driverConn.(driver.ExecerContext)
		if !ok {
			return fmt.Errorf("guard: %T cannot exec", driverConn)
		}
		g.(*guard).control.Store(true)
		defer g.(*guard).control.Store(false)
		_, err = execer.ExecContext(ctx, statement, nil)
		return err
	})
}

// release returns conn to the pool. A connection a request left inside a transaction is rolled
// back, and discarded when that fails, so no request ever starts on another's state.
func release(conn *sql.Conn) {
	inTransaction := false
	_ = conn.Raw(func(driverConn any) error {
		handle, tls, err := connHandle(driverConn)
		if err != nil {
			return driver.ErrBadConn
		}
		inTransaction = sqlite3.Xsqlite3_get_autocommit(tls, handle) == 0
		return nil
	})
	if inTransaction {
		ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		err := control(ctx, conn, "ROLLBACK")
		cancel()
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}
	_ = conn.Close()
}
