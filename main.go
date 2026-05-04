package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xy200303/claude-to-responses/converter"
)

type Config struct {
	mu            sync.RWMutex
	ClaudeAPIKey  string
	ClaudeBaseURL string
	ListenAddr    string
}

func (c *Config) GetAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ClaudeAPIKey
}

func (c *Config) GetBaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ClaudeBaseURL
}

func (c *Config) Set(apiKey, baseURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if apiKey != "" {
		c.ClaudeAPIKey = apiKey
	}
	if baseURL != "" {
		c.ClaudeBaseURL = baseURL
	}
}

func (c *Config) Snapshot() (apiKey, baseURL string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ClaudeAPIKey, c.ClaudeBaseURL
}

func loadConfig() *Config {
	cfg := &Config{
		ClaudeAPIKey:  os.Getenv("CLAUDE_API_KEY"),
		ClaudeBaseURL: os.Getenv("CLAUDE_BASE_URL"),
		ListenAddr:    os.Getenv("LISTEN_ADDR"),
	}
	if cfg.ClaudeBaseURL == "" {
		cfg.ClaudeBaseURL = "https://api.anthropic.com"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	return cfg
}

func main() {
	cfg := loadConfig()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", handleResponses(cfg))
	mux.HandleFunc("/v1/responses/", handleResponses(cfg))
	mux.HandleFunc("/v1/settings", handleSettings(cfg))
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/", handleSettingsPage)

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Claude-to-Responses proxy server starting on %s", cfg.ListenAddr)
		log.Printf("Forwarding to Claude API at %s", cfg.GetBaseURL())
		log.Printf("Settings page: http://localhost%s/", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleSettings(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			apiKey, baseURL := cfg.Snapshot()
			maskedKey := maskAPIKey(apiKey)
			json.NewEncoder(w).Encode(map[string]string{
				"claude_api_key":  maskedKey,
				"claude_base_url": baseURL,
			})

		case http.MethodPost:
			var req struct {
				ClaudeAPIKey  string `json:"claude_api_key"`
				ClaudeBaseURL string `json:"claude_base_url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
				return
			}

			if req.ClaudeBaseURL == "" {
				req.ClaudeBaseURL = "https://api.anthropic.com"
			}

			cfg.Set(req.ClaudeAPIKey, req.ClaudeBaseURL)
			log.Printf("Settings updated: base_url=%s", req.ClaudeBaseURL)

			apiKey, baseURL := cfg.Snapshot()
			json.NewEncoder(w).Encode(map[string]string{
				"claude_api_key":  maskAPIKey(apiKey),
				"claude_base_url": baseURL,
				"status":          "ok",
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		}
	}
}

func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func handleResponses(cfg *Config) http.HandlerFunc {
	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		apiKey := cfg.GetAPIKey()
		if apiKey == "" {
			writeErrorResponse(w, http.StatusServiceUnavailable, "Claude API key not configured. Please set it in the settings page.")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		claudeBody, err := converter.ConvertResponsesRequestToClaude(body)
		if err != nil {
			log.Printf("Error converting request: %v", err)
			writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Request conversion error: %v", err))
			return
		}

		var reqPreview struct {
			Stream bool `json:"stream"`
		}
		json.Unmarshal(body, &reqPreview)

		baseURL := cfg.GetBaseURL()
		claudeURL := strings.TrimRight(baseURL, "/") + "/v1/messages"

		claudeReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, claudeURL, bytes.NewReader(claudeBody))
		if err != nil {
			log.Printf("Error creating Claude request: %v", err)
			writeErrorResponse(w, http.StatusInternalServerError, "Internal server error")
			return
		}

		claudeReq.Header.Set("Content-Type", "application/json")
		claudeReq.Header.Set("x-api-key", apiKey)
		claudeReq.Header.Set("anthropic-version", "2023-06-01")

		forwardHeaders(r, claudeReq, []string{
			"anthropic-beta",
			"anthropic-dangerous-direct-browser-access",
		})

		resp, err := client.Do(claudeReq)
		if err != nil {
			log.Printf("Error forwarding to Claude: %v", err)
			writeErrorResponse(w, http.StatusBadGateway, fmt.Sprintf("Claude API error: %v", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			log.Printf("Claude API returned status %d: %s", resp.StatusCode, string(respBody))
			for k, vs := range resp.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			w.Write(respBody)
			return
		}

		if reqPreview.Stream {
			handleStreamResponse(w, resp)
		} else {
			handleNonStreamResponse(w, resp)
		}
	}
}

func handleNonStreamResponse(w http.ResponseWriter, resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading Claude response: %v", err)
		writeErrorResponse(w, http.StatusBadGateway, "Failed to read Claude response")
		return
	}

	responsesBody, err := converter.ConvertClaudeResponseToResponses(body)
	if err != nil {
		log.Printf("Error converting response: %v", err)
		writeErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Response conversion error: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responsesBody)
}

func handleStreamResponse(w http.ResponseWriter, resp *http.Response) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorResponse(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	responseID := converter.GenerateResponseID()
	var model string

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimPrefix(payload, " ")
		payload = strings.TrimSpace(payload)

		if payload == "[DONE]" {
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			break
		}

		converted, newRespID, err := converter.ConvertClaudeStreamEventToResponses(
			"", []byte(payload), responseID, model,
		)
		if err != nil {
			log.Printf("Error converting stream event: %v", err)
			continue
		}

		if newRespID != responseID {
			responseID = newRespID
		}

		if len(converted) > 0 {
			fmt.Fprintf(w, "data: %s\n\n", converted)
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading Claude stream: %v", err)
	}
}

func forwardHeaders(src *http.Request, dst *http.Request, headers []string) {
	for _, h := range headers {
		if v := src.Header.Get(h); v != "" {
			dst.Header.Set(h, v)
		}
	}
}

func writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    "proxy_error",
		},
	})
}

func handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(settingsPageHTML))
}

const settingsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Claude to Responses - Settings</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #0f0f11;
    color: #e4e4e7;
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .container {
    width: 100%;
    max-width: 520px;
    padding: 24px;
  }
  .card {
    background: #18181b;
    border: 1px solid #27272a;
    border-radius: 16px;
    padding: 32px;
  }
  h1 {
    font-size: 22px;
    font-weight: 600;
    margin-bottom: 4px;
    color: #fafafa;
  }
  .subtitle {
    font-size: 14px;
    color: #71717a;
    margin-bottom: 28px;
  }
  .form-group {
    margin-bottom: 20px;
  }
  label {
    display: block;
    font-size: 13px;
    font-weight: 500;
    color: #a1a1aa;
    margin-bottom: 6px;
  }
  input {
    width: 100%;
    padding: 10px 14px;
    background: #0f0f11;
    border: 1px solid #27272a;
    border-radius: 10px;
    color: #fafafa;
    font-size: 14px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    outline: none;
    transition: border-color 0.2s;
  }
  input:focus {
    border-color: #6366f1;
  }
  input::placeholder {
    color: #3f3f46;
  }
  .btn {
    width: 100%;
    padding: 12px;
    background: #6366f1;
    color: #fff;
    border: none;
    border-radius: 10px;
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.2s;
    margin-top: 8px;
  }
  .btn:hover { background: #4f46e5; }
  .btn:active { background: #4338ca; }
  .btn:disabled { background: #3f3f46; cursor: not-allowed; }
  .status {
    margin-top: 16px;
    padding: 12px 16px;
    border-radius: 10px;
    font-size: 13px;
    display: none;
  }
  .status.success {
    display: block;
    background: rgba(34,197,94,0.1);
    border: 1px solid rgba(34,197,94,0.2);
    color: #22c55e;
  }
  .status.error {
    display: block;
    background: rgba(239,68,68,0.1);
    border: 1px solid rgba(239,68,68,0.2);
    color: #ef4444;
  }
  .info-section {
    margin-top: 24px;
    padding-top: 20px;
    border-top: 1px solid #27272a;
  }
  .info-title {
    font-size: 13px;
    font-weight: 500;
    color: #71717a;
    margin-bottom: 10px;
  }
  .info-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 8px 0;
    font-size: 13px;
  }
  .info-label { color: #a1a1aa; }
  .info-value {
    color: #e4e4e7;
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
  }
  .badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 6px;
    font-size: 11px;
    font-weight: 600;
  }
  .badge.ok { background: rgba(34,197,94,0.15); color: #22c55e; }
  .badge.warn { background: rgba(234,179,8,0.15); color: #eab308; }
  .endpoints {
    margin-top: 16px;
  }
  .endpoint {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: #0f0f11;
    border-radius: 8px;
    margin-bottom: 6px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
  }
  .method {
    padding: 2px 6px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 700;
  }
  .method.post { background: rgba(34,197,94,0.15); color: #22c55e; }
  .method.get { background: rgba(59,130,246,0.15); color: #3b82f6; }
</style>
</head>
<body>
<div class="container">
  <div class="card">
    <h1>Claude to Responses</h1>
    <p class="subtitle">API protocol converter proxy settings</p>

    <form id="settingsForm">
      <div class="form-group">
        <label for="apiKey">Claude API Key</label>
        <input type="password" id="apiKey" name="claude_api_key" placeholder="sk-ant-api03-..." autocomplete="off">
      </div>
      <div class="form-group">
        <label for="baseUrl">Claude Base URL</label>
        <input type="url" id="baseUrl" name="claude_base_url" placeholder="https://api.anthropic.com">
      </div>
      <button type="submit" class="btn" id="saveBtn">Save Settings</button>
    </form>

    <div id="status" class="status"></div>

    <div class="info-section">
      <div class="info-title">Current Status</div>
      <div class="info-row">
        <span class="info-label">API Key</span>
        <span id="currentKey" class="info-value">-</span>
      </div>
      <div class="info-row">
        <span class="info-label">Base URL</span>
        <span id="currentUrl" class="info-value">-</span>
      </div>
      <div class="info-row">
        <span class="info-label">Status</span>
        <span id="currentStatus" class="info-value">-</span>
      </div>
    </div>

    <div class="endpoints">
      <div class="info-title">API Endpoints</div>
      <div class="endpoint">
        <span class="method post">POST</span>
        <span>/v1/responses</span>
      </div>
      <div class="endpoint">
        <span class="method get">GET</span>
        <span>/v1/settings</span>
      </div>
      <div class="endpoint">
        <span class="method get">GET</span>
        <span>/health</span>
      </div>
    </div>
  </div>
</div>

<script>
(async function() {
  const form = document.getElementById('settingsForm');
  const statusEl = document.getElementById('status');
  const currentKeyEl = document.getElementById('currentKey');
  const currentUrlEl = document.getElementById('currentUrl');
  const currentStatusEl = document.getElementById('currentStatus');
  const saveBtn = document.getElementById('saveBtn');

  function showStatus(msg, type) {
    statusEl.textContent = msg;
    statusEl.className = 'status ' + type;
    if (type === 'success') {
      setTimeout(() => { statusEl.className = 'status'; }, 3000);
    }
  }

  async function loadSettings() {
    try {
      const resp = await fetch('/v1/settings');
      const data = await resp.json();
      currentKeyEl.textContent = data.claude_api_key || '(not set)';
      currentUrlEl.textContent = data.claude_base_url || '(not set)';

      if (data.claude_api_key && data.claude_api_key !== '') {
        currentStatusEl.innerHTML = '<span class="badge ok">Configured</span>';
      } else {
        currentStatusEl.innerHTML = '<span class="badge warn">Not Configured</span>';
      }

      if (data.claude_base_url) {
        document.getElementById('baseUrl').value = data.claude_base_url;
      }
    } catch (e) {
      currentStatusEl.innerHTML = '<span class="badge warn">Error</span>';
    }
  }

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving...';

    const apiKey = document.getElementById('apiKey').value;
    const baseUrl = document.getElementById('baseUrl').value;

    const payload = {};
    if (apiKey) payload.claude_api_key = apiKey;
    if (baseUrl) payload.claude_base_url = baseUrl;

    try {
      const resp = await fetch('/v1/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const data = await resp.json();

      if (resp.ok) {
        showStatus('Settings saved successfully!', 'success');
        document.getElementById('apiKey').value = '';
        await loadSettings();
      } else {
        showStatus(data.error || 'Failed to save settings', 'error');
      }
    } catch (e) {
      showStatus('Network error: ' + e.message, 'error');
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save Settings';
    }
  });

  await loadSettings();
})();
</script>
</body>
</html>`
