package converter

import "encoding/json"

type ClaudeRequest struct {
	Model       string          `json:"model"`
	System      json.RawMessage `json:"system,omitempty"`
	Messages    []ClaudeMessage `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []ClaudeTool    `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

type ClaudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ClaudeContentPart struct {
	Type      string             `json:"type"`
	ID        string             `json:"id,omitempty"`
	Text      string             `json:"text,omitempty"`
	Source    *ClaudeImageSource `json:"source,omitempty"`
	Name      string             `json:"name,omitempty"`
	Input     json.RawMessage    `json:"input,omitempty"`
	ToolUseID string             `json:"tool_use_id,omitempty"`
	Content   json.RawMessage    `json:"content,omitempty"`
}

type ClaudeImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type ClaudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ClaudeResponse struct {
	ID         string              `json:"id,omitempty"`
	Type       string              `json:"type,omitempty"`
	Role       string              `json:"role,omitempty"`
	Model      string              `json:"model,omitempty"`
	Content    []ClaudeContentPart `json:"content,omitempty"`
	StopReason string              `json:"stop_reason,omitempty"`
	Usage      *ClaudeUsage        `json:"usage,omitempty"`
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type ClaudeStreamEvent struct {
	Type    string             `json:"type"`
	Index   int                `json:"index,omitempty"`
	Message *ClaudeResponse    `json:"message,omitempty"`
	Content *ClaudeContentPart `json:"content_block,omitempty"`
	Delta   *ClaudeStreamDelta `json:"delta,omitempty"`
	Usage   *ClaudeUsage       `json:"usage,omitempty"`
}

type ClaudeStreamDelta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}
