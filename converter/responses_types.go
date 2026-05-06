package converter

import "encoding/json"

type ResponsesRequest struct {
	Model           string           `json:"model"`
	Input           json.RawMessage  `json:"input"`
	Instructions    json.RawMessage  `json:"instructions,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	MaxOutputTokens *int             `json:"max_output_tokens,omitempty"`
	Stream          bool             `json:"stream,omitempty"`
	Tools           []ResponsesTool  `json:"tools,omitempty"`
	ToolChoice      json.RawMessage  `json:"tool_choice,omitempty"`
	Metadata        map[string]any   `json:"metadata,omitempty"`
}

type ResponsesInputItem struct {
	Type      string                 `json:"type,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Content   []ResponsesContentPart `json:"content,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments json.RawMessage        `json:"arguments,omitempty"`
	Output    json.RawMessage        `json:"output,omitempty"`
}

type ResponsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type ResponsesTool struct {
	Type        string          `json:"type,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ResponsesResponse struct {
	ID     string               `json:"id,omitempty"`
	Object string               `json:"object,omitempty"`
	Model  string               `json:"model,omitempty"`
	Output []ResponsesOutputItem `json:"output,omitempty"`
	Usage  *ResponsesUsage      `json:"usage,omitempty"`
	Status string               `json:"status,omitempty"`
}

type ResponsesOutputItem struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Status    string                 `json:"status,omitempty"`
	Name      string                 `json:"name,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Output    json.RawMessage        `json:"output,omitempty"`
	Content   []ResponsesContentPart `json:"content,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ResponsesStreamEvent struct {
	Type         string               `json:"type"`
	ResponseID   string               `json:"response_id,omitempty"`
	ItemID       string               `json:"item_id,omitempty"`
	OutputIndex  int                  `json:"output_index,omitempty"`
	ContentIndex int                  `json:"content_index,omitempty"`
	Delta        string               `json:"delta,omitempty"`
	Usage        *ResponsesUsage      `json:"usage,omitempty"`
	Response     *ResponsesEventResp  `json:"response,omitempty"`
	Item         *ResponsesOutputItem `json:"item,omitempty"`
	Part         *ResponsesContentRef `json:"part,omitempty"`
}

type ResponsesEventResp struct {
	ID        string               `json:"id,omitempty"`
	Object    string               `json:"object,omitempty"`
	Model     string               `json:"model,omitempty"`
	Status    string               `json:"status,omitempty"`
	Output    []ResponsesOutputItem `json:"output,omitempty"`
	Usage     *ResponsesUsage      `json:"usage,omitempty"`
	CreatedAd int64                `json:"created_at,omitempty"`
	EndTurn   *bool                `json:"end_turn,omitempty"`
}

type ResponsesContentRef struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Index int    `json:"index,omitempty"`
}

type StreamOutputEvent struct {
	EventType string
	Payload   []byte
}
