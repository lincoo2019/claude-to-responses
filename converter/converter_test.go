package converter

import (
	"encoding/json"
	"testing"
)

func TestConvertResponsesRequestToClaude_SimpleString(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-20250514",
		"input": "Hello, how are you?"
	}`

	out, err := ConvertResponsesRequestToClaude([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req ClaudeRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if req.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model claude-sonnet-4-20250514, got %s", req.Model)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("expected role user, got %s", req.Messages[0].Role)
	}

	var content string
	if err := json.Unmarshal(req.Messages[0].Content, &content); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	if content != "Hello, how are you?" {
		t.Errorf("expected content 'Hello, how are you?', got %s", content)
	}
}

func TestConvertResponsesRequestToClaude_WithMessages(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-20250514",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Hello"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Hi there!"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "How are you?"}]}
		],
		"temperature": 0.7,
		"max_output_tokens": 1024
	}`

	out, err := ConvertResponsesRequestToClaude([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req ClaudeRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("expected first message role user, got %s", req.Messages[0].Role)
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("expected second message role assistant, got %s", req.Messages[1].Role)
	}
	if req.Messages[2].Role != "user" {
		t.Errorf("expected third message role user, got %s", req.Messages[2].Role)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", req.Temperature)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 1024 {
		t.Errorf("expected max_tokens 1024, got %v", req.MaxTokens)
	}
}

