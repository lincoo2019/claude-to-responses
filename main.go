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
	"path/filepath"
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
	configPath    string
	ModelMapping  map[string]string
	DefaultModel  string
	APIFormat     string
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
	c.saveLocked()
}

func (c *Config) GetAPIFormat() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.APIFormat == "" {
		return "claude"
	}
	return c.APIFormat
}

func (c *Config) MapModel(model string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ModelMapping != nil {
		if mapped, ok := c.ModelMapping[model]; ok {
			return mapped
		}
	}
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	return model
}

func (c *Config) SetModelMapping(mapping map[string]string, defaultModel string, apiFormat string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ModelMapping = mapping
	c.DefaultModel = defaultModel
	if apiFormat != "" {
		c.APIFormat = apiFormat
	}
	c.saveLocked()
}

func (c *Config) GetModelMapping() (map[string]string, string, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	mapping := make(map[string]string)
	for k, v := range c.ModelMapping {
		mapping[k] = v
	}
	return mapping, c.DefaultModel, c.GetAPIFormat()
}

func (c *Config) Snapshot() (apiKey, baseURL string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ClaudeAPIKey, c.ClaudeBaseURL
}

type configFile struct {
	ClaudeAPIKey  string            `json:"claude_api_key"`
	ClaudeBaseURL string            `json:"claude_base_url"`
	ModelMapping  map[string]string `json:"model_mapping,omitempty"`
	DefaultModel  string            `json:"default_model,omitempty"`
	APIFormat     string            `json:"api_format,omitempty"`
}

func (c *Config) saveLocked() {
	if c.configPath == "" {
		return
	}
	data := configFile{
		ClaudeAPIKey:  c.ClaudeAPIKey,
		ClaudeBaseURL: c.ClaudeBaseURL,
		ModelMapping:  c.ModelMapping,
		DefaultModel:  c.DefaultModel,
		APIFormat:     c.APIFormat,
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("Error marshaling config: %v", err)
		return
	}
	dir := filepath.Dir(c.configPath)
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(c.configPath, raw, 0600); err != nil {
		log.Printf("Error saving config: %v", err)
	}
}

func (c *Config) loadFromFile() {
	if c.configPath == "" {
		return
	}
	raw, err := os.ReadFile(c.configPath)
	if err != nil {
		return
	}
	var data configFile
	if err := json.Unmarshal(raw, &data); err != nil {
		log.Printf("Error parsing config file: %v", err)
		return
	}
	if data.ClaudeAPIKey != "" {
		c.ClaudeAPIKey = data.ClaudeAPIKey
	}
	if data.ClaudeBaseURL != "" {
		c.ClaudeBaseURL = data.ClaudeBaseURL
	}
	if data.ModelMapping != nil {
		c.ModelMapping = data.ModelMapping
	}
	if data.DefaultModel != "" {
		c.DefaultModel = data.DefaultModel
	}
	if data.APIFormat != "" {
		c.APIFormat = data.APIFormat
	}
}

func configFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude-to-responses", "config.json")
}

func loadConfig() *Config {
	cfgPath := configFilePath()
	cfg := &Config{
		ClaudeAPIKey:  os.Getenv("CLAUDE_API_KEY"),
		ClaudeBaseURL: os.Getenv("CLAUDE_BASE_URL"),
		ListenAddr:    os.Getenv("LISTEN_ADDR"),
		configPath:    cfgPath,
	}
	if cfg.ClaudeBaseURL == "" {
		cfg.ClaudeBaseURL = "https://api.anthropic.com"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}

	cfg.loadFromFile()

	if envKey := os.Getenv("CLAUDE_API_KEY"); envKey != "" {
		cfg.ClaudeAPIKey = envKey
	}
	if envURL := os.Getenv("CLAUDE_BASE_URL"); envURL != "" {
		cfg.ClaudeBaseURL = envURL
	}

	return cfg
}

