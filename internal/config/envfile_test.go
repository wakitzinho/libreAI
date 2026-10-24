package config

import (
	"strings"
	"testing"
)

func TestParseEnv(t *testing.T) {
	raw := `
# A comment
PLAIN=value
SPACED = value with spaces
SINGLE='literal $VALUE # not comment'
DOUBLE="line\nbreak\tand quote\""
INLINE=hello # trailing comment
HASH=value#kept
EMPTY=
export EXPORTED=ok
`
	values, err := parseEnv(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	want := map[string]string{
		"PLAIN":    "value",
		"SPACED":   "value with spaces",
		"SINGLE":   "literal $VALUE # not comment",
		"DOUBLE":   "line\nbreak\tand quote\"",
		"INLINE":   "hello",
		"HASH":     "value#kept",
		"EMPTY":    "",
		"EXPORTED": "ok",
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("%s = %q, want %q", k, values[k], v)
		}
	}
}

func TestParseEnvErrors(t *testing.T) {
	cases := []string{
		"NO EQUALS\n",
		"1BAD=value\n",
		"BAD-NAME=value\n",
		"QUOTE='unterminated\n",
		"QUOTE=\"unterminated\n",
		"BAD=value\nBAD-NAME=x\n",
	}
	for _, input := range cases {
		if _, err := parseEnv(strings.NewReader(input)); err == nil {
			t.Errorf("parseEnv(%q) unexpectedly succeeded", input)
		}
	}
}

func TestParseEnvValue(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"a # comment", "a"},
		{"a#keep", "a#keep"},
		{"' spaced '", " spaced "},
		{`"a\tb"`, "a\tb"},
		{`"a\qb"`, `a\qb`},
	}
	for _, tt := range tests {
		got, err := parseEnvValue(tt.raw)
		if err != nil {
			t.Fatalf("parseEnvValue(%q): %v", tt.raw, err)
		}
		if got != tt.want {
			t.Errorf("parseEnvValue(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
