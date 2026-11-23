package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadPrecedenceAndOverrides(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "config.yml")
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(yaml, []byte("app:\n  port: 7000\n  api_key: yaml-secret\ndiary:\n  embedding:\n    api_key: yaml-embedding\nmemory:\n  llm:\n    api_key: yaml-llm\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("# secrets\nNAMIDIARY_APP__PORT=7100\nNAMIDIARY_API_KEY='env-file-secret'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMIDIARY_CONFIG", yaml)
	t.Setenv("NAMIDIARY_ENV_FILE", env)
	t.Setenv("NAMIDIARY_APP__PORT", "7200")
	t.Setenv("NAMIDIARY_API_KEY", "process-secret")
	t.Setenv("NAMIDIARY_DIARY__PATCH_WINDOW", "90m")
	t.Setenv("NAMIDIARY_MCP__ALLOWED_HOSTS", "example.test, localhost")
	t.Setenv("NAMIDIARY_MEMORY__LLM__AGENTS__DREAM__TEMPERATURE", "0.7")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Port != 7200 || c.App.APIKey != "process-secret" || c.Diary.PatchWindow != 90*time.Minute || c.MCP.AllowedHosts[0] != "example.test" || *c.Memory.LLM.Agents.Dream.Temperature != 0.7 {
		t.Fatalf("unexpected config: %s", c.Masked())
	}
	masked := c.Masked()
	for _, secret := range []string{"process-secret", "yaml-embedding", "yaml-llm"} {
		if strings.Contains(masked, secret) {
			t.Fatal("secret leaked")
		}
	}
	// Remove a process variable to exercise .env over YAML; restore it after the test.
	if err := os.Unsetenv("NAMIDIARY_APP__PORT"); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || c.App.Port != 7100 {
		t.Fatalf(".env precedence: %v %d", err, c.App.Port)
	}
}
func TestAggregateValidation(t *testing.T) {
	c := Defaults()
	c.App.Port = -1
	c.App.Timezone = "Invalid/Zone"
	c.Memory.EntryMaxChars = 1
	c.Memory.LLM.BaseURL = "relative"
	e := strings.Join(c.Validate(), " ")
	for _, field := range []string{"app.port", "app.api_key", "app.timezone", "entry_max_chars", "base_url"} {
		if !strings.Contains(e, field) {
			t.Errorf("missing %s: %s", field, e)
		}
	}
}
func TestYAMLDurationAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NAMIDIARY_CONFIG", filepath.Join(dir, "missing.yml"))
	t.Setenv("NAMIDIARY_ENV_FILE", filepath.Join(dir, "missing.env"))
	t.Setenv("NAMIDIARY_API_KEY", "api")
	t.Setenv("NAMIDIARY_EMBEDDING_API_KEY", "embedding")
	t.Setenv("NAMIDIARY_LLM_API_KEY", "llm")
	c, err := Load()
	if err != nil || c.App.Port != 8188 {
		t.Fatalf("defaults %v", err)
	}
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("diary:\n  patch_window: 90m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMIDIARY_CONFIG", path)
	c, err = Load()
	if err != nil || c.Diary.PatchWindow != 90*time.Minute {
		t.Fatalf("duration %v", err)
	}
}

func TestProcessLongSecretBeatsDotenvShortAlias(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("NAMIDIARY_API_KEY=dotenv-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMIDIARY_ENV_FILE", env)
	t.Setenv("NAMIDIARY_CONFIG", filepath.Join(dir, "missing"))
	t.Setenv("NAMIDIARY_API_KEY", "cleanup-value")
	if err := os.Unsetenv("NAMIDIARY_API_KEY"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMIDIARY_APP__API_KEY", "process-long-secret")
	t.Setenv("NAMIDIARY_EMBEDDING_API_KEY", "embedding")
	t.Setenv("NAMIDIARY_LLM_API_KEY", "llm")
	c, err := Load()
	if err != nil || c.App.APIKey != "process-long-secret" {
		t.Fatalf("precedence %v %s", err, c.Masked())
	}
}
