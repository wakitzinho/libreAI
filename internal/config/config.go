// Package config loads libreAI's server-side configuration from a .env file
// and the process environment. Environment variables always take precedence
// over values in the .env file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHost               = "127.0.0.1"
	DefaultPort               = 8080
	DefaultDatabasePath       = "./data/libreai.db"
	DefaultRetentionDays      = 30
	DefaultUpstreamTimeout    = 300 * time.Second
	DefaultMaxConcurrent      = 2
	DefaultMaxRequestBody     = 1 << 20
	DefaultMaxContextMessages = 200
	DefaultMaxContextBytes    = 400_000
	DefaultShutdownTimeout    = 15 * time.Second
)

// Provider is one statically configured OpenAI-compatible provider.
// APIKey is never serialized or logged by the application.
type Provider struct {
	ID          string
	DisplayName string
	BaseURL     string
	APIKey      string
	Models      []string
	Enabled     bool
	Discovery   bool
	NoAuth      bool
	Timeout     time.Duration
	Headers     map[string]string
}

// Configured reports whether the provider has enough configuration to be used.
func (p Provider) Configured() bool {
	return p.Enabled && p.BaseURL != "" && (p.APIKey != "" || p.NoAuth)
}

// Status returns a short, non-sensitive provider status.
func (p Provider) Status() string {
	switch {
	case !p.Enabled:
		return "disabled"
	case p.BaseURL == "":
		return "base_url_missing"
	case p.APIKey == "" && !p.NoAuth:
		return "api_key_missing"
	default:
		return "ready"
	}
}

// String renders a provider without its API key or custom header values. It is
// used by fmt-style diagnostics; slog uses LogValue below.
func (p Provider) String() string {
	return fmt.Sprintf("Provider{id:%q name:%q base_url:%q enabled:%t discovery:%t no_auth:%t models:%d}",
		p.ID, p.DisplayName, p.BaseURL, p.Enabled, p.Discovery, p.NoAuth, len(p.Models))
}

// LogValue implements slog.LogValuer so structured logs cannot accidentally
// expose credentials or custom header values.
func (p Provider) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", p.ID),
		slog.String("name", p.DisplayName),
		slog.Bool("enabled", p.Enabled),
		slog.Bool("configured", p.Configured()),
		slog.String("status", p.Status()),
		slog.Int("models", len(p.Models)),
	)
}

// Config is the complete runtime configuration.
type Config struct {
	Host                     string
	Port                     int
	DatabasePath             string
	RetentionDays            int
	UpstreamTimeout          time.Duration
	MaxConcurrentGenerations int
	MaxRequestBodyBytes      int64
	MaxContextMessages       int
	MaxContextBytes          int
	ShutdownTimeout          time.Duration
	LogLevel                 string
	Providers                []Provider
	// EnvFile is the .env path this configuration was loaded from. It is used
	// by the settings editor and is never exposed as a configurable value.
	EnvFile string
}

// ListenAddr returns the address passed to net.Listen.
func (c Config) ListenAddr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// ProviderByID returns the provider with the given ID (case-insensitive).
func (c Config) ProviderByID(id string) (Provider, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Load reads envFile (if it exists), overlays the process environment, and
// validates the result. A missing env file is not an error. Warnings are
// non-fatal messages safe to log (no secret values).
func Load(envFile string) (Config, []string, error) {
	fileValues, err := parseEnvFile(envFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, nil, err
	}
	cfg, warnings, err := FromMap(mergeEnvValues(fileValues))
	if err != nil {
		return Config{}, warnings, err
	}
	cfg.EnvFile = envFile
	if info, statErr := os.Stat(envFile); statErr == nil {
		perm := info.Mode().Perm()
		if perm&0o007 != 0 || perm&0o030 != 0 {
			warnings = append(warnings, fmt.Sprintf("%s is accessible by other users (mode %04o); recommend chmod 600, or root:libreai mode 0640 for systemd", envFile, perm))
		}
	}
	return cfg, warnings, nil
}

// ValidateEnvFileContents checks a prospective .env file without writing it.
// The process environment is overlaid just like Load, so validation matches
// what the server would use after saving and restarting.
func ValidateEnvFileContents(contents string) (Config, []string, error) {
	if len(contents) > MaxEnvFileBytes {
		return Config{}, nil, fmt.Errorf("environment file is larger than 1 MiB")
	}
	if strings.ContainsRune(contents, '\x00') {
		return Config{}, nil, errors.New("environment file must not contain NUL bytes")
	}
	fileValues, err := parseEnv(strings.NewReader(contents))
	if err != nil {
		return Config{}, nil, err
	}
	return FromMap(mergeEnvValues(fileValues))
}

// mergeEnvValues applies provider aliases and overlays explicit process
// environment variables on top of values from the .env file.
func mergeEnvValues(fileValues map[string]string) map[string]string {
	applyProviderAliases(fileValues)
	values := make(map[string]string, len(fileValues)+16)
	for k, v := range fileValues {
		values[k] = v
	}
	envValues := make(map[string]string)
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			envValues[k] = v
		}
	}
	applyProviderAliases(envValues)
	for k, v := range envValues {
		values[k] = v
	}
	return values
}

