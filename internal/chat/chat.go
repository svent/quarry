package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/svent/quarry/internal/bedrock"
	"github.com/svent/quarry/internal/config"
	"github.com/svent/quarry/internal/datasource"
)

const MaxToolSteps = 24

// buildSystemPrompt constructs the system prompt with available datasources.
func buildSystemPrompt(datasources []*datasource.Datasource, debug bool) string {
	var parts []string
	parts = append(parts, "You are a research assistant. Use tools to find information.")
	parts = append(parts, "")

	// List datasources with type information.
	var hasIndex, hasTools bool
	parts = append(parts, "Available datasources:")
	for _, ds := range datasources {
		switch ds.Type {
		case "index":
			hasIndex = true
			parts = append(parts, fmt.Sprintf("- \"%s\" (index): %s", ds.Name, ds.Description))
		case "tools":
			hasTools = true
			toolList := strings.Join(ds.Tools, ", ")
			parts = append(parts, fmt.Sprintf("- \"%s\" (tools: %s): %s", ds.Name, toolList, ds.Description))
		}
	}

	if hasIndex {
		parts = append(parts, "")
		parts = append(parts, "Tool guidance for index datasources:")
		parts = append(parts, "- Use list_keywords to see available keywords for a datasource.")
		parts = append(parts, "- Use lookup_keywords to find relevant docs by keyword.")
		parts = append(parts, "- If keywords are unknown, lookup_keywords will also use semantic fallback.")
		parts = append(parts, "- Use fetch_content to read the full document text.")
		parts = append(parts, "- Use lookup_keywords before fetch_content.")
		parts = append(parts, "- Use filenames exactly as provided by lookup_keywords.")
	}

	if hasTools {
		parts = append(parts, "")
		parts = append(parts, "Tool guidance for tools datasources:")
		parts = append(parts, "- Use list_files to discover files by path or glob pattern (supports **).")
		parts = append(parts, "- Use read_file to read file contents.")
		parts = append(parts, "- Use grep to search file contents by regex pattern (smart-case).")
	}

	parts = append(parts, "")
	parts = append(parts, "Rules:")
	parts = append(parts, "- Always specify the datasource name when using tools.")
	parts = append(parts, "- Use filenames and paths exactly as returned by tools.")
	parts = append(parts, "- If you cannot find the answer, say you do not know.")
	parts = append(parts, "- When ready, respond with a normal answer (no tool call).")

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] SYSTEM PROMPT: %v\n", strings.Join(parts, "\n"))
	}

	return strings.Join(parts, "\n")
}

// buildHistoryMessages converts ChatHistoryItems into Bedrock messages.
func buildHistoryMessages(history []ChatHistoryItem) []types.Message {
	var messages []types.Message
	for _, entry := range history {
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		var role types.ConversationRole
		if entry.Role == "assistant" {
			role = types.ConversationRoleAssistant
		} else {
			role = types.ConversationRoleUser
		}
		messages = append(messages, types.Message{
			Role: role,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: content},
			},
		})
	}
	return messages
}

// extractTextContent extracts all text content from Bedrock content blocks.
func extractTextContent(content []types.ContentBlock) string {
	var parts []string
	for _, block := range content {
		if textBlock, ok := block.(*types.ContentBlockMemberText); ok {
			parts = append(parts, textBlock.Value)
		}
	}
	return strings.Join(parts, "")
}

// extractToolUses extracts all tool use blocks from Bedrock content blocks.
func extractToolUses(content []types.ContentBlock) []types.ToolUseBlock {
	var toolUses []types.ToolUseBlock
	for _, block := range content {
		if toolBlock, ok := block.(*types.ContentBlockMemberToolUse); ok {
			toolUses = append(toolUses, toolBlock.Value)
		}
	}
	return toolUses
}

// parseToolInput extracts the input map from a tool use block's document.
func parseToolInput(input interface{}) (map[string]interface{}, error) {
	if input == nil {
		return map[string]interface{}{}, nil
	}

	// The input from Bedrock comes as a document.Interface. Try to marshal
	// it to JSON and back to a map.
	b, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool input: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool input: %w", err)
	}

	return result, nil
}

func extractConverseMessage(output *bedrockruntime.ConverseOutput) (types.Message, error) {
	if output == nil || output.Output == nil {
		return types.Message{}, fmt.Errorf("converse response did not include a message")
	}
	msg, ok := output.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return types.Message{}, fmt.Errorf("unexpected converse output type: %T", output.Output)
	}
	return msg.Value, nil
}

