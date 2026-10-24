package config

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func requireNumericConfigError(t *testing.T, err error, parts ...string) {
	t.Helper()
	if _, ok := errors.AsType[*ConfigError](err); !ok {
		t.Fatalf("error = %v, want *ConfigError", err)
	}
	if strings.ContainsAny(err.Error(), "\r\n") {
		t.Fatalf("error must be one line: %q", err.Error())
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error = %q, want %q", err.Error(), part)
		}
	}
	if strings.Contains(err.Error(), "do-not-echo") {
		t.Errorf("error disclosed raw input: %q", err.Error())
	}
}

func TestParseIntValue(t *testing.T) {
	bound := math.Ldexp(1, strconv.IntSize-1)
	largestFloat := math.Trunc(math.Nextafter(bound, 0))
	cases := []struct {
		name  string
		value any
		want  int
	}{
		{"int", 9, 9},
		{"int64", int64(9), 9},
		{"float", 8.0, 8},
		{"string", "  +009 \n", 9},
		{"zero", "0", 0},
		{"negative", "-9", -9},
		{"min-string", strconv.Itoa(math.MinInt), math.MinInt},
		{"max-string", strconv.Itoa(math.MaxInt), math.MaxInt},
		{"min-float", -bound, math.MinInt},
		{"largest-float", largestFloat, int(largestFloat)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIntValue("llm.max_tokens", tc.value)
			if err != nil || got != tc.want {
				t.Fatalf("ParseIntValue(%v) = %d, %v; want %d", tc.value, got, err, tc.want)
			}
		})
	}
	invalid := []any{
		nil, true, []int{9}, map[string]any{"api_key": "do-not-echo"}, int32(9),
		"", " ", "bad", "9junk", "9.5", "9 1", "1e2", "0x10",
		"999999999999999999999999999", 8.5, math.NaN(), math.Inf(1), math.Inf(-1),
		bound, math.Nextafter(-bound, math.Inf(-1)),
	}
	if strconv.IntSize == 32 {
		invalid = append(invalid, int64(math.MaxInt32)+1, int64(math.MinInt32)-1)
	}
	for _, value := range invalid {
		_, err := ParseIntValue("llm.max_tokens", value)
		requireNumericConfigError(t, err, "llm.max_tokens")
	}
}

func TestParseFloatValue(t *testing.T) {
	cases := []struct {
		value any
		want  float64
	}{
		{9, 9}, {int64(9), 9}, {0.5, 0.5}, {" \t5e-1\n", 0.5},
		{"0", 0}, {"-2.5", -2.5}, {math.MaxFloat64, math.MaxFloat64},
	}
	for _, tc := range cases {
		got, err := ParseFloatValue("llm.temperature", tc.value)
		if err != nil || got != tc.want {
			t.Fatalf("ParseFloatValue(%v) = %v, %v; want %v", tc.value, got, err, tc.want)
		}
	}
	for _, value := range []any{
		nil, false, []float64{0.5}, map[string]any{"api_key": "do-not-echo"}, float32(0.5),
		"", " ", "bad", "0.5junk", "0.5 1", "1e9999",
		"NaN", "Inf", "-Inf", math.NaN(), math.Inf(1), math.Inf(-1),
	} {
		_, err := ParseFloatValue("llm.temperature", value)
		requireNumericConfigError(t, err, "llm.temperature")
	}
}

func TestNormalizeLLMNumericSource(t *testing.T) {
	input := map[string]any{
		"llm_max_tokens": " 9 ",
		"llm":            map[string]any{"temperature": "0.5", "timeout_s": int64(3), "model": "keep"},
		"agents": map[string]any{
			"unused": map[string]any{"max_tokens": 8.0, "temperature": "1e-1", "model": 123},
		},
		"embedding": map[string]any{"timeout_s": "unchanged"},
	}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeLLMNumericSource(input, "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"llm_max_tokens":  9,
		"llm_temperature": 0.5,
		"llm_timeout_s":   3.0,
		"llm":             map[string]any{"model": "keep"},
		"agents": map[string]any{
			"unused": map[string]any{"max_tokens": 8, "temperature": 0.1, "model": 123},
		},
		"embedding": map[string]any{"timeout_s": "unchanged"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalized source = %#v, want %#v", got, want)
	}
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("normalizer mutated its input: before=%s, after=%s", before, after)
	}
}