// FromMap builds a Config from a set of environment-style key/value pairs.
func FromMap(values map[string]string) (Config, []string, error) {
	if values == nil {
		values = map[string]string{}
	}
	applyProviderAliases(values)
	cfg := Config{
		Host:                     DefaultHost,
		Port:                     DefaultPort,
		DatabasePath:             DefaultDatabasePath,
		RetentionDays:            DefaultRetentionDays,
		UpstreamTimeout:          DefaultUpstreamTimeout,
		MaxConcurrentGenerations: DefaultMaxConcurrent,
		MaxRequestBodyBytes:      DefaultMaxRequestBody,
		MaxContextMessages:       DefaultMaxContextMessages,
		MaxContextBytes:          DefaultMaxContextBytes,
		ShutdownTimeout:          DefaultShutdownTimeout,
		LogLevel:                 "info",
	}
	var errs []string
	var warnings []string

	if v, ok := values["HOST"]; ok {
		cfg.Host = strings.TrimSpace(v)
	}
	if cfg.Host == "" {
		errs = append(errs, "HOST must not be empty")
	} else if strings.ContainsAny(cfg.Host, " \t\r\n/") || len(cfg.Host) > 255 {
		errs = append(errs, "HOST is not a valid bind address")
	}

	if v, ok := values["PORT"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > 65535 {
			errs = append(errs, "PORT must be an integer between 1 and 65535")
		} else {
			cfg.Port = n
		}
	}
	if v, ok := values["DATABASE_PATH"]; ok {
		cfg.DatabasePath = strings.TrimSpace(v)
	}
	if cfg.DatabasePath == "" {
		errs = append(errs, "DATABASE_PATH must not be empty")
	}

	if v, ok := values["CHAT_RETENTION_DAYS"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > 3650 {
			errs = append(errs, "CHAT_RETENTION_DAYS must be an integer between 1 and 3650")
		} else {
			cfg.RetentionDays = n
		}
	}
	if v, ok := values["UPSTREAM_TIMEOUT_SECONDS"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 10 || n > 3600 {
			errs = append(errs, "UPSTREAM_TIMEOUT_SECONDS must be an integer between 10 and 3600")
		} else {
			cfg.UpstreamTimeout = time.Duration(n) * time.Second
		}
	}
	if v, ok := values["MAX_CONCURRENT_GENERATIONS"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > 16 {
			errs = append(errs, "MAX_CONCURRENT_GENERATIONS must be an integer between 1 and 16")
		} else {
			cfg.MaxConcurrentGenerations = n
		}
	}
	if v, ok := values["MAX_REQUEST_BYTES"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 1024 || n > 10<<20 {
			errs = append(errs, "MAX_REQUEST_BYTES must be between 1024 and 10485760")
		} else {
			cfg.MaxRequestBodyBytes = n
		}
	}
	if v, ok := values["MAX_CONTEXT_MESSAGES"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > 1000 {
			errs = append(errs, "MAX_CONTEXT_MESSAGES must be an integer between 1 and 1000")
		} else {
			cfg.MaxContextMessages = n
		}
	}
	if v, ok := values["MAX_CONTEXT_BYTES"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1024 || n > 4<<20 {
			errs = append(errs, "MAX_CONTEXT_BYTES must be between 1024 and 4194304")
		} else {
			cfg.MaxContextBytes = n
		}
	}
	if v, ok := values["SHUTDOWN_TIMEOUT_SECONDS"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > 120 {
			errs = append(errs, "SHUTDOWN_TIMEOUT_SECONDS must be an integer between 1 and 120")
		} else {
			cfg.ShutdownTimeout = time.Duration(n) * time.Second
		}
	}
	if v, ok := values["LOG_LEVEL"]; ok && strings.TrimSpace(v) != "" {
		lv := strings.ToLower(strings.TrimSpace(v))
		switch lv {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = lv
		default:
			errs = append(errs, "LOG_LEVEL must be one of debug, info, warn, error")
		}
	}

	providers, providerWarnings, providerErrs := parseProviders(values)
	cfg.Providers = providers
	warnings = append(warnings, providerWarnings...)
	errs = append(errs, providerErrs...)

	sort.Slice(cfg.Providers, func(i, j int) bool { return cfg.Providers[i].ID < cfg.Providers[j].ID })

	if len(errs) > 0 {
		return Config{}, warnings, fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}
	return cfg, warnings, nil
}

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

