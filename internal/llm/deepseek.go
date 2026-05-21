// Package llm — DeepSeek provider implementation.
// DeepSeek is OpenAI-compatible, so we use github.com/sashabaranov/go-openai
// and override the BaseURL to https://api.deepseek.com.
package llm

import (
	"context"
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
func (d *DeepSeekProvider) StreamChat(ctx context.Context, messages []Message, tokenChan chan<- string) error {
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
		default:
			role = openai.ChatMessageRoleUser
		}
		oaiMessages = append(oaiMessages, openai.ChatCompletionMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	req := openai.ChatCompletionRequest{
		Model:    d.model,
		Messages: oaiMessages,
		Stream:   true,
	}

	stream, err := d.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return fmt.Errorf("deepseek: failed to create stream: %w", err)
	}
	defer stream.Close()

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			logger.L.Error("deepseek: stream recv error", "error", err)
			return fmt.Errorf("deepseek: stream error: %w", err)
		}

		if len(resp.Choices) == 0 {
			continue
		}

		delta := resp.Choices[0].Delta.Content
		if delta == "" {
			continue
		}

		tokenCount++
		select {
		case <-ctx.Done():
			logger.L.Warn("deepseek: stream cancelled by context", "tokens_received", tokenCount)
			return ctx.Err()
		case tokenChan <- delta:
		}
	}
}
