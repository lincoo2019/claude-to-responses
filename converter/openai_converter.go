package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	jsonx "github.com/xy200303/claude-to-responses/converter/jsonx"
)

type OpenAIChatRequest struct {
	Model       string           `json:"model"`
	Messages    []OpenAIChatMessage `json:"messages"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Tools       []OpenAITool     `json:"tools,omitempty"`
}

type OpenAIChatMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name,omitempty"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type OpenAIToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function OpenAIFunction  `json:"function"`
}

type OpenAIFunction struct {
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
}

type OpenAITool struct {
	Type     string          `json:"type"`
	Function OpenAIToolFunc  `json:"function,omitempty"`
}

type OpenAIToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type OpenAIChatResponse struct {
	ID      string             `json:"id,omitempty"`
	Object  string             `json:"object,omitempty"`
	Model   string             `json:"model,omitempty"`
	Choices []OpenAIChatChoice `json:"choices,omitempty"`
	Usage   *OpenAIChatUsage   `json:"usage,omitempty"`
}

type OpenAIChatChoice struct {
	Index        int               `json:"index,omitempty"`
	Message      *OpenAIChatMessage `json:"message,omitempty"`
	FinishReason string            `json:"finish_reason,omitempty"`
	Delta        *OpenAIChatMessage `json:"delta,omitempty"`
}

type OpenAIChatUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type OpenAIStreamChunk struct {
	ID      string             `json:"id,omitempty"`
	Object  string             `json:"object,omitempty"`
	Model   string             `json:"model,omitempty"`
	Choices []OpenAIChatChoice `json:"choices,omitempty"`
	Usage   *OpenAIChatUsage   `json:"usage,omitempty"`
}

func ConvertResponsesRequestToOpenAIChat(body []byte) ([]byte, error) {
	var req ResponsesRequest
	if err := jsonx.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("decode responses request: %w", err)
	}

	chatReq := OpenAIChatRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxOutputTokens,
		Stream:      req.Stream,
	}

	var systemTexts []string
	var messages []OpenAIChatMessage

	switch jsonx.FirstNonSpaceByte(req.Input) {
	case '"':
		var single string
		if err := jsonx.Unmarshal(req.Input, &single); err != nil {
			return nil, fmt.Errorf("decode responses string input: %w", err)
		}
		messages = append(messages, OpenAIChatMessage{Role: "user"})
		content, _ := jsonx.Marshal(single)
		messages[0].Content = content
	case '[':
		var items []ResponsesInputItem
		if err := jsonx.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("decode responses input: %w", err)
		}
		for _, item := range items {
			msg, sys, err := convertResponsesInputItemToOpenAI(item)
			if err != nil {
				return nil, err
			}
			if sys != "" {
				systemTexts = append(systemTexts, sys)
				continue
			}
			if msg != nil {
				messages = append(messages, *msg)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported responses input shape")
	}

	if len(systemTexts) > 0 {
		sysContent, _ := jsonx.Marshal(strings.Join(systemTexts, "\n\n"))
		messages = append([]OpenAIChatMessage{{
			Role:    "system",
			Content: sysContent,
		}}, messages...)
	}

	chatReq.Messages = messages

	if len(req.Tools) > 0 {
		chatReq.Tools = make([]OpenAITool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if tool.Name == "" {
				continue
			}
			chatReq.Tools = append(chatReq.Tools, OpenAITool{
				Type: "function",
				Function: OpenAIToolFunc{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
				},
			})
		}
	}

	return jsonx.Marshal(chatReq)
}

func convertResponsesInputItemToOpenAI(item ResponsesInputItem) (*OpenAIChatMessage, string, error) {
	switch item.Type {
	case "", "message":
		if item.Role == "system" || item.Role == "developer" {
			var texts []string
			for _, part := range item.Content {
				if part.Type == "input_text" || part.Type == "text" || part.Type == "output_text" {
					texts = append(texts, part.Text)
				}
			}
			return nil, strings.Join(texts, "\n"), nil
		}

		content, err := encodeResponsesContentToOpenAI(item.Content)
		if err != nil {
			return nil, "", err
		}
		return &OpenAIChatMessage{
			Role:    item.Role,
			Content: content,
		}, "", nil

	case "function_call":
		return &OpenAIChatMessage{
			Role: "assistant",
			ToolCalls: []OpenAIToolCall{{
				ID:   item.CallID,
				Type: "function",
				Function: OpenAIFunction{
					Name:      item.Name,
					Arguments: string(item.Arguments),
				},
			}},
		}, "", nil

	case "function_call_output":
		return &OpenAIChatMessage{
			Role:       "tool",
			Content:    item.Output,
			ToolCallID: item.CallID,
		}, "", nil

	default:
		return nil, "", fmt.Errorf("unsupported responses input item type %q", item.Type)
	}
}

func encodeResponsesContentToOpenAI(parts []ResponsesContentPart) (json.RawMessage, error) {
	if len(parts) == 1 && (parts[0].Type == "input_text" || parts[0].Type == "output_text" || parts[0].Type == "text") {
		return jsonx.Marshal(parts[0].Text)
	}

	var out []any
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			out = append(out, map[string]string{"type": "text", "text": part.Text})
		case "input_image", "image_url":
			out = append(out, map[string]string{"type": "image_url", "image_url": part.ImageURL})
		default:
			return nil, fmt.Errorf("unsupported responses content type %q", part.Type)
		}
	}
	return jsonx.Marshal(out)
}

func ConvertOpenAIChatResponseToResponses(body []byte) ([]byte, error) {
	var chatResp OpenAIChatResponse
	if err := jsonx.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("decode openai chat response: %w", err)
	}

	resp := ResponsesResponse{
		ID:     chatResp.ID,
		Object: "response",
		Model:  chatResp.Model,
		Status: "completed",
	}

	if len(chatResp.Choices) > 0 && chatResp.Choices[0].Message != nil {
		msg := chatResp.Choices[0].Message
		var outputItems []ResponsesOutputItem

		if len(msg.Content) > 0 || (msg.ToolCalls == nil) {
			text := extractTextFromRaw(msg.Content)
			if text != "" {
				outputItems = append(outputItems, ResponsesOutputItem{
					Type:    "message",
					ID:      chatResp.ID + "-msg",
					Role:    "assistant",
					Status:  "completed",
					Content: []ResponsesContentPart{{Type: "output_text", Text: text}},
				})
			}
		}

		for _, tc := range msg.ToolCalls {
			outputItems = append(outputItems, ResponsesOutputItem{
				Type:      "function_call",
				ID:        tc.ID,
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}

		resp.Output = outputItems

		if chatResp.Choices[0].FinishReason == "length" {
			resp.Status = "incomplete"
		}
	}

	if chatResp.Usage != nil {
		resp.Usage = &ResponsesUsage{
			InputTokens:  chatResp.Usage.PromptTokens,
			OutputTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:  chatResp.Usage.TotalTokens,
		}
	}

	return jsonx.Marshal(resp)
}

func ConvertOpenAIStreamChunkToResponses(body []byte, ctx *StreamContext) ([][]byte, error) {
	var chunk OpenAIStreamChunk
	if err := jsonx.Unmarshal(body, &chunk); err != nil {
		return nil, fmt.Errorf("decode openai stream chunk: %w", err)
	}

	if chunk.Model != "" {
		ctx.Model = chunk.Model
	}

	if len(chunk.Choices) == 0 {
		if chunk.Usage != nil {
			usageEvent := ResponsesStreamEvent{
				Type:       "response.usage",
				ResponseID: ctx.ResponseID,
				Usage: &ResponsesUsage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
					TotalTokens:  chunk.Usage.TotalTokens,
				},
			}
			out, err := jsonx.Marshal(usageEvent)
			if err != nil {
				return nil, err
			}
			return [][]byte{out}, nil
		}
		return nil, nil
	}

	choice := chunk.Choices[0]
	var events [][]byte

	if choice.Delta != nil {
		if choice.Delta.Role == "assistant" && ctx.ResponseID != "" {
			if ctx.CreatedAt == 0 {
				ctx.CreatedAt = currentTimeUnix()
			}
			createdEvent := ResponsesStreamEvent{
				Type:       "response.created",
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

			itemAdded := ResponsesStreamEvent{
				Type:        "response.output_item.added",
				ResponseID:  ctx.ResponseID,
				OutputIndex: 0,
				Item: &ResponsesOutputItem{
					Type:    "message",
					ID:      ctx.ResponseID + "-msg",
					Role:    "assistant",
					Status:  "in_progress",
					Content: []ResponsesContentPart{},
				},
			}
			out3, err := jsonx.Marshal(itemAdded)
			if err != nil {
				return events, err
			}
			events = append(events, out3)

			partAdded := ResponsesStreamEvent{
				Type:         "response.content_part.added",
				ResponseID:   ctx.ResponseID,
				ItemID:       ctx.ResponseID + "-msg",
				OutputIndex:  0,
				ContentIndex: 0,
				Part: &ResponsesContentRef{
					Type:  "output_text",
					Index: 0,
				},
			}
			out4, err := jsonx.Marshal(partAdded)
			if err != nil {
				return events, err
			}
			events = append(events, out4)

			return events, nil
		}

		if len(choice.Delta.ToolCalls) > 0 {
			tc := choice.Delta.ToolCalls[0]
			if tc.ID != "" {
				ctx.CurrentToolID = tc.ID
				ctx.CurrentToolName = tc.Function.Name
				if ctx.ToolArgsMap == nil {
					ctx.ToolArgsMap = make(map[string]string)
				}
				ctx.ToolArgsMap[tc.ID] = ""

				itemAdded := ResponsesStreamEvent{
					Type:        "response.output_item.added",
					ResponseID:  ctx.ResponseID,
					OutputIndex: 1,
					Item: &ResponsesOutputItem{
						Type:   "function_call",
						ID:     tc.ID,
						CallID: tc.ID,
						Name:   tc.Function.Name,
						Status: "in_progress",
					},
				}
				out, err := jsonx.Marshal(itemAdded)
				if err != nil {
					return nil, err
				}
				return [][]byte{out}, nil
			}

			if tc.Function.Arguments != "" {
				if ctx.CurrentToolID != "" {
					ctx.ToolArgsMap[ctx.CurrentToolID] += tc.Function.Arguments
				}
				deltaEvent := ResponsesStreamEvent{
					Type:        "response.function_call_arguments.delta",
					ResponseID:  ctx.ResponseID,
					ItemID:      ctx.CurrentToolID,
					Delta:       tc.Function.Arguments,
					OutputIndex: 1,
				}
				out, err := jsonx.Marshal(deltaEvent)
				if err != nil {
					return nil, err
				}
				return [][]byte{out}, nil
			}
		}

		if choice.Delta.Content != nil && len(choice.Delta.Content) > 0 {
			text := extractTextFromRaw(choice.Delta.Content)
			if text != "" {
				ctx.AccumulatedText += text
				deltaEvent := ResponsesStreamEvent{
					Type:         "response.output_text.delta",
					ResponseID:   ctx.ResponseID,
					ItemID:       ctx.ResponseID + "-msg",
					OutputIndex:  0,
					ContentIndex: 0,
					Delta:        text,
				}
				out, err := jsonx.Marshal(deltaEvent)
				if err != nil {
					return nil, err
				}
				return [][]byte{out}, nil
			}
		}
	}

	if choice.FinishReason != "" && choice.FinishReason != "null" {
		if ctx.AccumulatedText != "" {
			textDone := ResponsesStreamEvent{
				Type:         "response.output_text.done",
				ResponseID:   ctx.ResponseID,
				ItemID:       ctx.ResponseID + "-msg",
				OutputIndex:  0,
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
				ItemID:       ctx.ResponseID + "-msg",
				OutputIndex:  0,
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
				ItemID:      ctx.ResponseID + "-msg",
				OutputIndex: 0,
				Item: &ResponsesOutputItem{
					Type:   "message",
					ID:     ctx.ResponseID + "-msg",
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
		}

		if ctx.CurrentToolID != "" {
			args := ""
			if ctx.ToolArgsMap != nil {
				args = ctx.ToolArgsMap[ctx.CurrentToolID]
			}
			argsDone := ResponsesStreamEvent{
				Type:        "response.function_call_arguments.done",
				ResponseID:  ctx.ResponseID,
				ItemID:      ctx.CurrentToolID,
				OutputIndex: 1,
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
				OutputIndex: 1,
				Item:        &toolItem,
			}
			out2, err := jsonx.Marshal(itemDone)
			if err != nil {
				return events, err
			}
			events = append(events, out2)
		}

		status := "completed"
		if choice.FinishReason == "length" {
			status = "incomplete"
		}

		outputItems := buildCurrentOutputItems(ctx)
		completedEvent := ResponsesStreamEvent{
			Type:       "response.completed",
			ResponseID: ctx.ResponseID,
			Response: &ResponsesEventResp{
				ID:        ctx.ResponseID,
				Object:    "response",
				Model:     ctx.Model,
				Status:    status,
				Output:    outputItems,
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
}

func extractTextFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := jsonx.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := jsonx.Unmarshal(raw, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}

func ReplaceModelInOpenAIChatRequest(body []byte, newModel string) ([]byte, error) {
	var req map[string]json.RawMessage
	if err := jsonx.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	modelBytes, err := jsonx.Marshal(newModel)
	if err != nil {
		return nil, err
	}
	req["model"] = modelBytes
	return jsonx.Marshal(req)
}