// AnswerQuestion performs a non-streaming chat with tool-calling loop.
func AnswerQuestion(ctx context.Context, question string, opts AnswerOptions) (string, error) {
	datasources, err := datasource.LoadDatasources(config.DatasourcesConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to load datasources: %w", err)
	}

	client, err := bedrock.GetChatClient()
	if err != nil {
		return "", fmt.Errorf("failed to get Bedrock client: %w", err)
	}

	if opts.Debug {
		names := make([]string, len(datasources))
		for i, ds := range datasources {
			names[i] = ds.Name
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] datasources: %v\n", names)
	}

	tools := CreateToolDefinitions(datasources)
	systemPrompt := buildSystemPrompt(datasources, opts.Debug)

	historyMessages := buildHistoryMessages(opts.History)
	messages := make([]types.Message, 0, len(historyMessages)+1)
	messages = append(messages, historyMessages...)
	messages = append(messages, types.Message{
		Role: types.ConversationRoleUser,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: question},
		},
	})

	modelID, err := config.GetChatModelID()
	if err != nil {
		return "", fmt.Errorf("failed to resolve chat model ID: %w", err)
	}

	for step := range MaxToolSteps {
		if opts.Debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] running step %d\n", step)
		}

		output, err := client.Converse(ctx, &bedrockruntime.ConverseInput{
			ModelId: &modelID,
			System: []types.SystemContentBlock{
				&types.SystemContentBlockMemberText{Value: systemPrompt},
			},
			Messages: messages,
			ToolConfig: &types.ToolConfiguration{
				Tools: tools,
			},
			InferenceConfig: &types.InferenceConfiguration{
				Temperature: aws.Float32(0),
			},
		})
		if err != nil {
			return "", fmt.Errorf("Bedrock Converse error: %w", err)
		}

		responseMsg, err := extractConverseMessage(output)
		if err != nil {
			return "", fmt.Errorf("invalid converse response: %w", err)
		}
		toolUses := extractToolUses(responseMsg.Content)

		if len(toolUses) == 0 {
			content := extractTextContent(responseMsg.Content)
			if opts.Debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] received content: %s\n", content)
				fmt.Fprintln(os.Stderr, "[ END ] received content")
			}
			return strings.TrimSpace(content), nil
		}

		// Append assistant message with tool use blocks.
		messages = append(messages, responseMsg)

		// Execute each tool and create tool result message.
		var toolResultBlocks []types.ContentBlock
		for _, toolUse := range toolUses {
			inputMap, err := parseToolInput(toolUse.Input)
			if err != nil {
				inputMap = map[string]interface{}{}
			}

			result := ExecuteTool(aws.ToString(toolUse.Name), inputMap, datasources, opts.Debug)

			toolResultBlocks = append(toolResultBlocks, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					ToolUseId: toolUse.ToolUseId,
					Content: []types.ToolResultContentBlock{
						&types.ToolResultContentBlockMemberText{Value: result},
					},
				},
			})
		}

		messages = append(messages, types.Message{
			Role:    types.ConversationRoleUser,
			Content: toolResultBlocks,
		})
	}

	return "I do not know.", nil
}

