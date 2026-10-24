package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"libreai/internal/config"
	"libreai/internal/provider"
)

type envSaveResponse struct {
	Providers       []provider.Info `json:"providers"`
	RestartRequired bool            `json:"restart_required"`
}

func TestEnvFileEditor(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}, "test-model")

	path := filepath.Join(t.TempDir(), ".env")
	original := strings.Join([]string{
		"PROVIDER_MOCK_ENABLED=true",
		"PROVIDER_MOCK_DISPLAY_NAME=Mock Provider",
		"PROVIDER_MOCK_BASE_URL=" + env.provider.URL,
		"PROVIDER_MOCK_API_KEY=sk-super-secret",
		"PROVIDER_MOCK_MODELS=alpha,beta",
		"PROVIDER_MOCK_DISCOVERY=false",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	env.server.cfg.EnvFile = path

	resp := env.request(t, "GET", "/api/env", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/env status = %d", resp.StatusCode)
	}
	loaded := readJSON[envFileDTO](t, resp)
	if loaded.Path != path || !loaded.Exists {
		t.Fatalf("loaded env metadata = %+v", loaded)
	}
	if strings.Contains(loaded.Contents, "sk-super-secret") {
		t.Fatal("GET /api/env exposed an API key")
	}
	if !strings.Contains(loaded.Contents, envSecretPlaceholder) {
		t.Fatalf("masked contents = %q", loaded.Contents)
	}

	updated := strings.Replace(loaded.Contents, "alpha,beta", "alpha,beta,gamma", 1)
	body, err := json.Marshal(envFileRequest{Contents: updated})
	if err != nil {
		t.Fatal(err)
	}
	resp = env.request(t, "PUT", "/api/env", string(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/env status = %d", resp.StatusCode)
	}
	saved := readJSON[envSaveResponse](t, resp)
	if len(saved.Providers) != 1 || saved.Providers[0].ID != "mock" {
		t.Fatalf("providers after save = %+v", saved.Providers)
	}
	models := map[string]bool{}
	for _, model := range saved.Providers[0].Models {
		models[model.ID] = true
	}
	if !models["alpha"] || !models["beta"] || !models["gamma"] {
		t.Fatalf("models after save = %+v", saved.Providers[0].Models)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved env: %v", err)
	}
	if !strings.Contains(string(onDisk), "sk-super-secret") {
		t.Fatal("secret was not preserved when saving the placeholder")
	}
	if !strings.Contains(string(onDisk), "alpha,beta,gamma") {
		t.Fatalf("saved env = %q", onDisk)
	}

	resp = env.request(t, "PUT", "/api/env", `{"contents":"BROKEN LINE\n"}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid env status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()
	afterInvalid, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env after invalid save: %v", err)
	}
	if string(afterInvalid) != string(onDisk) {
		t.Fatal("invalid save modified the .env file")
	}
}

func TestEnvFileUnavailableWithoutPath(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {})
	resp := env.request(t, "GET", "/api/env", "", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("GET /api/env status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
	resp.Body.Close()
}

func TestMaskAndRestoreEnvSecrets(t *testing.T) {
	current := "export PROVIDER_X_API_KEY='quoted-secret'\r\n" +
		"PROVIDER_X_HEADERS={\"X-Team\":\"secret-header\"}\r\n" +
		"PROVIDER_X_MODELS=a,b\r\n"
	masked := maskEnvSecrets(current)
	if strings.Contains(masked, "quoted-secret") || strings.Contains(masked, "secret-header") {
		t.Fatalf("masked contents leaked a secret: %q", masked)
	}
	if !strings.Contains(masked, "PROVIDER_X_MODELS=a,b") {
		t.Fatalf("masked contents changed a non-secret value: %q", masked)
	}
	if restored := restoreEnvSecrets(masked, current); restored != current {
		t.Fatalf("restored contents = %q, want %q", restored, current)
	}
}

func TestConfigNeedsRestart(t *testing.T) {
	base := config.Config{Host: "127.0.0.1", Port: 8080, RetentionDays: 30}
	if configNeedsRestart(base, base) {
		t.Fatal("identical configs reported a restart")
	}
	changed := base
	changed.Providers = []config.Provider{{ID: "mock"}}
	if configNeedsRestart(base, changed) {
		t.Fatal("provider-only change reported a restart")
	}
	changed.RetentionDays = 7
	if !configNeedsRestart(base, changed) {
		t.Fatal("retention change did not report a restart")
	}
}
