package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	jsonx "github.com/xy200303/claude-to-responses/converter/jsonx"
)

func ConvertClaudeResponseToResponses(body []byte) ([]byte, error) {
	var claudeResp ClaudeResponse
	if err := jsonx.Unmarshal(body, &claudeResp); err != nil {
		return nil, fmt.Errorf("decode claude response: %w", err)
	}

	resp := ResponsesResponse{
		ID:     claudeResp.ID,
		Object: "response",
		Model:  claudeResp.Model,
		Status: convertStopReason(claudeResp.StopReason),
		Usage:  convertClaudeUsage(claudeResp.Usage),
	}

	outputItems := buildResponsesOutputItems(claudeResp)
	resp.Output = outputItems

	return jsonx.Marshal(resp)
}

func buildResponsesOutputItems(claudeResp ClaudeResponse) []ResponsesOutputItem {
	var items []ResponsesOutputItem
	var textParts []ResponsesContentPart
	var functionCalls []ResponsesOutputItem

	for _, part := range claudeResp.Content {
		switch part.Type {
		case "text":
			textParts = append(textParts, ResponsesContentPart{
				Type: "output_text",
				Text: part.Text,
			})
		case "tool_use":
			functionCalls = append(functionCalls, ResponsesOutputItem{
				Type:   "function_call",
				ID:     part.ID,
				CallID: part.ID,
				Name:   part.Name,
				Arguments: part.Input,
			})
		case "tool_result":
			functionCalls = append(functionCalls, ResponsesOutputItem{
				Type:   "function_call_output",
				CallID: part.ToolUseID,
				Name:   part.Name,
				Output: part.Content,
			})
		}
	}

	if len(textParts) > 0 {
		items = append(items, ResponsesOutputItem{
			Type:    "message",
			ID:      claudeResp.ID + "-msg",
			Role:    "assistant",
			Status:  "completed",
			Content: textParts,
		})
	}

	items = append(items, functionCalls...)

	return items
}

func convertStopReason(reason string) string {
	switch reason {
	case "end_turn", "stop":
		return "completed"
	case "max_tokens":
		return "incomplete"
	case "tool_use":
		return "completed"
	case "":
		return "completed"
	default:
		return reason
	}
}

func convertClaudeUsage(in *ClaudeUsage) *ResponsesUsage {
	if in == nil {
		return nil
	}
	total := in.InputTokens + in.OutputTokens
	return &ResponsesUsage{
		InputTokens:  in.InputTokens,
		OutputTokens: in.OutputTokens,
		TotalTokens:  total,
	}
}