func TestNormalizeLLMNumericSourceErrors(t *testing.T) {
	for _, field := range []string{"max_tokens", "temperature", "timeout_s"} {
		t.Run(field, func(t *testing.T) {
			_, err := normalizeLLMNumericSource(map[string]any{
				"llm_" + field: 1,
				"llm":          map[string]any{field: 1},
			}, "duplicate.toml")
			requireNumericConfigError(t, err, "duplicate.toml", field)
			for _, input := range []map[string]any{
				{"llm_" + field: "bad"},
				{"llm": map[string]any{field: nil}},
				{"agents": map[string]any{"unused": map[string]any{"provider": "none", field: "bad"}}},
			} {
				_, err := normalizeLLMNumericSource(input, "invalid.toml")
				requireNumericConfigError(t, err, "invalid.toml", field)
			}
		})
	}
	_, err := normalizeLLMNumericSource(map[string]any{
		"agents": map[string]any{
			"z": map[string]any{"temperature": false},
			"a": map[string]any{"temperature": false},
		},
	}, "ordered.toml")
	requireNumericConfigError(t, err, "agents.a.temperature")
}

func TestNormalizeLLMNumericSourceEmpty(t *testing.T) {
	for _, input := range []map[string]any{nil, {}, {"agents": nil}, {"llm": "legacy-ignored"}} {
		if _, err := normalizeLLMNumericSource(input, "empty.toml"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ParseIntValue("source\nname: llm.max_tokens", map[string]any{"api_key": "do-not-echo"})
	requireNumericConfigError(t, err, "llm.max_tokens")
}

func isolateNumericLoad(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, key := range []string{"LADYM_LLM_MAX_TOKENS", "LADYM_LLM_TEMPERATURE", "LADYM_LLM_TIMEOUT_S"} {
		t.Setenv(key, "")
	}
}

func writeNumericFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLLMNumericFromFile(t *testing.T) {
	for _, field := range []string{"max_tokens", "temperature", "timeout_s"} {
		for _, body := range []string{
			"llm_" + field + " = \"bad\"\n",
			"[llm]\n" + field + " = \"bad\"\n",
			"[agents.unused]\nprovider = \"none\"\n" + field + " = \"bad\"\n",
			"llm_" + field + " = 1\n[llm]\n" + field + " = 1\n",
		} {
			path := writeToml(t, body)
			cfg, err := FromFile(path)
			requireNumericConfigError(t, err, path, field)
			if cfg != nil {
				t.Error("invalid source returned a configuration")
			}
		}
	}
}

func TestLLMNumericEachSourceValidatedBeforeOverrides(t *testing.T) {
	cases := []struct {
		name, global, project, explicit, env, source string
		cli                                          map[string]any
	}{
		{name: "global", global: `llm_max_tokens = "bad"`, project: "[llm]\nmax_tokens = 7", source: "config.toml"},
		{name: "project", project: `[llm]` + "\n" + `max_tokens = "bad"`, explicit: "llm_max_tokens = 7", source: "ladym.toml"},
		{name: "explicit", explicit: `llm_max_tokens = "bad"`, env: "7", source: "explicit.toml"},
		{name: "env", env: "bad", cli: map[string]any{"llm_max_tokens": 7}, source: "LADYM_LLM_MAX_TOKENS"},
		{name: "cli", cli: map[string]any{"llm_max_tokens": "bad"}, source: "<cli-overrides>"},
		{name: "unused-op", global: "[agents.unused]\nmax_tokens = \"bad\"", project: "[agents.unused]\nmax_tokens = 7", source: "config.toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateNumericLoad(t)
			if tc.global != "" {
				writeNumericFile(t, filepath.Join(HomeDir(), "config.toml"), tc.global)
			}
			if tc.project != "" {
				writeNumericFile(t, "ladym.toml", tc.project)
			}
			explicit := ""
			if tc.explicit != "" {
				explicit = filepath.Join(t.TempDir(), "explicit.toml")
				writeNumericFile(t, explicit, tc.explicit)
			}
			t.Setenv("LADYM_LLM_MAX_TOKENS", tc.env)
			cfg, err := Load(explicit, tc.cli)
			requireNumericConfigError(t, err, tc.source, "max_tokens")
			if cfg != nil {
				t.Error("invalid source returned a configuration")
			}
		})
	}
}

func TestLLMNumericCrossSourceAliasesAndPrecedence(t *testing.T) {
	isolateNumericLoad(t)
	writeNumericFile(t, filepath.Join(HomeDir(), "config.toml"), "llm_max_tokens = 7\nllm_temperature = 0.1\nllm_timeout_s = 1")
	writeNumericFile(t, "ladym.toml", "[llm]\nmax_tokens = 8\ntemperature = 0.2\ntimeout_s = 2")
	path := filepath.Join(t.TempDir(), "explicit.toml")
	writeNumericFile(t, path, "llm_max_tokens = 9\nllm_temperature = 0.3\nllm_timeout_s = 3")
	t.Setenv("LADYM_LLM_MAX_TOKENS", "10")
	t.Setenv("LADYM_LLM_TEMPERATURE", "0.4")
	t.Setenv("LADYM_LLM_TIMEOUT_S", "4")
	cli := map[string]any{"llm": map[string]any{"max_tokens": 0.0, "temperature": "-0.5", "timeout_s": "-2.5"}}
	before, err := json.Marshal(cli)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, cli)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMMaxTokens != 0 || cfg.LLMTemperature != -0.5 || cfg.LLMTimeoutS != -2.5 ||
		cfg.LLM.MaxTokens != 0 || cfg.LLM.Temperature != -0.5 || cfg.LLM.TimeoutS != -2.5 {
		t.Errorf("CLI zero/negative values or nested mirror lost: %+v", cfg.LLM)
	}
	after, err := json.Marshal(cli)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("Load mutated CLI overrides: before=%s, after=%s", before, after)
	}
	t.Setenv("LADYM_LLM_MAX_TOKENS", "")
	t.Setenv("LADYM_LLM_TEMPERATURE", "")
	t.Setenv("LADYM_LLM_TIMEOUT_S", "")
	cfg, err = Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMMaxTokens != 9 || cfg.LLMTemperature != 0.3 || cfg.LLMTimeoutS != 3 {
		t.Errorf("cross-source aliases lost precedence: tokens=%d temperature=%v timeout=%v",
			cfg.LLMMaxTokens, cfg.LLMTemperature, cfg.LLMTimeoutS)
	}
}