func main() {
	cfg := loadConfig()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", handleResponses(cfg))
	mux.HandleFunc("/v1/responses/", handleResponses(cfg))
	mux.HandleFunc("/responses", handleResponses(cfg))
	mux.HandleFunc("/responses/", handleResponses(cfg))
	mux.HandleFunc("/v1/settings", handleSettings(cfg))
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/", handleSettingsPage)

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
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
			mapping, defaultModel, apiFormat := cfg.GetModelMapping()
			json.NewEncoder(w).Encode(map[string]any{
				"claude_api_key":  maskAPIKey(apiKey),
				"claude_base_url": baseURL,
				"model_mapping":   mapping,
				"default_model":   defaultModel,
				"api_format":      apiFormat,
			})

		case http.MethodPost:
			var req struct {
				ClaudeAPIKey  string            `json:"claude_api_key"`
				ClaudeBaseURL string            `json:"claude_base_url"`
				ModelMapping  map[string]string `json:"model_mapping"`
				DefaultModel  string            `json:"default_model"`
				APIFormat     string            `json:"api_format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
				return
			}

			cfg.Set(req.ClaudeAPIKey, req.ClaudeBaseURL)
			if req.ModelMapping != nil || req.DefaultModel != "" || req.APIFormat != "" {
				cfg.SetModelMapping(req.ModelMapping, req.DefaultModel, req.APIFormat)
			}
			log.Printf("Settings updated: base_url=%s, default_model=%s, api_format=%s", req.ClaudeBaseURL, req.DefaultModel, req.APIFormat)

			apiKey, baseURL := cfg.Snapshot()
			mapping, defaultModel, apiFormat := cfg.GetModelMapping()
			json.NewEncoder(w).Encode(map[string]any{
				"claude_api_key":  maskAPIKey(apiKey),
				"claude_base_url": baseURL,
				"model_mapping":   mapping,
				"default_model":   defaultModel,
				"api_format":      apiFormat,
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
	client := &http.Client{}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		apiKey := cfg.GetAPIKey()
		if apiKey == "" {
			writeErrorResponse(w, http.StatusServiceUnavailable, "API key not configured. Please set it in the settings page.")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		apiFormat := cfg.GetAPIFormat()

		var backendBody []byte
		var backendPath string

		switch apiFormat {
		case "claude":
			backendBody, err = converter.ConvertResponsesRequestToClaude(body)
			backendPath = "/v1/messages"
		default:
			backendBody, err = converter.ConvertResponsesRequestToOpenAIChat(body)
			backendPath = "/v1/chat/completions"
		}
		if err != nil {
			log.Printf("Error converting request: %v", err)
			writeErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Request conversion error: %v", err))
			return
		}

		var diagReq struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools,omitempty"`
			System json.RawMessage `json:"system,omitempty"`
		}
		if json.Unmarshal(backendBody, &diagReq) == nil {
			toolNames := make([]string, 0, len(diagReq.Tools))
			for _, t := range diagReq.Tools {
				toolNames = append(toolNames, t.Name)
			}
			hasSystem := diagReq.System != nil && len(diagReq.System) > 2
			log.Printf("Backend request: tools=%v, has_system=%v", toolNames, hasSystem)
		}

		var reqModel struct {
			Model string `json:"model"`
		}
		json.Unmarshal(backendBody, &reqModel)
		mappedModel := cfg.MapModel(reqModel.Model)
		if mappedModel != reqModel.Model {
			switch apiFormat {
			case "claude":
				backendBody, _ = converter.ReplaceModelInClaudeRequest(backendBody, mappedModel)
			default:
				backendBody, _ = converter.ReplaceModelInOpenAIChatRequest(backendBody, mappedModel)
			}
			log.Printf("Model mapped: %s -> %s", reqModel.Model, mappedModel)
		}

		var reqPreview struct {
			Stream bool `json:"stream"`
		}
		json.Unmarshal(body, &reqPreview)

		baseURL := cfg.GetBaseURL()
		backendURL := strings.TrimRight(baseURL, "/") + backendPath

		backendReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, backendURL, bytes.NewReader(backendBody))
		if err != nil {
			log.Printf("Error creating backend request: %v", err)
			writeErrorResponse(w, http.StatusInternalServerError, "Internal server error")
			return
		}

		backendReq.Header.Set("Content-Type", "application/json")

		switch apiFormat {
		case "claude":
			backendReq.Header.Set("x-api-key", apiKey)
			backendReq.Header.Set("anthropic-version", "2023-06-01")
			forwardHeaders(r, backendReq, []string{
				"anthropic-beta",
				"anthropic-dangerous-direct-browser-access",
			})
		default:
			backendReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := client.Do(backendReq)
		if err != nil {
			log.Printf("Error forwarding to backend: %v", err)
			writeErrorResponse(w, http.StatusBadGateway, fmt.Sprintf("Backend API error: %v", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			log.Printf("Backend API returned status %d: %s", resp.StatusCode, string(respBody))
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
			handleStreamResponse(w, resp, r, apiFormat)
		} else {
			handleNonStreamResponse(w, resp, apiFormat)
		}
	}
}

func handleNonStreamResponse(w http.ResponseWriter, resp *http.Response, apiFormat string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading backend response: %v", err)
		writeErrorResponse(w, http.StatusBadGateway, "Failed to read backend response")
		return
	}

	var responsesBody []byte
	switch apiFormat {
	case "claude":
		responsesBody, err = converter.ConvertClaudeResponseToResponses(body)
	default:
		responsesBody, err = converter.ConvertOpenAIChatResponseToResponses(body)
	}
	if err != nil {
		log.Printf("Error converting response: %v", err)
		writeErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Response conversion error: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responsesBody)
}