var providerFieldSuffixes = []string{
	"_DISPLAY_NAME",
	"_TIMEOUT_SECONDS",
	"_BASE_URL",
	"_API_KEY",
	"_ENABLED",
	"_DISCOVERY",
	"_NO_AUTH",
	"_HEADERS",
	"_MODELS",
}

// providerAlias is a convenience variable for a known OpenAI-compatible
// provider. The canonical PROVIDER_<ID>_* variables always take precedence;
// an alias only fills in fields that are missing or empty.
type providerAlias struct {
	envKey      string
	id          string
	displayName string
	baseURL     string
}

var providerAliases = []providerAlias{
	{
		envKey:      "OPENROUTER_API_KEY",
		id:          "openrouter",
		displayName: "OpenRouter",
		baseURL:     "https://openrouter.ai/api/v1",
	},
}

// applyProviderAliases expands recognized shorthand variables into the
// canonical provider fields. It is called separately for the env file and the
// process environment so that normal environment precedence still applies.
func applyProviderAliases(values map[string]string) {
	for _, alias := range providerAliases {
		apiKey := strings.TrimSpace(values[alias.envKey])
		if apiKey == "" {
			continue
		}
		prefix := "PROVIDER_" + strings.ToUpper(alias.id) + "_"
		setProviderFieldIfEmpty(values, prefix+"API_KEY", apiKey)
		setProviderFieldIfEmpty(values, prefix+"DISPLAY_NAME", alias.displayName)
		setProviderFieldIfEmpty(values, prefix+"BASE_URL", alias.baseURL)
		// An explicit ENABLED value wins, including false.
		if strings.TrimSpace(values[prefix+"ENABLED"]) == "" {
			values[prefix+"ENABLED"] = "true"
		}
	}
}

func setProviderFieldIfEmpty(values map[string]string, key, value string) {
	if strings.TrimSpace(values[key]) == "" {
		values[key] = value
	}
}