// StreamAnswer performs a streaming chat with tool-calling loop.
// It returns a channel that emits StreamEvents.
func StreamAnswer(ctx context.Context, question string, opts AnswerOptions) (<-chan StreamEvent, error) {
	datasources, err := datasource.LoadDatasources(config.DatasourcesConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load datasources: %w", err)
	}

	client, err := bedrock.GetChatClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get Bedrock client: %w", err)
	}

	if opts.Debug {
		names := make([]string, len(datasources))
		for i, ds := range datasources {
			names[i] = ds.Name
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] datasources: %v\n", names)
	}

	tools := CreateToolDefinitions(datasources)
	systemPrompt := buildSystemPrompt(datasources, opts.Debug)

	historyMessages := buildHistoryMessages(opts.History)
	messages := make([]types.Message, 0, len(historyMessages)+1)
	messages = append(messages, historyMessages...)
	messages = append(messages, types.Message{
		Role: types.ConversationRoleUser,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: question},
		},
	})

	modelID, err := config.GetChatModelID()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve chat model ID: %w", err)
	}

	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)

		for step := 0; step < MaxToolSteps; step++ {
			if opts.Debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] running step %d\n", step)
			}

			if step == 0 {
				ch <- StreamEvent{Type: "thinking"}
			}

			output, err := client.ConverseStream(ctx, &bedrockruntime.ConverseStreamInput{
				ModelId: &modelID,
				System: []types.SystemContentBlock{
					&types.SystemContentBlockMemberText{Value: systemPrompt},
				},
				Messages: messages,
				ToolConfig: &types.ToolConfiguration{
					Tools: tools,
				},
				InferenceConfig: &types.InferenceConfiguration{
					Temperature: aws.Float32(0),
				},
			})
			if err != nil {
				ch <- StreamEvent{Type: "delta", Text: fmt.Sprintf("Error: %v", err)}
				ch <- StreamEvent{Type: "done"}
				return
			}

			stream := output.GetStream()

			// Accumulate the full response for tool call detection.
			var textContent strings.Builder
			type accumulatedToolUse struct {
				toolUseID string
				name      string
				inputJSON strings.Builder
			}
			var currentToolUse *accumulatedToolUse
			var toolUses []accumulatedToolUse
			var contentBlocks []types.ContentBlock
			hasToolUse := false

			for event := range stream.Events() {
				switch v := event.(type) {
				case *types.ConverseStreamOutputMemberContentBlockStart:
					if start, ok := v.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
						currentToolUse = &accumulatedToolUse{
							toolUseID: aws.ToString(start.Value.ToolUseId),
							name:      aws.ToString(start.Value.Name),
						}
						hasToolUse = true
						ch <- StreamEvent{Type: "tool_start", Name: currentToolUse.name}
					}

				case *types.ConverseStreamOutputMemberContentBlockDelta:
					switch delta := v.Value.Delta.(type) {
					case *types.ContentBlockDeltaMemberText:
						text := delta.Value
						if text != "" {
							textContent.WriteString(text)
							ch <- StreamEvent{Type: "delta", Text: text}
						}
					case *types.ContentBlockDeltaMemberToolUse:
						if currentToolUse != nil && delta.Value.Input != nil {
							currentToolUse.inputJSON.WriteString(*delta.Value.Input)
						}
					}

				case *types.ConverseStreamOutputMemberContentBlockStop:
					if currentToolUse != nil {
						toolUses = append(toolUses, *currentToolUse)
						// Build the content block for the assistant message.
						inputStr := currentToolUse.inputJSON.String()
						var inputDoc interface{}
						if inputStr != "" {
							_ = json.Unmarshal([]byte(inputStr), &inputDoc)
						}
						contentBlocks = append(contentBlocks, &types.ContentBlockMemberToolUse{
							Value: types.ToolUseBlock{
								ToolUseId: aws.String(currentToolUse.toolUseID),
								Name:      aws.String(currentToolUse.name),
								Input:     newDocument(inputDoc),
							},
						})
						currentToolUse = nil
					}

				case *types.ConverseStreamOutputMemberMessageStop:
					// Message finished.
				}
			}

			if err := stream.Close(); err != nil {
				ch <- StreamEvent{Type: "delta", Text: fmt.Sprintf("Stream error: %v", err)}
				ch <- StreamEvent{Type: "done"}
				return
			}

			if !hasToolUse {
				ch <- StreamEvent{Type: "done"}
				return
			}

			// Build assistant message with all content blocks.
			if text := textContent.String(); text != "" {
				contentBlocks = append([]types.ContentBlock{
					&types.ContentBlockMemberText{Value: text},
				}, contentBlocks...)
			}
			messages = append(messages, types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: contentBlocks,
			})

			// Execute tools and append results.
			var toolResultBlocks []types.ContentBlock
			for _, tu := range toolUses {
				inputStr := tu.inputJSON.String()
				var inputMap map[string]interface{}
				if inputStr != "" {
					_ = json.Unmarshal([]byte(inputStr), &inputMap)
				}
				if inputMap == nil {
					inputMap = map[string]interface{}{}
				}

				result := ExecuteTool(tu.name, inputMap, datasources, opts.Debug)

				toolResultBlocks = append(toolResultBlocks, &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String(tu.toolUseID),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: result},
						},
					},
				})

				ch <- StreamEvent{Type: "tool_end", Name: tu.name}
			}

			messages = append(messages, types.Message{
				Role:    types.ConversationRoleUser,
				Content: toolResultBlocks,
			})
		}

		// Max steps exhausted.
		ch <- StreamEvent{Type: "delta", Text: "I do not know."}
		ch <- StreamEvent{Type: "done"}
	}()

	return ch, nil
}