func handleStreamResponse(w http.ResponseWriter, resp *http.Response, r *http.Request, apiFormat string) {
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

	ctx := &converter.StreamContext{
		ResponseID: responseID,
	}

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	eventCount := 0
	convertedCount := 0
	clientDisconnected := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if line == "" {
					log.Printf("Stream ended: backend EOF after %d backend events, %d converted events", eventCount, convertedCount)
					break
				}
			} else {
				if r.Context().Err() != nil {
					clientDisconnected = true
					log.Printf("Client disconnected during stream: %v (after %d backend events, %d converted)", r.Context().Err(), eventCount, convertedCount)
				} else {
					log.Printf("Error reading backend stream: %v (after %d backend events, %d converted)", err, eventCount, convertedCount)
				}
				break
			}
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, "data: ") {
			if strings.HasPrefix(line, "event:") {
				continue
			}
			continue
		}

		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimPrefix(payload, " ")
		payload = strings.TrimSpace(payload)

		if payload == "[DONE]" {
			log.Printf("Stream [DONE] received after %d backend events, %d converted events", eventCount, convertedCount)
			break
		}

		eventCount++

		var converted [][]byte
		var convErr error

		switch apiFormat {
		case "claude":
			converted, convErr = converter.ConvertClaudeStreamEventToResponses(
				"", []byte(payload), ctx,
			)
		default:
			converted, convErr = converter.ConvertOpenAIStreamChunkToResponses(
				[]byte(payload), ctx,
			)
		}
		if convErr != nil {
			log.Printf("Error converting stream event #%d: %v, payload: %s", eventCount, convErr, payload)
			continue
		}

		var rawEvt struct {
			Type    string `json:"type"`
			Content *struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block,omitempty"`
			Delta *struct {
				Type       string `json:"type"`
				StopReason string `json:"stop_reason"`
			} `json:"delta,omitempty"`
		}
		if json.Unmarshal([]byte(payload), &rawEvt) == nil {
			if rawEvt.Type == "content_block_start" && rawEvt.Content != nil {
				log.Printf("  [backend] content_block_start: type=%s name=%s", rawEvt.Content.Type, rawEvt.Content.Name)
			} else if rawEvt.Type == "message_delta" && rawEvt.Delta != nil && rawEvt.Delta.StopReason != "" {
				log.Printf("  [backend] message_delta: stop_reason=%s", rawEvt.Delta.StopReason)
			}
		}

		for _, event := range converted {
			convertedCount++
			eventType := converter.ExtractResponsesEventType(event)
			if clientDisconnected {
				continue
			}
			if eventType != "" {
				fmt.Fprintf(w, "event: %s\n", eventType)
			}
			fmt.Fprintf(w, "data: %s\n\n", event)
			flusher.Flush()
		}
	}

	if clientDisconnected {
		return
	}

	if !ctx.CompletedSent {
		log.Printf("WARNING: stream ended without response.completed being sent! Force sending...")
		outputItems := converter.BuildCurrentOutputItems(ctx)
		hasToolCalls := len(ctx.ToolCalls) > 0
		endTurn := !hasToolCalls
		forceCompleted := converter.ResponsesStreamEvent{
			Type:       "response.completed",
			ResponseID: ctx.ResponseID,
			Response: &converter.ResponsesEventResp{
				ID:        ctx.ResponseID,
				Object:    "response",
				Model:     ctx.Model,
				Status:    "completed",
				Output:    outputItems,
				Usage:     &converter.ResponsesUsage{},
				CreatedAd: ctx.CreatedAt,
				EndTurn:   &endTurn,
			},
		}
		out, err := json.Marshal(forceCompleted)
		if err == nil {
			fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", out)
			flusher.Flush()
		}
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
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
    max-width: 560px;
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
  input, textarea {
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
  input:focus, textarea:focus {
    border-color: #6366f1;
  }
  input::placeholder, textarea::placeholder {
    color: #3f3f46;
  }
  textarea {
    resize: vertical;
    min-height: 80px;
  }
  .hint {
    font-size: 11px;
    color: #52525b;
    margin-top: 4px;
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
  .mapping-list {
    margin-top: 8px;
  }
  .mapping-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    background: #0f0f11;
    border-radius: 8px;
    margin-bottom: 4px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
  }
  .mapping-arrow { color: #6366f1; }
  .mapping-del {
    margin-left: auto;
    background: none;
    border: none;
    color: #71717a;
    cursor: pointer;
    font-size: 16px;
    padding: 0 4px;
  }
  .mapping-del:hover { color: #ef4444; }
  .add-mapping {
    display: flex;
    gap: 8px;
    margin-top: 8px;
  }
  .add-mapping input {
    flex: 1;
    padding: 8px 10px;
    font-size: 12px;
  }
  .add-mapping button {
    padding: 8px 14px;
    background: #27272a;
    color: #e4e4e7;
    border: 1px solid #3f3f46;
    border-radius: 8px;
    cursor: pointer;
    font-size: 12px;
    white-space: nowrap;
  }
  .add-mapping button:hover { background: #3f3f46; }
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
      <div class="form-group">
        <label for="defaultModel">Default Model</label>
        <input type="text" id="defaultModel" name="default_model" placeholder="claude-sonnet-4-20250514">
        <div class="hint">Fallback model when no mapping matches</div>
      </div>
      <div class="form-group">
        <label for="apiFormat">Backend API Format</label>
        <select id="apiFormat" name="api_format" style="width:100%;padding:10px 14px;background:#0f0f11;border:1px solid #27272a;border-radius:10px;color:#fafafa;font-size:14px;font-family:'SF Mono','Fira Code',monospace;outline:none;">
          <option value="claude">Claude / Anthropic (/v1/messages)</option>
          <option value="openai">OpenAI Compatible (/v1/chat/completions)</option>
        </select>
        <div class="hint">Select the API format of your backend service</div>
      </div>
      <button type="submit" class="btn" id="saveBtn">Save Settings</button>
    </form>

    <div id="status" class="status"></div>

    <div class="info-section">
      <div class="info-title">Model Mapping</div>
      <div id="mappingList" class="mapping-list"></div>
      <div class="add-mapping">
        <input type="text" id="mapFrom" placeholder="gpt-4o">
        <input type="text" id="mapTo" placeholder="claude-sonnet-4-20250514">
        <button type="button" id="addMapBtn">Add</button>
      </div>
    </div>

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
        <span class="info-label">Default Model</span>
        <span id="currentModel" class="info-value">-</span>
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
  const currentModelEl = document.getElementById('currentModel');
  const currentStatusEl = document.getElementById('currentStatus');
  const saveBtn = document.getElementById('saveBtn');
  const mappingListEl = document.getElementById('mappingList');
  let modelMapping = {};

  function showStatus(msg, type) {
    statusEl.textContent = msg;
    statusEl.className = 'status ' + type;
    if (type === 'success') {
      setTimeout(() => { statusEl.className = 'status'; }, 3000);
    }
  }

  function renderMappings() {
    mappingListEl.innerHTML = '';
    const keys = Object.keys(modelMapping);
    if (keys.length === 0) {
      mappingListEl.innerHTML = '<div style="font-size:12px;color:#52525b;padding:4px 0;">No mappings configured</div>';
      return;
    }
    keys.forEach(from => {
      const row = document.createElement('div');
      row.className = 'mapping-row';
      row.innerHTML = '<span>' + escHtml(from) + '</span><span class="mapping-arrow">&rarr;</span><span>' + escHtml(modelMapping[from]) + '</span><button class="mapping-del" data-from="' + escAttr(from) + '">&times;</button>';
      mappingListEl.appendChild(row);
    });
    mappingListEl.querySelectorAll('.mapping-del').forEach(btn => {
      btn.addEventListener('click', () => {
        delete modelMapping[btn.dataset.from];
        saveModelMapping();
        renderMappings();
      });
    });
  }

  function escHtml(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }
  function escAttr(s) { return s.replace(/"/g, '&quot;'); }

  async function saveModelMapping() {
    const defaultModel = document.getElementById('defaultModel').value;
    await fetch('/v1/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ model_mapping: modelMapping, default_model: defaultModel }),
    });
  }

  document.getElementById('addMapBtn').addEventListener('click', () => {
    const from = document.getElementById('mapFrom').value.trim();
    const to = document.getElementById('mapTo').value.trim();
    if (!from || !to) return;
    modelMapping[from] = to;
    document.getElementById('mapFrom').value = '';
    document.getElementById('mapTo').value = '';
    saveModelMapping();
    renderMappings();
  });

  async function loadSettings() {
    try {
      const resp = await fetch('/v1/settings');
      const data = await resp.json();
      currentKeyEl.textContent = data.claude_api_key || '(not set)';
      currentUrlEl.textContent = data.claude_base_url || '(not set)';
      currentModelEl.textContent = data.default_model || '(not set)';

      if (data.claude_api_key && data.claude_api_key !== '') {
        currentStatusEl.innerHTML = '<span class="badge ok">Configured</span>';
      } else {
        currentStatusEl.innerHTML = '<span class="badge warn">Not Configured</span>';
      }

      if (data.claude_base_url) {
        document.getElementById('baseUrl').value = data.claude_base_url;
      }
      if (data.default_model) {
        document.getElementById('defaultModel').value = data.default_model;
      }
      if (data.api_format) {
        document.getElementById('apiFormat').value = data.api_format;
      }
      if (data.model_mapping) {
        modelMapping = data.model_mapping;
      }
      renderMappings();
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
    const defaultModel = document.getElementById('defaultModel').value;
    const apiFormat = document.getElementById('apiFormat').value;

    const payload = {};
    if (apiKey) payload.claude_api_key = apiKey;
    if (baseUrl) payload.claude_base_url = baseUrl;
    if (defaultModel) payload.default_model = defaultModel;
    payload.api_format = apiFormat;
    payload.model_mapping = modelMapping;

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
