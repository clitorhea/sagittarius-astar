// Package llm — Gemini provider implementation using the official
// Google Gen AI Go SDK (google.golang.org/genai).
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
)

// GeminiProvider implements Provider using Google's Gemini API.
type GeminiProvider struct {
	client *genai.Client
	model  string
}

// NewGeminiProvider creates an authenticated Gemini provider.
// apiKey must be a valid Google AI Studio API key.
func NewGeminiProvider(apiKey, model string) (*GeminiProvider, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: failed to create client: %w", err)
	}

	return &GeminiProvider{
		client: client,
		model:  model,
	}, nil
}

// StreamChat streams a response from Gemini into tokenChan.
// The channel is always closed before this function returns.
// Returns all function calls requested in this turn (may be > 1).
//
// google.golang.org/genai v1.x returns iter.Seq2[*genai.GenerateContentResponse, error]
// from GenerateContentStream. We consume it with the Go 1.23 range-over-func pattern.
func (g *GeminiProvider) StreamChat(ctx context.Context, messages []Message, tools []Tool, tokenChan chan<- string) ([]ToolCall, string, error) {
	defer close(tokenChan)

	logger.L.Debug("gemini: stream starting",
		"model", g.model,
		"history_len", len(messages),
	)
	tokenCount := 0
	defer func() {
		logger.L.Debug("gemini: stream finished", "tokens_received", tokenCount)
	}()

	// Build conversation contents from message history.
	var contents []*genai.Content
	var systemInstruction *genai.Content

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			// Gemini handles the system prompt as a separate config field,
			// not as a conversation turn.
			systemInstruction = genai.NewContentFromText(msg.Content, genai.RoleUser)
		case RoleUser:
			contents = append(contents, genai.NewContentFromText(msg.Content, genai.RoleUser))
		case RoleAssistant:
			var parts []*genai.Part
			if msg.ReasoningContent != "" {
				parts = append(parts, &genai.Part{
					Thought: true,
					Text:    msg.ReasoningContent,
				})
			}
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					parts = append(parts, &genai.Part{
						FunctionCall: &genai.FunctionCall{
							Name: tc.Name,
							Args: tc.Args,
						},
					})
				}
			} else if msg.Content != "" {
				parts = append(parts, &genai.Part{
					Text: msg.Content,
				})
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  genai.RoleModel,
					Parts: parts,
				})
			}
		case RoleTool:
			var response map[string]any
			if err := json.Unmarshal([]byte(msg.Content), &response); err != nil {
				response = map[string]any{"output": msg.Content}
			}
			contents = append(contents, genai.NewContentFromFunctionResponse(msg.ToolName, response, genai.RoleUser))
		}
	}

	cfg := &genai.GenerateContentConfig{}
	if systemInstruction != nil {
		cfg.SystemInstruction = systemInstruction
	}
	if len(tools) > 0 {
		var decls []*genai.FunctionDeclaration
		for _, t := range tools {
			decls = append(decls, &genai.FunctionDeclaration{
				Name:                 t.Name,
				Description:          t.Description,
				ParametersJsonSchema: t.Parameters,
			})
		}
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
		cfg.ToolConfig = &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAuto,
			},
		}
	}

	// Collect all function calls across the entire streamed response.
	var collectedCalls []ToolCall
	var reasoningBuf strings.Builder

	for resp, err := range g.client.Models.GenerateContentStream(ctx, g.model, contents, cfg) {
		if err != nil {
			logger.L.Error("gemini: stream error", "error", err)
			return nil, "", fmt.Errorf("gemini: stream error: %w", err)
		}

		for _, cand := range resp.Candidates {
			if cand.Content == nil {
				continue
			}
			for _, part := range cand.Content.Parts {
				if part.Thought {
					if part.Text != "" {
						reasoningBuf.WriteString(part.Text)
						select {
						case <-ctx.Done():
							logger.L.Warn("gemini: stream cancelled by context", "tokens_received", tokenCount)
							return nil, "", ctx.Err()
						case tokenChan <- "\x00" + part.Text:
						}
					}
					continue
				}
				if part.FunctionCall != nil {
					collectedCalls = append(collectedCalls, ToolCall{
						ID:   part.FunctionCall.ID,
						Name: part.FunctionCall.Name,
						Args: part.FunctionCall.Args,
					})
					// Do not stream text when tool calls are being made.
					continue
				}
				if part.Text != "" {
					tokenCount++
					select {
					case <-ctx.Done():
						logger.L.Warn("gemini: stream cancelled by context", "tokens_received", tokenCount)
						return nil, "", ctx.Err()
					case tokenChan <- part.Text:
					}
				}
			}
		}
	}

	if len(collectedCalls) > 0 {
		logger.L.Debug("gemini: tool calls received", "count", len(collectedCalls))
		return collectedCalls, reasoningBuf.String(), nil
	}
	return nil, reasoningBuf.String(), nil
}
