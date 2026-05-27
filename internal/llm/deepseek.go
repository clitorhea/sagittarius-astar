// Package llm — DeepSeek provider implementation.
// DeepSeek is OpenAI-compatible, so we use github.com/sashabaranov/go-openai
// and override the BaseURL to https://api.deepseek.com.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	openai "github.com/sashabaranov/go-openai"

	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
)

// DeepSeekProvider implements Provider using DeepSeek's OpenAI-compatible API.
type DeepSeekProvider struct {
	client *openai.Client
	model  string
}

// NewDeepSeekProvider creates an authenticated DeepSeek provider.
// apiKey must be a valid DeepSeek API key.
func NewDeepSeekProvider(apiKey, model string) (*DeepSeekProvider, error) {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "https://api.deepseek.com"

	return &DeepSeekProvider{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}, nil
}

// StreamChat streams a response from DeepSeek into tokenChan.
// The channel is always closed before this function returns.
// Returns all tool calls requested in this turn (may be > 1).
func (d *DeepSeekProvider) StreamChat(ctx context.Context, messages []Message, tools []Tool, tokenChan chan<- string) ([]ToolCall, error) {
	defer close(tokenChan)

	logger.L.Debug("deepseek: stream starting",
		"model", d.model,
		"history_len", len(messages),
	)
	tokenCount := 0
	defer func() {
		logger.L.Debug("deepseek: stream finished", "tokens_received", tokenCount)
	}()

	// Map our internal Message type to openai.ChatCompletionMessage.
	oaiMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		var role string
		switch msg.Role {
		case RoleSystem:
			role = openai.ChatMessageRoleSystem
		case RoleAssistant:
			role = openai.ChatMessageRoleAssistant
		case RoleTool:
			role = openai.ChatMessageRoleTool
		default:
			role = openai.ChatMessageRoleUser
		}

		oaiMsg := openai.ChatCompletionMessage{
			Role:       role,
			Content:    msg.Content,
			Name:       msg.ToolName,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				b, _ := json.Marshal(tc.Args)
				oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(b),
					},
				})
			}
		}
		oaiMessages = append(oaiMessages, oaiMsg)
	}

	req := openai.ChatCompletionRequest{
		Model:    d.model,
		Messages: oaiMessages,
		Stream:   true,
	}

	if len(tools) > 0 {
		for _, t := range tools {
			req.Tools = append(req.Tools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	}

	stream, err := d.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("deepseek: failed to create stream: %w", err)
	}
	defer stream.Close()

	// pendingCalls accumulates tool calls across streaming deltas.
	// DeepSeek sends them index-by-index with incremental argument JSON.
	pendingCalls := make(map[int]*struct {
		id   string
		name string
		args string
	})

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			logger.L.Error("deepseek: stream recv error", "error", err)
			return nil, fmt.Errorf("deepseek: stream error: %w", err)
		}

		if len(resp.Choices) == 0 {
			continue
		}

		choice := resp.Choices[0]

		// Accumulate all tool call deltas by index.
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			if idx == nil {
				i := 0
				idx = &i
			}
			entry := pendingCalls[*idx]
			if entry == nil {
				entry = &struct {
					id   string
					name string
					args string
				}{}
				pendingCalls[*idx] = entry
			}
			if tc.ID != "" {
				entry.id = tc.ID
			}
			if tc.Function.Name != "" {
				entry.name = tc.Function.Name
			}
			entry.args += tc.Function.Arguments
		}

		if choice.FinishReason == openai.FinishReasonToolCalls {
			var calls []ToolCall
			for i := 0; i < len(pendingCalls); i++ {
				entry, ok := pendingCalls[i]
				if !ok {
					continue
				}
				var args map[string]any
				if err := json.Unmarshal([]byte(entry.args), &args); err != nil {
					args = map[string]any{"raw": entry.args}
				}
				calls = append(calls, ToolCall{
					ID:   entry.id,
					Name: entry.name,
					Args: args,
				})
			}
			logger.L.Debug("deepseek: tool calls received", "count", len(calls))
			return calls, nil
		}

		delta := choice.Delta.Content
		if delta == "" {
			continue
		}

		tokenCount++
		select {
		case <-ctx.Done():
			logger.L.Warn("deepseek: stream cancelled by context", "tokens_received", tokenCount)
			return nil, ctx.Err()
		case tokenChan <- delta:
		}
	}

	return nil, nil
}
