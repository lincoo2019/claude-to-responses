package converter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
				Type:      "function_call",
				ID:        part.ID,
				CallID:    part.ID,
				Name:      part.Name,
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
	total := 0
	if in.InputTokens > 0 || in.OutputTokens > 0 {
		total = in.InputTokens + in.OutputTokens
	}
	return &ResponsesUsage{
		InputTokens:  in.InputTokens,
		OutputTokens: in.OutputTokens,
		TotalTokens:  total,
	}
}

type StreamContext struct {
	ResponseID      string
	ClaudeMsgID     string
	Model           string
	AccumulatedText string
	ToolCalls       []ResponsesOutputItem
	ToolArgsMap     map[string]string
	CurrentBlockType string
	CurrentToolID    string
	CurrentToolName  string
	CreatedAt       int64
}

func ConvertClaudeStreamEventToResponses(eventType string, body []byte, ctx *StreamContext) ([][]byte, error) {
	var claudeEvent ClaudeStreamEvent
	if err := jsonx.Unmarshal(body, &claudeEvent); err != nil {
		return nil, fmt.Errorf("decode claude stream event: %w", err)
	}

	switch claudeEvent.Type {
	case "message_start":
		if claudeEvent.Message != nil {
			if claudeEvent.Message.ID != "" {
				ctx.ClaudeMsgID = claudeEvent.Message.ID
			}
			if claudeEvent.Message.Model != "" {
				ctx.Model = claudeEvent.Message.Model
			}
		}
		if ctx.CreatedAt == 0 {
			ctx.CreatedAt = currentTimeUnix()
		}

		var events [][]byte

		createdEvent := ResponsesStreamEvent{
			Type:       "response.created",
			ResponseID: ctx.ResponseID,
			Response: &ResponsesEventResp{
				ID:        ctx.ResponseID,
				Object:    "response",
				Model:     ctx.Model,
				Status:    "in_progress",
				Output:    []ResponsesOutputItem{},
				Usage:     convertClaudeUsage(claudeEvent.Message.Usage),
				CreatedAd: ctx.CreatedAt,
			},
		}
		out, err := jsonx.Marshal(createdEvent)
		if err != nil {
			return nil, err
		}
		events = append(events, out)

		inProgressEvent := ResponsesStreamEvent{
			Type:       "response.in_progress",
			ResponseID: ctx.ResponseID,
			Response: &ResponsesEventResp{
				ID:        ctx.ResponseID,
				Object:    "response",
				Model:     ctx.Model,
				Status:    "in_progress",
				Output:    []ResponsesOutputItem{},
				CreatedAd: ctx.CreatedAt,
			},
		}
		out2, err := jsonx.Marshal(inProgressEvent)
		if err != nil {
			return events, err
		}
		events = append(events, out2)

		return events, nil

	case "content_block_start":
		if claudeEvent.Content != nil {
			ctx.CurrentBlockType = claudeEvent.Content.Type

			switch claudeEvent.Content.Type {
			case "text":
				msgItemID := ctx.ClaudeMsgID
				if msgItemID == "" {
					msgItemID = ctx.ResponseID + "-msg"
				}
				itemAdded := ResponsesStreamEvent{
					Type:        "response.output_item.added",
					ResponseID:  ctx.ResponseID,
					OutputIndex: claudeEvent.Index,
					Item: &ResponsesOutputItem{
						Type:    "message",
						ID:      msgItemID,
						Role:    "assistant",
						Status:  "in_progress",
						Content: []ResponsesContentPart{},
					},
				}
				out, err := jsonx.Marshal(itemAdded)
				if err != nil {
					return nil, err
				}

				partAdded := ResponsesStreamEvent{
					Type:         "response.content_part.added",
					ResponseID:   ctx.ResponseID,
					ItemID:       msgItemID,
					OutputIndex:  claudeEvent.Index,
					ContentIndex: 0,
					Part: &ResponsesContentRef{
						Type:  "output_text",
						Index: 0,
					},
				}
				out2, err := jsonx.Marshal(partAdded)
				if err != nil {
					return [][]byte{out}, err
				}

				return [][]byte{out, out2}, nil

			case "tool_use":
				ctx.CurrentToolID = claudeEvent.Content.ID
				ctx.CurrentToolName = claudeEvent.Content.Name
				if ctx.ToolArgsMap == nil {
					ctx.ToolArgsMap = make(map[string]string)
				}
				ctx.ToolArgsMap[claudeEvent.Content.ID] = ""

				itemAdded := ResponsesStreamEvent{
					Type:        "response.output_item.added",
					ResponseID:  ctx.ResponseID,
					OutputIndex: claudeEvent.Index,
					Item: &ResponsesOutputItem{
						Type:   "function_call",
						ID:     claudeEvent.Content.ID,
						CallID: claudeEvent.Content.ID,
						Name:   claudeEvent.Content.Name,
						Status: "in_progress",
					},
				}
				out, err := jsonx.Marshal(itemAdded)
				if err != nil {
					return nil, err
				}
				return [][]byte{out}, nil
			}
		}

	case "content_block_delta":
		if claudeEvent.Delta != nil {
			switch claudeEvent.Delta.Type {
			case "text_delta":
				ctx.AccumulatedText += claudeEvent.Delta.Text

				msgItemID := ctx.ClaudeMsgID
				if msgItemID == "" {
					msgItemID = ctx.ResponseID + "-msg"
				}
				deltaEvent := ResponsesStreamEvent{
					Type:         "response.output_text.delta",
					ResponseID:   ctx.ResponseID,
					ItemID:       msgItemID,
					OutputIndex:  claudeEvent.Index,
					ContentIndex: 0,
					Delta:        claudeEvent.Delta.Text,
				}
				out, err := jsonx.Marshal(deltaEvent)
				if err != nil {
					return nil, err
				}
				return [][]byte{out}, nil

			case "input_json_delta":
				if ctx.CurrentToolID != "" {
					ctx.ToolArgsMap[ctx.CurrentToolID] += claudeEvent.Delta.Text
				}

				deltaEvent := ResponsesStreamEvent{
					Type:        "response.function_call_arguments.delta",
					ResponseID:  ctx.ResponseID,
					ItemID:      ctx.CurrentToolID,
					Delta:       claudeEvent.Delta.Text,
					OutputIndex: claudeEvent.Index,
				}
				out, err := jsonx.Marshal(deltaEvent)
				if err != nil {
					return nil, err
				}
				return [][]byte{out}, nil
			}
		}

	case "content_block_stop":
		var events [][]byte

		msgItemID := ctx.ClaudeMsgID
		if msgItemID == "" {
			msgItemID = ctx.ResponseID + "-msg"
		}

		switch ctx.CurrentBlockType {
		case "text":
			textDone := ResponsesStreamEvent{
				Type:         "response.output_text.done",
				ResponseID:   ctx.ResponseID,
				ItemID:       msgItemID,
				OutputIndex:  claudeEvent.Index,
				ContentIndex: 0,
				Part: &ResponsesContentRef{
					Type: "output_text",
					Text: ctx.AccumulatedText,
				},
			}
			out, err := jsonx.Marshal(textDone)
			if err != nil {
				return nil, err
			}
			events = append(events, out)

			partDone := ResponsesStreamEvent{
				Type:         "response.content_part.done",
				ResponseID:   ctx.ResponseID,
				ItemID:       msgItemID,
				OutputIndex:  claudeEvent.Index,
				ContentIndex: 0,
				Part: &ResponsesContentRef{
					Type: "output_text",
					Text: ctx.AccumulatedText,
				},
			}
			out2, err := jsonx.Marshal(partDone)
			if err != nil {
				return events, err
			}
			events = append(events, out2)

			itemDone := ResponsesStreamEvent{
				Type:        "response.output_item.done",
				ResponseID:  ctx.ResponseID,
				ItemID:      msgItemID,
				OutputIndex: claudeEvent.Index,
				Item: &ResponsesOutputItem{
					Type:   "message",
					ID:     msgItemID,
					Role:   "assistant",
					Status: "completed",
					Content: []ResponsesContentPart{
						{Type: "output_text", Text: ctx.AccumulatedText},
					},
				},
			}
			out3, err := jsonx.Marshal(itemDone)
			if err != nil {
				return events, err
			}
			events = append(events, out3)

		case "tool_use":
			args := ""
			if ctx.CurrentToolID != "" {
				args = ctx.ToolArgsMap[ctx.CurrentToolID]
			}

			argsDone := ResponsesStreamEvent{
				Type:        "response.function_call_arguments.done",
				ResponseID:  ctx.ResponseID,
				ItemID:      ctx.CurrentToolID,
				OutputIndex: claudeEvent.Index,
				Delta:       args,
			}
			out, err := jsonx.Marshal(argsDone)
			if err != nil {
				return nil, err
			}
			events = append(events, out)

			toolItem := ResponsesOutputItem{
				Type:      "function_call",
				ID:        ctx.CurrentToolID,
				CallID:    ctx.CurrentToolID,
				Name:      ctx.CurrentToolName,
				Arguments: json.RawMessage(args),
				Status:    "completed",
			}
			ctx.ToolCalls = append(ctx.ToolCalls, toolItem)

			itemDone := ResponsesStreamEvent{
				Type:        "response.output_item.done",
				ResponseID:  ctx.ResponseID,
				ItemID:      ctx.CurrentToolID,
				OutputIndex: claudeEvent.Index,
				Item:        &toolItem,
			}
			out2, err := jsonx.Marshal(itemDone)
			if err != nil {
				return events, err
			}
			events = append(events, out2)

			ctx.CurrentToolID = ""
			ctx.CurrentToolName = ""
		}

		ctx.CurrentBlockType = ""
		return events, nil

	case "message_delta":
		var events [][]byte

		if claudeEvent.Usage != nil {
			usageEvent := ResponsesStreamEvent{
				Type:       "response.usage",
				ResponseID: ctx.ResponseID,
				Usage:      convertClaudeUsage(claudeEvent.Usage),
			}
			out, err := jsonx.Marshal(usageEvent)
			if err != nil {
				return nil, err
			}
			events = append(events, out)
		}

		if claudeEvent.Delta != nil && claudeEvent.Delta.StopReason != "" {
			outputItems := buildCurrentOutputItems(ctx)

			completedEvent := ResponsesStreamEvent{
				Type:       "response.completed",
				ResponseID: ctx.ResponseID,
				Response: &ResponsesEventResp{
					ID:        ctx.ResponseID,
					Object:    "response",
					Model:     ctx.Model,
					Status:    convertStopReason(claudeEvent.Delta.StopReason),
					Output:    outputItems,
					Usage:     convertClaudeUsage(claudeEvent.Usage),
					CreatedAd: ctx.CreatedAt,
				},
			}
			out, err := jsonx.Marshal(completedEvent)
			if err != nil {
				return events, err
			}
			events = append(events, out)
		}

		return events, nil

	case "message_stop":
		return nil, nil
	}

	return nil, nil
}

func buildCurrentOutputItems(ctx *StreamContext) []ResponsesOutputItem {
	var items []ResponsesOutputItem

	msgItemID := ctx.ClaudeMsgID
	if msgItemID == "" {
		msgItemID = ctx.ResponseID + "-msg"
	}

	if ctx.AccumulatedText != "" {
		items = append(items, ResponsesOutputItem{
			Type:    "message",
			ID:      msgItemID,
			Role:    "assistant",
			Status:  "completed",
			Content: []ResponsesContentPart{{Type: "output_text", Text: ctx.AccumulatedText}},
		})
	}

	items = append(items, ctx.ToolCalls...)

	return items
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

func ExtractResponsesEventType(payload []byte) string {
	var event struct {
		Type string `json:"type"`
	}
	if err := jsonx.Unmarshal(payload, &event); err != nil {
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

func currentTimeUnix() int64 {
	return time.Now().Unix()
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
