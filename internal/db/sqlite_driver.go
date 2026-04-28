package db

// Register the modernc pure-Go SQLite driver under the "sqlite" name.
// The blank import causes database/sql to call modernc.org/sqlite's init(),
// which registers the driver. Subsequent tasks in Plan 2A use
// sql.Open("sqlite", dsn) when Dialect == DialectSQLite.
import _ "modernc.org/sqlite"
