//go:build !cgo

package sqlite

// SQLite is required; compile with CGO_ENABLED=1 and system libsqlite3.
// This package intentionally fails fast without the required dependency.
var _ = requires_CGO_ENABLED_1_and_libsqlite3
