package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// MaxEnvFileBytes caps both files read at startup and files written through
// the settings editor.
const MaxEnvFileBytes = 1 << 20

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseEnvFile parses a deliberately small .env subset: comments, optional
// "export", single/double quoted values, and inline comments after whitespace.
// It never expands variables or executes anything.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxEnvFileBytes {
		return nil, fmt.Errorf("environment file %s is larger than 1 MiB", path)
	}
	return parseEnv(f)
}

func parseEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid environment file line %d: expected KEY=VALUE", lineNo)
		}
		key = strings.TrimSpace(key)
		if !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid environment file line %d: invalid variable name", lineNo)
		}
		parsed, err := parseEnvValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid environment file line %d: %w", lineNo, err)
		}
		out[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, errors.New("environment file line is too long")
		}
		return nil, err
	}
	return out, nil
}

func parseEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] == '\'' {
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", errors.New("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	}
	if raw[0] == '"' {
		if len(raw) < 2 || raw[len(raw)-1] != '"' {
			return "", errors.New("unterminated double-quoted value")
		}
		// Unquote only common escapes; do not interpret shell syntax.
		var b strings.Builder
		inner := raw[1 : len(raw)-1]
		for i := 0; i < len(inner); i++ {
			if inner[i] != '\\' || i+1 >= len(inner) {
				b.WriteByte(inner[i])
				continue
			}
			i++
			switch inner[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte('\\')
				b.WriteByte(inner[i])
			}
		}
		return b.String(), nil
	}
	// Unquoted: an inline comment starts at a '#' preceded by whitespace.
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i > 0 && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			raw = strings.TrimSpace(raw[:i])
			break
		}
	}
	return raw, nil
}