func ConvertClaudeStreamEventToResponses(eventType string, body []byte, responseID string, model string) ([]byte, string, error) {
	var claudeEvent ClaudeStreamEvent
	if err := jsonx.Unmarshal(body, &claudeEvent); err != nil {
		return nil, "", fmt.Errorf("decode claude stream event: %w", err)
	}

	switch claudeEvent.Type {
	case "message_start":
		newRespID := responseID
		newModel := model
		if claudeEvent.Message != nil {
			if claudeEvent.Message.ID != "" {
				newRespID = claudeEvent.Message.ID
			}
			if claudeEvent.Message.Model != "" {
				newModel = claudeEvent.Message.Model
			}
		}
		respEvent := ResponsesStreamEvent{
			Type:       "response.created",
			ResponseID: newRespID,
			Response: &struct {
				ID     string          `json:"id,omitempty"`
				Object string          `json:"object,omitempty"`
				Model  string          `json:"model,omitempty"`
				Status string          `json:"status,omitempty"`
				Usage  *ResponsesUsage `json:"usage,omitempty"`
			}{
				ID:     newRespID,
				Object: "response",
				Model:  newModel,
				Status: "in_progress",
				Usage:  convertClaudeUsage(claudeEvent.Message.Usage),
			},
		}
		out, err := jsonx.Marshal(respEvent)
		return out, newRespID, err

	case "content_block_start":
		if claudeEvent.Content != nil && claudeEvent.Content.Type == "tool_use" {
			itemEvent := ResponsesStreamEvent{
				Type:       "response.output_item.added",
				ResponseID: responseID,
				ItemID:     claudeEvent.Content.ID,
				OutputIndex: claudeEvent.Index,
			}
			out, err := jsonx.Marshal(itemEvent)
			return out, responseID, err
		}
		if claudeEvent.Content != nil && claudeEvent.Content.Type == "text" {
			itemEvent := ResponsesStreamEvent{
				Type:        "response.output_item.added",
				ResponseID:  responseID,
				ItemID:      responseID + "-msg",
				OutputIndex: claudeEvent.Index,
			}
			out, err := jsonx.Marshal(itemEvent)
			return out, responseID, err
		}

	case "content_block_delta":
		if claudeEvent.Delta != nil && claudeEvent.Delta.Type == "text_delta" {
			deltaEvent := ResponsesStreamEvent{
				Type:         "response.output_text.delta",
				ResponseID:   responseID,
				ItemID:       responseID + "-msg",
				Delta:        claudeEvent.Delta.Text,
				OutputIndex:  claudeEvent.Index,
				ContentIndex: 0,
			}
			out, err := jsonx.Marshal(deltaEvent)
			return out, responseID, err
		}
		if claudeEvent.Delta != nil && claudeEvent.Delta.Type == "input_json_delta" {
			deltaEvent := ResponsesStreamEvent{
				Type:        "response.function_call_arguments.delta",
				ResponseID:  responseID,
				ItemID:      "",
				Delta:       claudeEvent.Delta.Text,
				OutputIndex: claudeEvent.Index,
			}
			out, err := jsonx.Marshal(deltaEvent)
			return out, responseID, err
		}

	case "content_block_stop":
		stopEvent := ResponsesStreamEvent{
			Type:        "response.output_text.done",
			ResponseID:  responseID,
			ItemID:      responseID + "-msg",
			OutputIndex: claudeEvent.Index,
		}
		out, err := jsonx.Marshal(stopEvent)
		return out, responseID, err

	case "message_delta":
		if claudeEvent.Usage != nil {
			usageEvent := ResponsesStreamEvent{
				Type:       "response.usage",
				ResponseID: responseID,
				Usage:      convertClaudeUsage(claudeEvent.Usage),
			}
			out, err := jsonx.Marshal(usageEvent)
			return out, responseID, err
		}
		if claudeEvent.Delta != nil && claudeEvent.Delta.StopReason != "" {
			completedEvent := ResponsesStreamEvent{
				Type:       "response.completed",
				ResponseID: responseID,
				Response: &struct {
					ID     string          `json:"id,omitempty"`
					Object string          `json:"object,omitempty"`
					Model  string          `json:"model,omitempty"`
					Status string          `json:"status,omitempty"`
					Usage  *ResponsesUsage `json:"usage,omitempty"`
				}{
					ID:     responseID,
					Object: "response",
					Model:  model,
					Status: convertStopReason(claudeEvent.Delta.StopReason),
				},
			}
			out, err := jsonx.Marshal(completedEvent)
			return out, responseID, err
		}

	case "message_stop":
		return nil, responseID, nil
	}

	return nil, responseID, nil
}

func IsClaudeStreamDone(body []byte) bool {
	var event ClaudeStreamEvent
	if err := jsonx.Unmarshal(body, &event); err != nil {
		return false
	}
	return event.Type == "message_stop"
}

func ExtractClaudeStreamEventType(body []byte) string {
	var event struct {
		Type string `json:"type"`
	}
	if err := jsonx.Unmarshal(body, &event); err != nil {
		return ""
	}
	return event.Type
}

func GenerateResponseID() string {
	return fmt.Sprintf("resp_%s", generateRandomString(24))
}

func generateRandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[i%len(letters)]
	}
	return string(b)
}

func unmarshalClaudeContent(raw json.RawMessage) ([]ClaudeContentPart, error) {
	switch jsonx.FirstNonSpaceByte(raw) {
	case '"':
		var text string
		if err := jsonx.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("decode claude text content: %w", err)
		}
		return []ClaudeContentPart{{Type: "text", Text: text}}, nil
	case '[':
		var parts []ClaudeContentPart
		if err := jsonx.Unmarshal(raw, &parts); err != nil {
			return nil, fmt.Errorf("decode claude content: %w", err)
		}
		return parts, nil
	default:
		return nil, fmt.Errorf("unsupported claude content shape")
	}
}

func marshalClaudeContent(parts []ClaudeContentPart) (json.RawMessage, error) {
	if len(parts) == 1 && parts[0].Type == "text" {
		return jsonx.Marshal(parts[0].Text)
	}
	return jsonx.Marshal(parts)
}

func extractTextFromClaudeContent(raw json.RawMessage) string {
	var text string
	if err := jsonx.Unmarshal(raw, &text); err == nil {
		return text
	}
	parts, err := unmarshalClaudeContent(raw)
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}
