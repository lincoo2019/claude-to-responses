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
	"syscall"
	"time"

	"github.com/xy200303/claude-to-responses/converter"
)

type Config struct {
	ClaudeAPIKey  string
	ClaudeBaseURL string
	ListenAddr    string
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

	if cfg.ClaudeAPIKey == "" {
		log.Fatal("CLAUDE_API_KEY environment variable is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", handleResponses(cfg))
	mux.HandleFunc("/v1/responses/", handleResponses(cfg))
	mux.HandleFunc("/health", handleHealth)

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Claude-to-Responses proxy server starting on %s", cfg.ListenAddr)
		log.Printf("Forwarding to Claude API at %s", cfg.ClaudeBaseURL)
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

func handleResponses(cfg *Config) http.HandlerFunc {
	client := &http.Client{
		Timeout: 300 * time.Second,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

		claudeURL := strings.TrimRight(cfg.ClaudeBaseURL, "/") + "/v1/messages"

		claudeReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, claudeURL, bytes.NewReader(claudeBody))
		if err != nil {
			log.Printf("Error creating Claude request: %v", err)
			writeErrorResponse(w, http.StatusInternalServerError, "Internal server error")
			return
		}

		claudeReq.Header.Set("Content-Type", "application/json")
		claudeReq.Header.Set("x-api-key", cfg.ClaudeAPIKey)
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