func parseProviders(values map[string]string) ([]Provider, []string, []string) {
	type builder struct {
		fields map[string]string
	}
	builders := map[string]*builder{}
	var errs []string
	for key, value := range values {
		if !strings.HasPrefix(key, "PROVIDER_") {
			continue
		}
		rest := strings.TrimPrefix(key, "PROVIDER_")
		matched := ""
		for _, suffix := range providerFieldSuffixes {
			if strings.HasSuffix(rest, suffix) {
				matched = suffix
				break
			}
		}
		if matched == "" {
			continue
		}
		id := strings.ToLower(strings.TrimSuffix(rest, matched))
		if !providerIDPattern.MatchString(id) {
			errs = append(errs, fmt.Sprintf("provider variable %s has an invalid provider ID", key))
			continue
		}
		b := builders[id]
		if b == nil {
			b = &builder{fields: map[string]string{}}
			builders[id] = b
		}
		b.fields[strings.TrimPrefix(matched, "_")] = value
	}

	ids := make([]string, 0, len(builders))
	for id := range builders {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Provider, 0, len(ids))
	var warnings []string
	for _, id := range ids {
		f := builders[id].fields
		p := Provider{
			ID:          id,
			DisplayName: displayName(id),
			Discovery:   true,
			Timeout:     0,
			Headers:     map[string]string{},
		}
		if v, ok := f["DISPLAY_NAME"]; ok && strings.TrimSpace(v) != "" {
			p.DisplayName = strings.TrimSpace(v)
		}
		if len(p.DisplayName) > 80 || hasControl(p.DisplayName) {
			errs = append(errs, fmt.Sprintf("provider %s DISPLAY_NAME must be at most 80 characters", id))
			p.DisplayName = displayName(id)
		}
		if v, ok := f["ENABLED"]; ok && strings.TrimSpace(v) != "" {
			b, err := parseBool(v)
			if err != nil {
				errs = append(errs, fmt.Sprintf("provider %s ENABLED must be true or false", id))
			} else {
				p.Enabled = b
			}
		}
		p.BaseURL = strings.TrimRight(strings.TrimSpace(f["BASE_URL"]), "/")
		if p.BaseURL != "" {
			if err := validateBaseURL(p.BaseURL); err != nil {
				errs = append(errs, fmt.Sprintf("provider %s BASE_URL %s", id, err))
			}
		}
		if p.Enabled && p.BaseURL == "" {
			errs = append(errs, fmt.Sprintf("provider %s is enabled but BASE_URL is empty", id))
		}
		p.APIKey = strings.TrimSpace(f["API_KEY"])
		if strings.ContainsAny(p.APIKey, "\r\n\x00") {
			errs = append(errs, fmt.Sprintf("provider %s API_KEY contains invalid control characters", id))
			p.APIKey = ""
		}
		if v, ok := f["NO_AUTH"]; ok && strings.TrimSpace(v) != "" {
			b, err := parseBool(v)
			if err != nil {
				errs = append(errs, fmt.Sprintf("provider %s NO_AUTH must be true or false", id))
			} else {
				p.NoAuth = b
			}
		}
		if v, ok := f["DISCOVERY"]; ok && strings.TrimSpace(v) != "" {
			b, err := parseBool(v)
			if err != nil {
				errs = append(errs, fmt.Sprintf("provider %s DISCOVERY must be true or false", id))
			} else {
				p.Discovery = b
			}
		}
		if v, ok := f["TIMEOUT_SECONDS"]; ok && strings.TrimSpace(v) != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 5 || n > 3600 {
				errs = append(errs, fmt.Sprintf("provider %s TIMEOUT_SECONDS must be an integer between 5 and 3600", id))
			} else {
				p.Timeout = time.Duration(n) * time.Second
			}
		}
		if v, ok := f["MODELS"]; ok {
			models, modelWarnings := parseModels(id, v)
			p.Models = models
			warnings = append(warnings, modelWarnings...)
		}
		if v, ok := f["HEADERS"]; ok && strings.TrimSpace(v) != "" {
			headers, err := parseHeaders(v)
			if err != nil {
				errs = append(errs, fmt.Sprintf("provider %s HEADERS %s", id, err))
			} else {
				p.Headers = headers
			}
		}
		if p.Enabled && p.Status() != "ready" {
			warnings = append(warnings, fmt.Sprintf("provider %s is enabled but disabled at runtime: %s", id, p.Status()))
		}
		if p.Enabled && p.Configured() && len(p.Models) == 0 && !p.Discovery {
			warnings = append(warnings, fmt.Sprintf("provider %s has no configured models and model discovery is disabled", id))
		}
		out = append(out, p)
	}
	return out, warnings, errs
}

func parseModels(id, raw string) ([]string, []string) {
	var out []string
	seen := map[string]bool{}
	var warnings []string
	tooMany := false
	for _, part := range strings.Split(raw, ",") {
		model := strings.TrimSpace(part)
		if model == "" {
			continue
		}
		if len(model) > 200 || hasControl(model) {
			warnings = append(warnings, fmt.Sprintf("provider %s MODELS ignored an invalid model ID", id))
			continue
		}
		if !seen[model] {
			if len(out) >= 200 {
				tooMany = true
				continue
			}
			seen[model] = true
			out = append(out, model)
		}
	}
	if tooMany {
		warnings = append(warnings, fmt.Sprintf("provider %s MODELS list was truncated to 200 entries", id))
	}
	return out, warnings
}

func parseHeaders(raw string) (map[string]string, error) {
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, errors.New("must be a JSON object of string header names to string values")
	}
	if len(headers) > 20 {
		return nil, errors.New("must contain at most 20 headers")
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		if !validHeaderName(name) {
			return nil, fmt.Errorf("contains invalid header name %q", name)
		}
		switch strings.ToLower(name) {
		case "host", "content-length", "transfer-encoding", "connection", "trailer", "upgrade", "keep-alive":
			return nil, fmt.Errorf("must not set the %s header", name)
		}
		if strings.ContainsAny(value, "\r\n\x00") || len(value) > 1000 {
			return nil, fmt.Errorf("header %s has an invalid value", name)
		}
		out[name] = value
	}
	return out, nil
}

func validHeaderName(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if u.Host == "" {
		return errors.New("must include a host")
	}
	if u.User != nil {
		return errors.New("must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must not contain a query string or fragment")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("must not contain whitespace")
	}
	return nil
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, errors.New("not a boolean")
	}
}

func displayName(id string) string {
	if id == "openai" {
		return "OpenAI"
	}
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
