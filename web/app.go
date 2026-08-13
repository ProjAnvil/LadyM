// Package web holds the local-only config editor (net/http + html/template).
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/secrets"
	"github.com/ProjAnvil/LadyM/storage"
)

const pageHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>LadyM config</title>
<style>body{font-family:system-ui,sans-serif;max-width:720px;margin:2rem auto;padding:0 1rem}label{display:block;margin-top:.5rem}input{width:100%;padding:.35rem}button{margin-top:.6rem;padding:.4rem .8rem}table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:.25rem .5rem}</style>
</head><body>
<h1>LadyM config</h1>
<form method="post" action="/save">
<label>db_path <input name="db_path" value="{{.Cfg.DBPath}}"></label>
<label>workspace <input name="workspace" value="{{.Cfg.Workspace}}"></label>
<h3>embedding</h3>
<label>provider <input name="embedding_provider" value="{{.Cfg.EmbeddingProvider}}"></label>
<label>base_url <input name="embedding_base_url" value="{{.Cfg.EmbeddingBaseURL}}"></label>
<label>model <input name="embedding_model" value="{{.Cfg.EmbeddingModel}}"></label>
<label>api_key_env <input name="embedding_api_key_env" value="{{.Cfg.EmbeddingAPIKeyEnv}}"></label>
<label>fallback <input name="embedding_fallback" value="{{.Cfg.EmbeddingFallback}}"></label>
<label>query_cache_size <input name="embedding_query_cache_size" value="{{.Cfg.EmbeddingQueryCacheSize}}"></label>
<h3>llm</h3>
<label>provider <input name="llm_provider" value="{{.Cfg.LLMProvider}}"></label>
<label>base_url <input name="llm_base_url" value="{{.Cfg.LLMBaseURL}}"></label>
<label>model <input name="llm_model" value="{{.Cfg.LLMModel}}"></label>
<label>api_key_env <input name="llm_api_key_env" value="{{.Cfg.LLMAPIKeyEnv}}"></label>
<label>structured_method <input name="llm_structured_method" value="{{.Cfg.LLMStructuredMethod}}"></label>
<h3>activation</h3>
<label>similarity <input name="activation_similarity" value="{{.Cfg.Activation.Similarity}}"></label>
<label>recency <input name="activation_recency" value="{{.Cfg.Activation.Recency}}"></label>
<label>frequency <input name="activation_frequency" value="{{.Cfg.Activation.Frequency}}"></label>
<button type="submit">Save</button>
</form>
<p>Secrets ({{if .MasterKeySet}}master key set{{else}}no master key{{end}})</p>
<ul>{{range .Names}}<li>{{.}}</li>{{end}}</ul>
<form method="post" action="/api/secrets"><input name="name" placeholder="KEY_NAME"><input name="value" placeholder="value"><button>store</button></form>
</body></html>`

// Run starts the local config editor.
func Run(configPath string, port int, noBrowser bool) error {
	cfgPath := configPath
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		cfg, err := config.Load(cfgPath, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		store := secrets.NewStore("")
		names, _ := store.ListNames()
		tmpl, _ := template.New("page").Parse(pageHTML)
		_ = tmpl.Execute(w, map[string]any{
			"Cfg": cfg, "MasterKeySet": store.HasMasterKey(), "Names": names,
		})
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.Load(cfgPath, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		applyForm(cfg, r)
		if err := writeToml("ladym.toml", cfg); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprint(w, "<p>Saved to ./ladym.toml</p>")
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.Load(cfgPath, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		eng, err := engine.New(cfg)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer eng.Close()
		s, err := eng.Stats()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintf(w, "<p>total memories: %d</p><p>avg tokens/mem: %.1f</p>", s.TotalMemories, s.AvgTokensPerMemory)
	})
	mux.HandleFunc("/test/embedding", func(w http.ResponseWriter, r *http.Request) {
		cfg := config.Default()
		applyForm(cfg, r)
		prov, err := storage.MakeProvider(cfg)
		if err != nil {
			fmt.Fprintf(w, "<small>✗ %v</small>", err)
			return
		}
		ok, msg := prov.HealthCheck()
		fmt.Fprintf(w, "<small>%v %s</small>", checkMark(ok), msg)
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc("/test/llm", func(w http.ResponseWriter, r *http.Request) {
		cfg := config.Default()
		applyForm(cfg, r)
		if cfg.LLMProvider == "none" {
			fmt.Fprint(w, "<small>✓ none (heuristic mode)</small>")
			return
		}
		prov, err := providers.MakeLLMProvider(cfg.LLMProvider, cfg.LLMBaseURL, cfg.LLMModel, os.Getenv(cfg.LLMAPIKeyEnv), cfg.LLMStructuredMethod, cfg.LLMMaxTokens, cfg.LLMTemperature, cfg.LLMTimeoutS)
		if err != nil {
			fmt.Fprintf(w, "<small>✗ %v</small>", err)
			return
		}
		if prov == nil {
			fmt.Fprint(w, "<small>✓ none (heuristic mode)</small>")
			return
		}
		out, err := prov.Complete([]providers.Message{{Role: "user", Content: "ping"}})
		if err != nil {
			fmt.Fprintf(w, "<small>✗ %v</small>", err)
			return
		}
		if len(out) > 20 {
			out = out[:20]
		}
		fmt.Fprintf(w, "<small>✓ %q</small>", out)
	})
	mux.HandleFunc("/api/secrets", func(w http.ResponseWriter, r *http.Request) {
		store := secrets.NewStore("")
		switch r.Method {
		case http.MethodGet:
			names, _ := store.ListNames()
			writeJSON(w, map[string]any{"master_key_set": store.HasMasterKey(), "names": names})
		case http.MethodPost:
			_ = r.ParseForm()
			name := r.FormValue("name")
			value := r.FormValue("value")
			if name == "" || value == "" {
				http.Error(w, "name and value are required", 400)
				return
			}
			if err := store.Set(name, value); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
		case http.MethodDelete:
			name := strings.TrimPrefix(r.URL.Path, "/api/secrets/")
			_, _ = store.Remove(name)
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/master-key", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var payload struct {
			Reset bool   `json:"reset"`
			Key   string `json:"key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		store := secrets.NewStore("")
		var err error
		if payload.Reset {
			err = store.ResetMasterKey(payload.Key)
		} else {
			_, err = store.SetMasterKey(payload.Key)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "master_key_set": true})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("LadyM config on http://%s/\n", addr)
	if !noBrowser {
		go func() {
			time.Sleep(time.Second)
			_ = openBrowser("http://" + addr + "/")
		}()
	}
	return http.ListenAndServe(addr, mux)
}

