// SPDX-License-Identifier: AGPL-3.0-or-later

package sqliteimport

// Test-only accessors. tableOrder and the exclusion sets stay unexported
// — they are this package's internal bookkeeping, not API — but the
// parity test lives in the external test package alongside the rest.

func TableOrderForTest() []string { return tableOrder }

func IsExcludedForTest(table string) (string, bool) { return isExcluded(table) }
