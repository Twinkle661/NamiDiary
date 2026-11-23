package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type App struct {
	Port     int    `yaml:"port"`
	APIKey   string `yaml:"api_key"`
	Debug    bool   `yaml:"debug"`
	DataDir  string `yaml:"data_dir"`
	Timezone string `yaml:"timezone"`
}
type MCP struct {
	AllowedHosts   []string `yaml:"allowed_hosts"`
	AllowedOrigins []string `yaml:"allowed_origins"`
}
type Embedding struct {
	BaseURL     string        `yaml:"base_url"`
	Model       string        `yaml:"model"`
	APIKey      string        `yaml:"api_key"`
	Dimensions  int           `yaml:"dimensions"`
	BatchSize   int           `yaml:"batch_size"`
	Concurrency int           `yaml:"concurrency"`
	Timeout     time.Duration `yaml:"timeout"`
}
type Diary struct {
	SearchLimit          int           `yaml:"search_limit"`
	LatestMax            int           `yaml:"latest_max"`
	SimilarLimit         int           `yaml:"similar_limit"`
	SimilarQueryMaxChars int           `yaml:"similar_query_max_chars"`
	PatchWindow          time.Duration `yaml:"patch_window"`
	FutureWindow         time.Duration `yaml:"future_window"`
	ActiveNormalize      bool          `yaml:"active_normalize"`
	Embedding            Embedding     `yaml:"embedding"`
}
type Limit struct {
	MaxTopics  int `yaml:"max_topics" json:"max_topics,omitempty"`
	MaxEntries int `yaml:"max_entries" json:"max_entries"`
}
type Limits struct {
	Profile    Limit `yaml:"profile" json:"profile"`
	Preference Limit `yaml:"preference" json:"preference"`
	People     Limit `yaml:"people" json:"people"`
	Area       Limit `yaml:"area" json:"area"`
	Project    Limit `yaml:"project" json:"project"`
}

func (l Limits) For(kind, topic string) Limit {
	switch kind {
	case "people":
		return l.People
	case "area":
		return l.Area
	case "project":
		return l.Project
	default:
		if topic == "profile" {
			return l.Profile
		}
		return l.Preference
	}
}

type Override struct {
	Model       string   `yaml:"model"`
	Temperature *float64 `yaml:"temperature"`
}
type LLM struct {
	BaseURL     string        `yaml:"base_url"`
	Model       string        `yaml:"model"`
	APIKey      string        `yaml:"api_key"`
	Temperature float64       `yaml:"temperature"`
	MaxTokens   int           `yaml:"max_tokens"`
	Timeout     time.Duration `yaml:"timeout"`
	JSONMode    bool          `yaml:"json_mode"`
	MaxAttempts int           `yaml:"max_attempts"`
	Agents      struct {
		Dream   Override `yaml:"dream"`
		Archive Override `yaml:"archive"`
		Compact Override `yaml:"compact"`
	} `yaml:"agents"`
}
type Retention struct {
	Policy string        `yaml:"policy"`
	MaxAge time.Duration `yaml:"max_age"`
}
type Memory struct {
	EntryMaxChars int    `yaml:"entry_max_chars"`
	Language      string `yaml:"language"`
	Names         struct {
		Agent string `yaml:"agent"`
		User  string `yaml:"user"`
	} `yaml:"names"`
	Limits Limits `yaml:"limits"`
	Dream  struct {
		ContextDiaries int `yaml:"context_diaries"`
	} `yaml:"dream"`
	Maintenance struct {
		Interval time.Duration `yaml:"interval"`
	} `yaml:"maintenance"`
	Backup struct {
		Retention Retention `yaml:"retention"`
	} `yaml:"backup"`
	LLM        LLM    `yaml:"llm"`
	PromptsDir string `yaml:"prompts_dir"`
}
type Config struct {
	App     App   `yaml:"app"`
	MCP     MCP   `yaml:"mcp"`
	Diary   Diary `yaml:"diary"`
	Context struct {
		Static struct {
			DefaultRecent int `yaml:"default_recent"`
			MaxRecent     int `yaml:"max_recent"`
		} `yaml:"static"`
	} `yaml:"context"`
	Memory Memory `yaml:"memory"`
}