func TestConvertResponsesRequestToClaude_WithSystemMessage(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-20250514",
		"input": [
			{"type": "message", "role": "system", "content": [{"type": "input_text", "text": "You are a helpful assistant."}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Hello"}]}
		]
	}`

	out, err := ConvertResponsesRequestToClaude([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req ClaudeRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message (system should be separate), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("expected message role user, got %s", req.Messages[0].Role)
	}

	var systemText string
	if err := json.Unmarshal(req.System, &systemText); err != nil {
		t.Fatalf("failed to unmarshal system: %v", err)
	}
	if systemText != "You are a helpful assistant." {
		t.Errorf("expected system text 'You are a helpful assistant.', got %s", systemText)
	}
}

func TestConvertResponsesRequestToClaude_WithTools(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-20250514",
		"input": "What's the weather?",
		"tools": [
			{
				"name": "get_weather",
				"description": "Get weather for a location",
				"parameters": {"type": "object", "properties": {"location": {"type": "string"}}}
			}
		]
	}`

	out, err := ConvertResponsesRequestToClaude([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req ClaudeRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", req.Tools[0].Name)
	}
	if req.Tools[0].Description != "Get weather for a location" {
		t.Errorf("unexpected tool description: %s", req.Tools[0].Description)
	}
}

func TestConvertResponsesRequestToClaude_FunctionCall(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-20250514",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What's the weather?"}]},
			{"type": "function_call", "call_id": "call_123", "name": "get_weather", "arguments": "{\"location\": \"NYC\"}"},
			{"type": "function_call_output", "call_id": "call_123", "name": "get_weather", "output": "{\"temp\": 72}"}
		]
	}`

	out, err := ConvertResponsesRequestToClaude([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req ClaudeRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("expected function_call to map to assistant, got %s", req.Messages[1].Role)
	}
	if req.Messages[2].Role != "user" {
		t.Errorf("expected function_call_output to map to user, got %s", req.Messages[2].Role)
	}
}

func TestConvertClaudeResponseToResponses_SimpleText(t *testing.T) {
	input := `{
		"id": "msg_0123",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-20250514",
		"content": [{"type": "text", "text": "Hello! How can I help you?"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 8}
	}`

	out, err := ConvertClaudeResponseToResponses([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp ResponsesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if resp.ID != "msg_0123" {
		t.Errorf("expected id msg_0123, got %s", resp.ID)
	}
	if resp.Object != "response" {
		t.Errorf("expected object response, got %s", resp.Object)
	}
	if resp.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model claude-sonnet-4-20250514, got %s", resp.Model)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status completed, got %s", resp.Status)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "message" {
		t.Errorf("expected output type message, got %s", resp.Output[0].Type)
	}
	if resp.Output[0].Role != "assistant" {
		t.Errorf("expected output role assistant, got %s", resp.Output[0].Role)
	}
	if len(resp.Output[0].Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(resp.Output[0].Content))
	}
	if resp.Output[0].Content[0].Type != "output_text" {
		t.Errorf("expected content type output_text, got %s", resp.Output[0].Content[0].Type)
	}
	if resp.Output[0].Content[0].Text != "Hello! How can I help you?" {
		t.Errorf("unexpected text: %s", resp.Output[0].Content[0].Text)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage to be set")
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected input_tokens 10, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 8 {
		t.Errorf("expected output_tokens 8, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 18 {
		t.Errorf("expected total_tokens 18, got %d", resp.Usage.TotalTokens)
	}
}

func TestConvertClaudeResponseToResponses_WithToolUse(t *testing.T) {
	input := `{
		"id": "msg_0456",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-20250514",
		"content": [
			{"type": "text", "text": "Let me check the weather."},
			{"type": "tool_use", "id": "toolu_123", "name": "get_weather", "input": {"location": "NYC"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 20, "output_tokens": 15}
	}`

	out, err := ConvertClaudeResponseToResponses([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp ResponsesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(resp.Output) != 2 {
		t.Fatalf("expected 2 output items, got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "message" {
		t.Errorf("expected first output type message, got %s", resp.Output[0].Type)
	}
	if resp.Output[1].Type != "function_call" {
		t.Errorf("expected second output type function_call, got %s", resp.Output[1].Type)
	}
	if resp.Output[1].CallID != "toolu_123" {
		t.Errorf("expected call_id toolu_123, got %s", resp.Output[1].CallID)
	}
	if resp.Output[1].Name != "get_weather" {
		t.Errorf("expected name get_weather, got %s", resp.Output[1].Name)
	}
}

func TestConvertClaudeResponseToResponses_MaxTokens(t *testing.T) {
	input := `{
		"id": "msg_0789",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-20250514",
		"content": [{"type": "text", "text": "Truncated..."}],
		"stop_reason": "max_tokens",
		"usage": {"input_tokens": 5, "output_tokens": 100}
	}`

	out, err := ConvertClaudeResponseToResponses([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp ResponsesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if resp.Status != "incomplete" {
		t.Errorf("expected status incomplete for max_tokens, got %s", resp.Status)
	}
}

func TestConvertClaudeStreamEventToResponses_MessageStart(t *testing.T) {
	event := `{
		"type": "message_start",
		"message": {
			"id": "msg_stream_123",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-20250514",
			"usage": {"input_tokens": 10, "output_tokens": 0}
		}
	}`

	out, newID, err := ConvertClaudeStreamEventToResponses("", []byte(event), "resp_initial", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newID != "msg_stream_123" {
		t.Errorf("expected newID msg_stream_123, got %s", newID)
	}

	var resp ResponsesStreamEvent
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if resp.Type != "response.created" {
		t.Errorf("expected type response.created, got %s", resp.Type)
	}
	if resp.Response == nil {
		t.Fatal("expected response to be set")
	}
	if resp.Response.ID != "msg_stream_123" {
		t.Errorf("expected response ID msg_stream_123, got %s", resp.Response.ID)
	}
	if resp.Response.Status != "in_progress" {
		t.Errorf("expected status in_progress, got %s", resp.Response.Status)
	}
}

func TestConvertClaudeStreamEventToResponses_ContentDelta(t *testing.T) {
	event := `{
		"type": "content_block_delta",
		"index": 0,
		"delta": {"type": "text_delta", "text": "Hello"}
	}`

	out, _, err := ConvertClaudeStreamEventToResponses("", []byte(event), "resp_123", "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp ResponsesStreamEvent
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if resp.Type != "response.output_text.delta" {
		t.Errorf("expected type response.output_text.delta, got %s", resp.Type)
	}
	if resp.Delta != "Hello" {
		t.Errorf("expected delta Hello, got %s", resp.Delta)
	}
}

func TestConvertClaudeStreamEventToResponses_MessageDeltaStop(t *testing.T) {
	event := `{
		"type": "message_delta",
		"delta": {"stop_reason": "end_turn"}
	}`

	out, _, err := ConvertClaudeStreamEventToResponses("", []byte(event), "resp_123", "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp ResponsesStreamEvent
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if resp.Type != "response.completed" {
		t.Errorf("expected type response.completed, got %s", resp.Type)
	}
	if resp.Response == nil {
		t.Fatal("expected response to be set")
	}
	if resp.Response.Status != "completed" {
		t.Errorf("expected status completed, got %s", resp.Response.Status)
	}
}

func TestConvertClaudeStreamEventToResponses_MessageDeltaUsage(t *testing.T) {
	event := `{
		"type": "message_delta",
		"usage": {"input_tokens": 10, "output_tokens": 25}
	}`

	out, _, err := ConvertClaudeStreamEventToResponses("", []byte(event), "resp_123", "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp ResponsesStreamEvent
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if resp.Type != "response.usage" {
		t.Errorf("expected type response.usage, got %s", resp.Type)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage to be set")
	}
	if resp.Usage.TotalTokens != 35 {
		t.Errorf("expected total_tokens 35, got %d", resp.Usage.TotalTokens)
	}
}

func TestRoundTrip_SimpleMessage(t *testing.T) {
	responsesReq := `{
		"model": "claude-sonnet-4-20250514",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Hello"}]}
		],
		"temperature": 0.5,
		"max_output_tokens": 512
	}`

	claudeBody, err := ConvertResponsesRequestToClaude([]byte(responsesReq))
	if err != nil {
		t.Fatalf("request conversion error: %v", err)
	}

	var claudeReq ClaudeRequest
	if err := json.Unmarshal(claudeBody, &claudeReq); err != nil {
		t.Fatalf("unmarshal claude request: %v", err)
	}

	if claudeReq.Model != "claude-sonnet-4-20250514" {
		t.Errorf("model mismatch: %s", claudeReq.Model)
	}
	if claudeReq.Temperature == nil || *claudeReq.Temperature != 0.5 {
		t.Errorf("temperature mismatch: %v", claudeReq.Temperature)
	}
	if claudeReq.MaxTokens == nil || *claudeReq.MaxTokens != 512 {
		t.Errorf("max_tokens mismatch: %v", claudeReq.MaxTokens)
	}
}
