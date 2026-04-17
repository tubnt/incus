package sshexec

import "testing"

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "''"},
		{"abc", "'abc'"},
		{"a b", "'a b'"},
		{"with 'single'", `'with '\''single'\'''`},
		{"semi;colon && rm", "'semi;colon && rm'"},
		{"$(whoami)", "'$(whoami)'"},
		{"`id`", "'`id`'"},
	}
	for _, c := range cases {
		got := shellQuote(c.in)
		if got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
