// SPDX-License-Identifier: AGPL-3.0-or-later

package db

// Register the modernc pure-Go SQLite driver under the "sqlite" name.
// The blank import causes database/sql to call modernc.org/sqlite's
// init(), which registers the driver.
//
// embookshelf is Postgres-only (ADR-0023). This registration survives
// for exactly one reason: `embookshelf import-sqlite` has to read an old
// SQLite library to migrate it. Nothing that serves requests may open
// SQLite. Delete this file, and the modernc dependency, when the
// importer is retired.
import _ "modernc.org/sqlite"