func Defaults() Config {
	var c Config
	c.App = App{Port: 8188, DataDir: "./data", Timezone: "UTC"}
	c.MCP = MCP{[]string{"localhost", "127.0.0.1", "[::1]"}, []string{"localhost", "127.0.0.1", "[::1]"}}
	c.Diary = Diary{SearchLimit: 20, LatestMax: 31, SimilarLimit: 15, SimilarQueryMaxChars: 4000, PatchWindow: 24 * time.Hour, FutureWindow: 24 * time.Hour, ActiveNormalize: true, Embedding: Embedding{BaseURL: "https://openrouter.ai/api/v1", Model: "qwen/qwen3-embedding-8b", Dimensions: 1024, BatchSize: 64, Concurrency: 4, Timeout: 30 * time.Second}}
	c.Context.Static.DefaultRecent = 7
	c.Context.Static.MaxRecent = 60
	c.Memory.EntryMaxChars = 200
	c.Memory.Limits = Limits{Limit{MaxEntries: 30}, Limit{MaxEntries: 30}, Limit{20, 20}, Limit{15, 20}, Limit{7, 15}}
	c.Memory.Dream.ContextDiaries = 3
	c.Memory.Backup.Retention = Retention{"max_age", 168 * time.Hour}
	c.Memory.LLM = LLM{BaseURL: "https://openrouter.ai/api/v1", Model: "google/gemini-2.5-flash", Temperature: 0.2, MaxTokens: 8192, Timeout: 180 * time.Second, JSONMode: true, MaxAttempts: 3}
	return c
}
func Load() (Config, error) {
	c := Defaults()
	originalEnv := map[string]bool{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		originalEnv[k] = true
	}
	var errs []string
	envPath := os.Getenv("NAMIDIARY_ENV_FILE")
	if envPath == "" {
		envPath = ".env"
	}
	b, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, err.Error())
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			errs = append(errs, "invalid .env assignment")
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if _, ok := os.LookupEnv(k); !ok {
			if err := os.Setenv(k, v); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	path := os.Getenv("NAMIDIARY_CONFIG")
	if path == "" {
		path = "config.yml"
	}
	b, err = os.ReadFile(path)
	if err == nil {
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if e := dec.Decode(&c); e != nil {
			errs = append(errs, e.Error())
		}
	} else if !os.IsNotExist(err) {
		errs = append(errs, err.Error())
	}
	walk(reflect.ValueOf(&c).Elem(), nil, &errs)
	for key, dst := range map[string]*string{"NAMIDIARY_API_KEY": &c.App.APIKey, "NAMIDIARY_EMBEDDING_API_KEY": &c.Diary.Embedding.APIKey, "NAMIDIARY_LLM_API_KEY": &c.Memory.LLM.APIKey} {
		longKey := map[string]string{"NAMIDIARY_API_KEY": "NAMIDIARY_APP__API_KEY", "NAMIDIARY_EMBEDDING_API_KEY": "NAMIDIARY_DIARY__EMBEDDING__API_KEY", "NAMIDIARY_LLM_API_KEY": "NAMIDIARY_MEMORY__LLM__API_KEY"}[key]
		if v, ok := os.LookupEnv(key); ok && (originalEnv[key] || !originalEnv[longKey]) {
			*dst = v
		}
	}
	errs = append(errs, c.Validate()...)
	if len(errs) > 0 {
		return c, fmt.Errorf("CONFIG_INVALID: %s", strings.Join(errs, "; "))
	}
	return c, nil
}
func walk(v reflect.Value, path []string, errs *[]string) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		p := append(append([]string{}, path...), t.Field(i).Tag.Get("yaml"))
		key := "NAMIDIARY_" + strings.ToUpper(strings.Join(p, "__"))
		if f.Kind() == reflect.Struct {
			walk(f, p, errs)
			continue
		}
		raw, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		var err error
		if f.Type() == reflect.TypeOf(time.Duration(0)) {
			var d time.Duration
			d, err = time.ParseDuration(raw)
			if err == nil {
				f.SetInt(int64(d))
			}
		} else {
			switch f.Kind() {
			case reflect.String:
				f.SetString(raw)
			case reflect.Bool:
				var n bool
				n, err = strconv.ParseBool(raw)
				if err == nil {
					f.SetBool(n)
				}
			case reflect.Int:
				var n int64
				n, err = strconv.ParseInt(raw, 10, 64)
				if err == nil {
					f.SetInt(n)
				}
			case reflect.Float64:
				var n float64
				n, err = strconv.ParseFloat(raw, 64)
				if err == nil {
					f.SetFloat(n)
				}
			case reflect.Ptr:
				var n float64
				n, err = strconv.ParseFloat(raw, 64)
				if err == nil {
					f.Set(reflect.ValueOf(&n))
				}
			case reflect.Slice:
				xs := []string{}
				for _, s := range strings.Split(raw, ",") {
					if s = strings.TrimSpace(s); s != "" {
						xs = append(xs, s)
					}
				}
				f.Set(reflect.ValueOf(xs))
			}
		}
		if err != nil {
			*errs = append(*errs, key+": invalid value")
		}
	}
}
func (c Config) Validate() []string {
	var e []string
	check := func(ok bool, s string) {
		if !ok {
			e = append(e, s)
		}
	}
	check(c.App.Port > 0 && c.App.Port <= 65535, "app.port must be in 1..65535")
	check(c.App.APIKey != "", "app.api_key is required")
	check(c.App.DataDir != "", "app.data_dir is required")
	_, err := time.LoadLocation(c.App.Timezone)
	check(err == nil, "app.timezone is invalid")
	for k, v := range map[string]int{"diary.search_limit": c.Diary.SearchLimit, "diary.latest_max": c.Diary.LatestMax, "diary.similar_limit": c.Diary.SimilarLimit, "diary.similar_query_max_chars": c.Diary.SimilarQueryMaxChars, "diary.embedding.dimensions": c.Diary.Embedding.Dimensions, "diary.embedding.batch_size": c.Diary.Embedding.BatchSize, "diary.embedding.concurrency": c.Diary.Embedding.Concurrency, "context.static.max_recent": c.Context.Static.MaxRecent, "memory.dream.context_diaries": c.Memory.Dream.ContextDiaries, "memory.llm.max_tokens": c.Memory.LLM.MaxTokens, "memory.llm.max_attempts": c.Memory.LLM.MaxAttempts} {
		check(v > 0, k+" must be positive")
	}
	check(c.Context.Static.DefaultRecent >= 0 && c.Context.Static.DefaultRecent <= c.Context.Static.MaxRecent, "context.static.default_recent out of bounds")
	check(c.Memory.EntryMaxChars >= 20 && c.Memory.EntryMaxChars <= 2000, "memory.entry_max_chars must be in 20..2000")
	for _, k := range []string{"profile", "preference", "people", "area", "project"} {
		l := c.Memory.Limits.For(k, k)
		check(l.MaxEntries > 0, "memory.limits."+k+".max_entries must be positive")
		if k != "profile" && k != "preference" {
			check(l.MaxTopics > 0, "memory.limits."+k+".max_topics must be positive")
		}
	}
	for k, v := range map[string]time.Duration{"diary.patch_window": c.Diary.PatchWindow, "diary.future_window": c.Diary.FutureWindow, "diary.embedding.timeout": c.Diary.Embedding.Timeout, "memory.llm.timeout": c.Memory.LLM.Timeout, "memory.backup.retention.max_age": c.Memory.Backup.Retention.MaxAge} {
		check(v > 0, k+" must be positive")
	}
	check(c.Memory.Maintenance.Interval >= 0, "memory.maintenance.interval must be nonnegative")
	check(c.Memory.Backup.Retention.Policy == "max_age", "unsupported retention policy")
	for k, v := range map[string]string{"diary.embedding.base_url": c.Diary.Embedding.BaseURL, "memory.llm.base_url": c.Memory.LLM.BaseURL} {
		u, err := url.Parse(v)
		check(err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http"), k+" must be an absolute HTTP URL")
	}
	check(c.Diary.Embedding.Model != "" && c.Diary.Embedding.APIKey != "", "embedding model and API key are required")
	check(c.Memory.LLM.Model != "" && c.Memory.LLM.APIKey != "", "LLM model and API key are required")
	check(c.Memory.LLM.Temperature >= 0 && c.Memory.LLM.Temperature <= 2, "memory.llm.temperature must be in 0..2")
	for _, o := range []Override{c.Memory.LLM.Agents.Dream, c.Memory.LLM.Agents.Archive, c.Memory.LLM.Agents.Compact} {
		check(o.Temperature == nil || (*o.Temperature >= 0 && *o.Temperature <= 2), "agent temperature must be in 0..2")
	}
	return e
}
func (c Config) Masked() string {
	c.App.APIKey = "***"
	c.Diary.Embedding.APIKey = "***"
	c.Memory.LLM.APIKey = "***"
	b, _ := json.Marshal(c)
	return string(b)
}
