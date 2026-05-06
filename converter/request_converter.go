package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	jsonx "github.com/xy200303/claude-to-responses/converter/jsonx"
)

func ReplaceModelInClaudeRequest(body []byte, newModel string) ([]byte, error) {
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

func ConvertResponsesRequestToClaude(body []byte) ([]byte, error) {
	var req ResponsesRequest
	if err := jsonx.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("decode responses request: %w", err)
	}

	claudeReq := ClaudeRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxOutputTokens,
		Stream:      req.Stream,
		Metadata:    req.Metadata,
	}

	var systemTexts []string
	var messages []ClaudeMessage

	switch jsonx.FirstNonSpaceByte(req.Input) {
	case '"':
		var single string
		if err := jsonx.Unmarshal(req.Input, &single); err != nil {
			return nil, fmt.Errorf("decode responses string input: %w", err)
		}
		messages = append(messages, ClaudeMessage{
			Role: "user",
		})
		content, _ := jsonx.Marshal(single)
		messages[0].Content = content
	case '[':
		var items []ResponsesInputItem
		if err := jsonx.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("decode responses input: %w", err)
		}
		for _, item := range items {
			msg, sys, err := convertResponsesInputItem(item)
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
		raw, err := jsonx.Marshal(strings.Join(systemTexts, "\n\n"))
		if err != nil {
			return nil, err
		}
		claudeReq.System = raw
	}

	if req.Instructions != nil && len(req.Instructions) > 0 {
		var instrStr string
		switch jsonx.FirstNonSpaceByte(req.Instructions) {
		case '"':
			if err := jsonx.Unmarshal(req.Instructions, &instrStr); err != nil {
				return nil, fmt.Errorf("decode instructions string: %w", err)
			}
		default:
			instrStr = string(req.Instructions)
		}
		if instrStr != "" {
			if len(systemTexts) > 0 {
				existing := string(claudeReq.System)
				combined, err := jsonx.Marshal(instrStr + "\n\n" + existing)
				if err != nil {
					return nil, err
				}
				claudeReq.System = combined
			} else {
				raw, err := jsonx.Marshal(instrStr)
				if err != nil {
					return nil, err
				}
				claudeReq.System = raw
			}
		}
	}

	claudeReq.Messages = messages

	if len(req.Tools) > 0 {
		claudeReq.Tools = make([]ClaudeTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if tool.Name == "" {
				continue
			}
			claudeReq.Tools = append(claudeReq.Tools, ClaudeTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.Parameters,
			})
		}
	}

	return jsonx.Marshal(claudeReq)
}

func convertResponsesInputItem(item ResponsesInputItem) (*ClaudeMessage, string, error) {
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

		content, err := encodeResponsesContentToClaude(item.Role, item.Content)
		if err != nil {
			return nil, "", err
		}
		return &ClaudeMessage{
			Role:    item.Role,
			Content: content,
		}, "", nil

	case "function_call":
		raw, err := jsonx.Marshal([]ClaudeContentPart{{
			Type:  "tool_use",
			ID:    item.CallID,
			Name:  item.Name,
			Input: item.Arguments,
		}})
		if err != nil {
			return nil, "", err
		}
		return &ClaudeMessage{
			Role:    "assistant",
			Content: raw,
		}, "", nil

	case "function_call_output":
		toolResultContent, err := marshalClaudeToolResultContent(item.Output)
		if err != nil {
			return nil, "", err
		}
		raw, err := jsonx.Marshal([]ClaudeContentPart{{
			Type:      "tool_result",
			ToolUseID: item.CallID,
			Content:   toolResultContent,
		}})
		if err != nil {
			return nil, "", err
		}
		return &ClaudeMessage{
			Role:    "user",
			Content: raw,
		}, "", nil

	default:
		return nil, "", fmt.Errorf("unsupported responses input item type %q", item.Type)
	}
}

func encodeResponsesContentToClaude(role string, parts []ResponsesContentPart) (json.RawMessage, error) {
	if len(parts) == 1 && (parts[0].Type == "input_text" || parts[0].Type == "output_text" || parts[0].Type == "text") {
		return jsonx.Marshal(parts[0].Text)
	}

	out := make([]ClaudeContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			out = append(out, ClaudeContentPart{Type: "text", Text: part.Text})
		case "input_image", "image_url":
			if strings.HasPrefix(part.ImageURL, "data:") {
				mimeType, data := parseBase64Image(part.ImageURL)
				out = append(out, ClaudeContentPart{
					Type: "image",
					Source: &ClaudeImageSource{
						Type:      "base64",
						MediaType: mimeType,
						Data:      data,
					},
				})
			} else {
				out = append(out, ClaudeContentPart{
					Type: "image",
					Source: &ClaudeImageSource{
						Type: "url",
						URL:  part.ImageURL,
					},
				})
			}
		default:
			return nil, fmt.Errorf("unsupported responses content type %q", part.Type)
		}
	}
	return jsonx.Marshal(out)
}

func parseBase64Image(dataURL string) (mimeType, data string) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", dataURL
	}
	rest := dataURL[5:]
	sep := strings.Index(rest, ";")
	if sep < 0 {
		return "", dataURL
	}
	mimeType = rest[:sep]
	base64Part := rest[sep+1:]
	if strings.HasPrefix(base64Part, "base64,") {
		data = base64Part[7:]
	}
	return
}

func marshalClaudeToolResultContent(output json.RawMessage) (json.RawMessage, error) {
	if len(output) == 0 {
		return jsonx.Marshal("")
	}
	switch jsonx.FirstNonSpaceByte(output) {
	case '"':
		return output, nil
	default:
		return jsonx.Marshal(string(output))
	}
}
