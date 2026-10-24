package httpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"libreai/internal/config"
	"libreai/internal/provider"
)

// envSecretPlaceholder is shown instead of existing secret values. Leaving it
// untouched keeps the current value; replacing it writes a new one.
const envSecretPlaceholder = "__LIBREAI_KEEP_SECRET__"

var envEditorLine = regexp.MustCompile(`^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_]*)(\s*=\s*)(.*)$`)

type envFileDTO struct {
	Path              string `json:"path"`
	Contents          string `json:"contents"`
	Exists            bool   `json:"exists"`
	SecretPlaceholder string `json:"secret_placeholder"`
}

type envFileRequest struct {
	Contents string `json:"contents"`
}

func (s *Server) handleGetEnvFile(w http.ResponseWriter, r *http.Request) {
	path, ok := s.envFilePath(w)
	if !ok {
		return
	}
	data, exists, err := readEnvFile(path)
	if err != nil {
		s.logger.Error("could not read env file", "path", path, "error", err)
		writeError(w, http.StatusInternalServerError, "env_read_failed", "Could not read the configuration file.")
		return
	}
	writeJSON(w, http.StatusOK, envFileDTO{
		Path:              path,
		Contents:          maskEnvSecrets(string(data)),
		Exists:            exists,
		SecretPlaceholder: envSecretPlaceholder,
	})
}

func (s *Server) handlePutEnvFile(w http.ResponseWriter, r *http.Request) {
	path, ok := s.envFilePath(w)
	if !ok {
		return
	}
	var req envFileRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if !s.handleRequestError(w, err) {
			s.internalError(w, err)
		}
		return
	}
	if len(req.Contents) > config.MaxEnvFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "env_too_large", "The .env file must be at most 1 MiB.")
		return
	}

	current, _, err := readEnvFile(path)
	if err != nil {
		s.logger.Error("could not read env file", "path", path, "error", err)
		writeError(w, http.StatusInternalServerError, "env_read_failed", "Could not read the configuration file.")
		return
	}
	contents := restoreEnvSecrets(req.Contents, string(current))

	cfg, warnings, err := config.ValidateEnvFileContents(contents)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_env", "Configuration is invalid: "+err.Error())
		return
	}
	cfg.EnvFile = path

	if err := writeEnvFile(path, []byte(contents)); err != nil {
		s.logger.Error("could not write env file", "path", path, "error", err)
		writeError(w, http.StatusInternalServerError, "env_write_failed", "Could not write the configuration file. Check its permissions.")
		return
	}

	// Provider configuration can be applied immediately by swapping the
	// registry. Server-level options (HOST, PORT, DATABASE_PATH, ...) still
	// require a restart because they are captured at startup.
	reg := provider.NewRegistry(cfg, s.logger)
	s.setRegistry(reg)

	writeJSON(w, http.StatusOK, map[string]any{
		"path":             path,
		"providers":        reg.Infos(),
		"warnings":         warnings,
		"applied":          true,
		"restart_required": configNeedsRestart(s.cfg, cfg),
	})
}

func (s *Server) envFilePath(w http.ResponseWriter) (string, bool) {
	path := strings.TrimSpace(s.cfg.EnvFile)
	if path == "" {
		writeError(w, http.StatusConflict, "env_unavailable", "The configuration file path is unavailable. Start libreAI with -env <path>.")
		return "", false
	}
	return path, true
}

func readEnvFile(path string) ([]byte, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > config.MaxEnvFileBytes {
		return nil, false, fmt.Errorf("%s is larger than 1 MiB", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func writeEnvFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".libreai-env-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func maskEnvSecrets(contents string) string {
	lines := strings.SplitAfter(contents, "\n")
	for i, line := range lines {
		body, ending := splitEnvLineEnding(line)
		prefix, key, sep, value, ok := matchEnvLine(body)
		if !ok || !isSecretEnvKey(key) || strings.TrimSpace(value) == "" {
			continue
		}
		lines[i] = prefix + key + sep + envSecretPlaceholder + ending
	}
	return strings.Join(lines, "")
}

func restoreEnvSecrets(contents, current string) string {
	originals := map[string]string{}
	for _, line := range strings.Split(current, "\n") {
		body, _ := splitEnvLineEnding(line)
		_, key, _, value, ok := matchEnvLine(body)
		if !ok || !isSecretEnvKey(key) || strings.TrimSpace(value) == "" {
			continue
		}
		originals[key] = value
	}

	lines := strings.SplitAfter(contents, "\n")
	for i, line := range lines {
		body, ending := splitEnvLineEnding(line)
		prefix, key, sep, value, ok := matchEnvLine(body)
		if !ok || !isSecretEnvKey(key) || !isSecretPlaceholder(value) {
			continue
		}
		if original, exists := originals[key]; exists {
			lines[i] = prefix + key + sep + original + ending
		}
	}
	return strings.Join(lines, "")
}

func matchEnvLine(line string) (prefix, key, sep, value string, ok bool) {
	m := envEditorLine.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", "", false
	}
	return m[1], m[2], m[3], m[4], true
}

func splitEnvLineEnding(line string) (body, ending string) {
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
		ending = "\n"
	}
	if strings.HasSuffix(line, "\r") {
		line = strings.TrimSuffix(line, "\r")
		ending = "\r" + ending
	}
	return line, ending
}

func isSecretEnvKey(key string) bool {
	up := strings.ToUpper(key)
	return strings.Contains(up, "API_KEY") ||
		strings.HasSuffix(up, "_KEY") ||
		strings.HasSuffix(up, "_TOKEN") ||
		strings.Contains(up, "TOKEN_") ||
		strings.HasSuffix(up, "_SECRET") ||
		strings.Contains(up, "PASSWORD") ||
		up == "HEADERS" ||
		strings.HasSuffix(up, "_HEADERS")
}

func isSecretPlaceholder(value string) bool {
	value = strings.TrimSpace(value)
	if value == envSecretPlaceholder {
		return true
	}
	if len(value) < 2 {
		return false
	}
	if (value[0] == '\'' && value[len(value)-1] == '\'') ||
		(value[0] == '"' && value[len(value)-1] == '"') {
		return value[1:len(value)-1] == envSecretPlaceholder
	}
	return false
}

func configNeedsRestart(old, next config.Config) bool {
	return old.Host != next.Host ||
		old.Port != next.Port ||
		old.DatabasePath != next.DatabasePath ||
		old.RetentionDays != next.RetentionDays ||
		old.UpstreamTimeout != next.UpstreamTimeout ||
		old.MaxConcurrentGenerations != next.MaxConcurrentGenerations ||
		old.MaxRequestBodyBytes != next.MaxRequestBodyBytes ||
		old.MaxContextMessages != next.MaxContextMessages ||
		old.MaxContextBytes != next.MaxContextBytes ||
		old.ShutdownTimeout != next.ShutdownTimeout ||
		old.LogLevel != next.LogLevel
}
