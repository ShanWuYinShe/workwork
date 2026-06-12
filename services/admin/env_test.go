package main

import "testing"

// A .env value consisting of a single quote character must not panic the
// loader (v[1:len(v)-1] would be v[1:0] and slice out of range).
func TestUnquoteEnvValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"`, `"`},
		{`'`, `'`},
		{``, ``},
		{`x`, `x`},
		{`""`, ``},
		{`''`, ``},
		{`"x"`, `x`},
		{`'x'`, `x`},
		{`"hello world"`, `hello world`},
		{`"unterminated`, `"unterminated`},
		{`"mismatched'`, `"mismatched'`},
	}
	for _, tc := range cases {
		if got := unquoteEnvValue(tc.in); got != tc.want {
			t.Errorf("unquoteEnvValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