func checkMark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func openBrowser(url string) error {
	if _, err := exec.LookPath("open"); err == nil {
		return exec.Command("open", url).Start()
	}
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command("xdg-open", url).Start()
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func applyForm(cfg *config.Config, r *http.Request) {
	_ = r.ParseForm()
	get := func(k string) string { return r.FormValue(k) }
	if v := get("db_path"); v != "" {
		cfg.DBPath = v
	}
	if v := get("workspace"); v != "" {
		cfg.Workspace = v
	}
	if v := get("embedding_provider"); v != "" {
		cfg.EmbeddingProvider = v
	}
	if v := get("embedding_base_url"); v != "" {
		cfg.EmbeddingBaseURL = v
	}
	if v := get("embedding_model"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v := get("embedding_api_key_env"); v != "" {
		cfg.EmbeddingAPIKeyEnv = v
	}
	if v := get("embedding_fallback"); v != "" {
		cfg.EmbeddingFallback = v
	}
	if v := get("embedding_query_cache_size"); v != "" {
		cfg.EmbeddingQueryCacheSize, _ = strconv.Atoi(v)
	}
	if v := get("llm_provider"); v != "" {
		cfg.LLMProvider = v
	}
	if v := get("llm_base_url"); v != "" {
		cfg.LLMBaseURL = v
	}
	if v := get("llm_model"); v != "" {
		cfg.LLMModel = v
	}
	if v := get("llm_api_key_env"); v != "" {
		cfg.LLMAPIKeyEnv = v
	}
	if v := get("llm_structured_method"); v != "" {
		cfg.LLMStructuredMethod = v
	}
	if v := get("activation_similarity"); v != "" {
		cfg.Activation.Similarity, _ = strconv.ParseFloat(v, 64)
	}
	if v := get("activation_recency"); v != "" {
		cfg.Activation.Recency, _ = strconv.ParseFloat(v, 64)
	}
	if v := get("activation_frequency"); v != "" {
		cfg.Activation.Frequency, _ = strconv.ParseFloat(v, 64)
	}
}

func tomlScalar(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		s := strings.ReplaceAll(fmt.Sprint(v), "\\", "\\\\")
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}
}

func writeToml(path string, cfg *config.Config) error {
	lines := []string{
		"db_path = " + tomlScalar(cfg.DBPath),
		"workspace = " + tomlScalar(cfg.Workspace),
		"",
		"[embedding]",
		"provider = " + tomlScalar(cfg.EmbeddingProvider),
		"base_url = " + tomlScalar(cfg.EmbeddingBaseURL),
		"model = " + tomlScalar(cfg.EmbeddingModel),
		"api_key_env = " + tomlScalar(cfg.EmbeddingAPIKeyEnv),
		"fallback = " + tomlScalar(cfg.EmbeddingFallback),
		"query_cache_size = " + tomlScalar(cfg.EmbeddingQueryCacheSize),
		"",
		"[llm]",
		"provider = " + tomlScalar(cfg.LLMProvider),
		"base_url = " + tomlScalar(cfg.LLMBaseURL),
		"model = " + tomlScalar(cfg.LLMModel),
		"api_key_env = " + tomlScalar(cfg.LLMAPIKeyEnv),
		"structured_method = " + tomlScalar(cfg.LLMStructuredMethod),
		"",
		"[activation]",
		"similarity = " + tomlScalar(cfg.Activation.Similarity),
		"recency = " + tomlScalar(cfg.Activation.Recency),
		"frequency = " + tomlScalar(cfg.Activation.Frequency),
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
