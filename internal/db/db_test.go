package db

import "testing"

func TestDetectDialect(t *testing.T) {
	cases := []struct {
		url  string
		want Dialect
		err  bool
	}{
		{"postgres://u:p@host/db", DialectPostgres, false},
		{"postgresql://u:p@host/db", DialectPostgres, false},
		{"sqlite:///var/lib/app.db", DialectSQLite, false},
		{"file:./data.db", DialectSQLite, false},
		{"./data.db", DialectSQLite, false},
		{"mysql://u:p@host/db", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got, err := DetectDialect(tc.url)
			if (err != nil) != tc.err {
				t.Fatalf("err=%v want_err=%v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
