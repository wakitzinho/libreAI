package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, warnings, err := FromMap(map[string]string{})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 8080 {
		t.Fatalf("unexpected listen address: %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.DatabasePath != "./data/libreai.db" {
		t.Fatalf("unexpected database path: %q", cfg.DatabasePath)
	}
	if cfg.RetentionDays != 30 {
		t.Fatalf("retention days = %d, want 30", cfg.RetentionDays)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"bad host", map[string]string{"HOST": "127.0.0.1/evil"}, "HOST"},
		{"bad port", map[string]string{"PORT": "70000"}, "PORT"},
		{"empty database", map[string]string{"DATABASE_PATH": "  "}, "DATABASE_PATH"},
		{"bad retention", map[string]string{"CHAT_RETENTION_DAYS": "0"}, "CHAT_RETENTION_DAYS"},
		{"bad timeout", map[string]string{"UPSTREAM_TIMEOUT_SECONDS": "1"}, "UPSTREAM_TIMEOUT_SECONDS"},
		{"bad concurrency", map[string]string{"MAX_CONCURRENT_GENERATIONS": "100"}, "MAX_CONCURRENT_GENERATIONS"},
		{"bad log level", map[string]string{"LOG_LEVEL": "verbose"}, "LOG_LEVEL"},
		{"invalid provider id", map[string]string{"PROVIDER_BAD-ID_ENABLED": "true"}, "invalid provider ID"},
		{"enabled without url", map[string]string{"PROVIDER_TEST_ENABLED": "true"}, "BASE_URL"},
		{"url credentials", map[string]string{
			"PROVIDER_TEST_ENABLED":  "true",
			"PROVIDER_TEST_BASE_URL": "https://user:pass@example.com/v1",
			"PROVIDER_TEST_API_KEY":  "key",
		}, "must not contain credentials"},
		{"bad headers", map[string]string{
			"PROVIDER_TEST_ENABLED":  "true",
			"PROVIDER_TEST_BASE_URL": "https://example.com/v1",
			"PROVIDER_TEST_API_KEY":  "key",
			"PROVIDER_TEST_HEADERS":  `{"Host":"evil"}`,
		}, "must not set the Host header"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := FromMap(tt.values)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestSearchConfiguration(t *testing.T) {
	cfg, warnings, err := FromMap(map[string]string{})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.SearchEnabled || cfg.SearchConfigured() {
		t.Fatal("web search should be disabled by default")
	}
	if cfg.SearchBaseURL != DefaultSearchBaseURL || cfg.SearchMaxResults != 5 ||
		cfg.SearchTimeout != 10*time.Second || cfg.SearchMaxBytes != 12000 {
		t.Fatalf("unexpected search defaults: %+v", cfg)
	}

	cfg, warnings, err = FromMap(map[string]string{
		"SEARCH_ENABLED":         "true",
		"SEARCH_API_KEY":         "pplx-test",
		"SEARCH_MAX_RESULTS":     "8",
		"SEARCH_TIMEOUT_SECONDS": "20",
		"SEARCH_MAX_BYTES":       "20000",
	})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("FromMap = %v, %v", err, warnings)
	}
	if !cfg.SearchConfigured() || cfg.SearchMaxResults != 8 ||
		cfg.SearchTimeout != 20*time.Second || cfg.SearchMaxBytes != 20000 {
		t.Fatalf("search config = %+v", cfg)
	}

	if _, _, err := FromMap(map[string]string{"SEARCH_ENABLED": "true"}); err == nil ||
		!strings.Contains(err.Error(), "SEARCH_API_KEY") {
		t.Fatalf("missing key error = %v", err)
	}

	_, warnings, err = FromMap(map[string]string{"SEARCH_API_KEY": "pplx-test"})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "SEARCH_ENABLED") {
		t.Fatalf("warnings = %v", warnings)
	}

	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"bad base url", map[string]string{"SEARCH_BASE_URL": "ftp://example.com"}, "SEARCH_BASE_URL"},
		{"bad max results", map[string]string{"SEARCH_MAX_RESULTS": "50"}, "SEARCH_MAX_RESULTS"},
		{"bad timeout", map[string]string{"SEARCH_TIMEOUT_SECONDS": "1"}, "SEARCH_TIMEOUT_SECONDS"},
		{"bad max bytes", map[string]string{"SEARCH_MAX_BYTES": "10"}, "SEARCH_MAX_BYTES"},
		{"bad enabled", map[string]string{"SEARCH_ENABLED": "maybe"}, "SEARCH_ENABLED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := FromMap(tt.values)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestProviderConfiguration(t *testing.T) {
	cfg, warnings, err := FromMap(map[string]string{
		"PROVIDER_GROQ_ENABLED":         "true",
		"PROVIDER_GROQ_DISPLAY_NAME":    "Groq Cloud",
		"PROVIDER_GROQ_BASE_URL":        "https://api.groq.example/v1/",
		"PROVIDER_GROQ_API_KEY":         "sk-test-secret",
		"PROVIDER_GROQ_MODELS":          "llama-3.1-8b, mixtral,llama-3.1-8b",
		"PROVIDER_GROQ_DISCOVERY":       "false",
		"PROVIDER_GROQ_TIMEOUT_SECONDS": "42",
		"PROVIDER_GROQ_HEADERS":         `{"X-Tenant":"personal"}`,
		"PROVIDER_OFF_ENABLED":          "false",
	})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(cfg.Providers))
	}
	p, ok := cfg.ProviderByID("groq")
	if !ok {
		t.Fatal("groq provider missing")
	}
	if p.DisplayName != "Groq Cloud" || !p.Enabled || !p.Configured() || p.Status() != "ready" {
		t.Fatalf("unexpected provider: %+v", p)
	}
	if p.BaseURL != "https://api.groq.example/v1" {
		t.Fatalf("base URL = %q", p.BaseURL)
	}
	if p.Timeout != 42*time.Second {
		t.Fatalf("timeout = %s", p.Timeout)
	}
	if p.Discovery {
		t.Fatal("discovery should be disabled")
	}
	if len(p.Models) != 2 || p.Models[0] != "llama-3.1-8b" || p.Models[1] != "mixtral" {
		t.Fatalf("models = %#v", p.Models)
	}
	if p.Headers["X-Tenant"] != "personal" {
		t.Fatalf("headers = %#v", p.Headers)
	}

	rendered := p.String()
	if strings.Contains(rendered, "sk-test-secret") || strings.Contains(rendered, "personal") {
		t.Fatalf("String leaked secrets: %s", rendered)
	}

	off, ok := cfg.ProviderByID("off")
	if !ok || off.Status() != "disabled" || off.Configured() {
		t.Fatalf("disabled provider = %+v", off)
	}
	// The disabled provider is expected, but a configured provider without a
	// key gets a warning rather than an error.
	_ = warnings
}