func TestLLMNumericEnvironment(t *testing.T) {
	for _, key := range []string{"LADYM_LLM_MAX_TOKENS", "LADYM_LLM_TEMPERATURE", "LADYM_LLM_TIMEOUT_S"} {
		t.Run(key, func(t *testing.T) {
			isolateNumericLoad(t)
			t.Setenv(key, "bad")
			cfg, err := Load("", nil)
			requireNumericConfigError(t, err, key)
			if cfg != nil {
				t.Error("invalid environment returned a configuration")
			}
			t.Setenv(key, "")
			if _, err := Load("", nil); err != nil {
				t.Fatalf("empty environment must be ignored: %v", err)
			}
		})
	}
	supported := []struct {
		name  string
		key   string
		value string
		check func(*Config) bool
	}{
		{
			name: "max-tokens", key: "LADYM_LLM_MAX_TOKENS", value: " 2048 ",
			check: func(cfg *Config) bool { return cfg.LLMMaxTokens == 2048 && cfg.LLM.MaxTokens == 2048 },
		},
		{
			name: "temperature", key: "LADYM_LLM_TEMPERATURE", value: " 5e-1 ",
			check: func(cfg *Config) bool { return cfg.LLMTemperature == 0.5 && cfg.LLM.Temperature == 0.5 },
		},
		{
			name: "timeout", key: "LADYM_LLM_TIMEOUT_S", value: " 2.5 ",
			check: func(cfg *Config) bool { return cfg.LLMTimeoutS == 2.5 && cfg.LLM.TimeoutS == 2.5 },
		},
	}
	for _, tc := range supported {
		t.Run(tc.name+"-supported", func(t *testing.T) {
			isolateNumericLoad(t)
			t.Setenv(tc.key, tc.value)
			cfg, err := Load("", nil)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(cfg) {
				t.Errorf("%s not applied or nested mirror lost: flat=%d/%v/%v nested=%+v",
					tc.key, cfg.LLMMaxTokens, cfg.LLMTemperature, cfg.LLMTimeoutS, cfg.LLM)
			}
		})
	}
}

func TestLLMNumericCLIRejectsDuplicateAndUnusedInvalidFields(t *testing.T) {
	isolateNumericLoad(t)
	for _, overrides := range []map[string]any{
		{"llm_temperature": 0.5, "llm": map[string]any{"temperature": 0.5}},
		{"agents": map[string]any{"unused": map[string]any{"provider": "none", "temperature": "bad"}}},
		{"llm_timeout_s": nil},
	} {
		_, err := Load("", overrides)
		requireNumericConfigError(t, err, "<cli-overrides>")
	}
}

func TestLLMNumericUnrelatedParsingUnchanged(t *testing.T) {
	cfg, err := FromFile(writeToml(t, "[embedding]\ntimeout_s = \"legacy-invalid\""))
	if err != nil || cfg.EmbeddingTimeoutS != 0 {
		t.Fatalf("unrelated numeric parsing changed: cfg=%v, err=%v", cfg, err)
	}
}