func TestOpenRouterAPIKeyAlias(t *testing.T) {
	cfg, _, err := FromMap(map[string]string{
		"OPENROUTER_API_KEY": "sk-or-test",
	})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	p, ok := cfg.ProviderByID("openrouter")
	if !ok {
		t.Fatal("openrouter provider missing")
	}
	if !p.Enabled || !p.Configured() || p.Status() != "ready" {
		t.Fatalf("unexpected provider: %+v", p)
	}
	if p.DisplayName != "OpenRouter" {
		t.Fatalf("display name = %q", p.DisplayName)
	}
	if p.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("base URL = %q", p.BaseURL)
	}
	if p.APIKey != "sk-or-test" {
		t.Fatal("API key was not applied")
	}
	if strings.Contains(p.String(), "sk-or-test") {
		t.Fatalf("String leaked the API key: %s", p.String())
	}
}

func TestOpenRouterAliasDoesNotOverrideExplicitConfig(t *testing.T) {
	cfg, _, err := FromMap(map[string]string{
		"OPENROUTER_API_KEY":               "alias-key",
		"PROVIDER_OPENROUTER_ENABLED":      "false",
		"PROVIDER_OPENROUTER_DISPLAY_NAME": "Custom OpenRouter",
		"PROVIDER_OPENROUTER_BASE_URL":     "https://proxy.example/v1",
		"PROVIDER_OPENROUTER_API_KEY":      "explicit-key",
	})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	p, ok := cfg.ProviderByID("openrouter")
	if !ok {
		t.Fatal("openrouter provider missing")
	}
	if p.Enabled {
		t.Fatal("explicit ENABLED=false was overridden")
	}
	if p.DisplayName != "Custom OpenRouter" || p.BaseURL != "https://proxy.example/v1" {
		t.Fatalf("explicit fields were overridden: %+v", p)
	}
	if p.APIKey != "explicit-key" {
		t.Fatal("explicit API key was overridden")
	}
}

func TestOpenRouterAliasInEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("OPENROUTER_API_KEY=\"file-key\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENROUTER_API_KEY", "")

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, ok := cfg.ProviderByID("openrouter")
	if !ok {
		t.Fatal("openrouter provider missing")
	}
	if !p.Enabled || p.APIKey != "file-key" {
		t.Fatalf("unexpected provider: %+v", p)
	}
}

func TestOpenRouterAliasEnvironmentPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "PROVIDER_OPENROUTER_ENABLED=true\nPROVIDER_OPENROUTER_API_KEY=file-key\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENROUTER_API_KEY", "env-key")

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, ok := cfg.ProviderByID("openrouter")
	if !ok {
		t.Fatal("openrouter provider missing")
	}
	if p.APIKey != "env-key" {
		t.Fatalf("process-environment alias did not override .env: %q", p.APIKey)
	}
}

func TestMissingKeyWarnsButLoads(t *testing.T) {
	cfg, warnings, err := FromMap(map[string]string{
		"PROVIDER_TEST_ENABLED":  "true",
		"PROVIDER_TEST_BASE_URL": "https://example.com/v1",
	})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	p, ok := cfg.ProviderByID("test")
	if !ok {
		t.Fatal("provider missing")
	}
	if p.Status() != "api_key_missing" || p.Configured() {
		t.Fatalf("status = %q configured = %v", p.Status(), p.Configured())
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "api_key_missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected api_key_missing warning, got %v", warnings)
	}
}

func TestNoAuthProvider(t *testing.T) {
	cfg, _, err := FromMap(map[string]string{
		"PROVIDER_LOCAL_ENABLED":  "true",
		"PROVIDER_LOCAL_BASE_URL": "http://127.0.0.1:11434/v1",
		"PROVIDER_LOCAL_NO_AUTH":  "true",
		"PROVIDER_LOCAL_MODELS":   "local-model",
	})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	p, _ := cfg.ProviderByID("local")
	if !p.Configured() || p.Status() != "ready" || p.APIKey != "" {
		t.Fatalf("unexpected no-auth provider: %+v", p)
	}
}

func TestEnvFileAndEnvironmentPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# comment
export HOST=0.0.0.0
PORT="9090"
CHAT_RETENTION_DAYS=7
PROVIDER_TEST_ENABLED=true
PROVIDER_TEST_BASE_URL=https://example.com/v1 # inline comment
PROVIDER_TEST_API_KEY='sk-from-file'
PROVIDER_TEST_MODELS="alpha,beta"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", "9191")
	t.Setenv("PROVIDER_TEST_API_KEY", "sk-from-env")

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 9191 || cfg.RetentionDays != 7 {
		t.Fatalf("unexpected config: host=%s port=%d retention=%d", cfg.Host, cfg.Port, cfg.RetentionDays)
	}
	p, ok := cfg.ProviderByID("test")
	if !ok {
		t.Fatal("provider missing")
	}
	if p.BaseURL != "https://example.com/v1" {
		t.Fatalf("base URL = %q", p.BaseURL)
	}
	if p.APIKey != "sk-from-env" {
		t.Fatalf("environment did not override .env API key")
	}
	if len(p.Models) != 2 || p.Models[1] != "beta" {
		t.Fatalf("models = %#v", p.Models)
	}
}

func TestMissingEnvFileIsNotAnError(t *testing.T) {
	cfg, _, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port = %d", cfg.Port)
	}
}

func TestInsecureEnvFileWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PORT=8081\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "chmod 600") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected permission warning, got %v", warnings)
	}
}

func TestGroupReadableEnvFileIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PORT=8081\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, w := range warnings {
		if strings.Contains(w, "accessible by other users") {
			t.Fatalf("unexpected permission warning for 0640: %v", warnings)
		}
	}
}
