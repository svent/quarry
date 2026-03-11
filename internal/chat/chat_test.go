package chat

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/svent/quarry/internal/datasource"
)

func TestBuildSystemPrompt_IndexOnly(t *testing.T) {
	datasources := []*datasource.Datasource{
		{Type: "index", Name: "zo-docs", Description: "Documentation for the zo CLI tool."},
		{Type: "index", Name: "api-docs", Description: "API reference documentation."},
	}

	prompt := buildSystemPrompt(datasources, false)

	if !strings.Contains(prompt, "You are a research assistant") {
		t.Error("missing system prompt header")
	}
	if !strings.Contains(prompt, `"zo-docs" (index): Documentation for the zo CLI tool.`) {
		t.Error("missing zo-docs datasource")
	}
	if !strings.Contains(prompt, `"api-docs" (index): API reference documentation.`) {
		t.Error("missing api-docs datasource")
	}
	if !strings.Contains(prompt, "Use list_keywords") {
		t.Error("missing list_keywords guidance")
	}
	if !strings.Contains(prompt, "Use fetch_content") {
		t.Error("missing fetch_content guidance")
	}
	if !strings.Contains(prompt, "Use lookup_keywords before fetch_content") {
		t.Error("missing ordering rule")
	}
	if !strings.Contains(prompt, "lookup_keywords will also use semantic fallback") {
		t.Error("missing semantic fallback guidance")
	}
	if !strings.Contains(prompt, "If you cannot find the answer, say you do not know") {
		t.Error("missing fallback rule")
	}
	// Should NOT contain tools-type guidance.
	if strings.Contains(prompt, "Tool guidance for tools datasources") {
		t.Error("should not contain tools guidance for index-only config")
	}
}

func TestBuildSystemPrompt_ToolsOnly(t *testing.T) {
	datasources := []*datasource.Datasource{
		{Type: "tools", Name: "my-code", Description: "Source code.", Tools: []string{"list_files", "read_file", "grep"}},
	}

	prompt := buildSystemPrompt(datasources, false)

	if !strings.Contains(prompt, `"my-code" (tools: list_files, read_file, grep): Source code.`) {
		t.Error("missing my-code datasource with tools listing")
	}
	if !strings.Contains(prompt, "Tool guidance for tools datasources") {
		t.Error("missing tools guidance section")
	}
	if !strings.Contains(prompt, "Use list_files") {
		t.Error("missing list_files guidance")
	}
	if !strings.Contains(prompt, "Use read_file") {
		t.Error("missing read_file guidance")
	}
	if !strings.Contains(prompt, "Use grep") {
		t.Error("missing grep guidance")
	}
	// Should NOT contain index-type guidance.
	if strings.Contains(prompt, "Tool guidance for index datasources") {
		t.Error("should not contain index guidance for tools-only config")
	}
}

func TestBuildSystemPrompt_Mixed(t *testing.T) {
	datasources := []*datasource.Datasource{
		{Type: "index", Name: "zo-docs", Description: "Documentation."},
		{Type: "tools", Name: "src", Description: "Source code.", Tools: []string{"list_files", "read_file"}},
	}

	prompt := buildSystemPrompt(datasources, false)

	if !strings.Contains(prompt, "Tool guidance for index datasources") {
		t.Error("missing index guidance section")
	}
	if !strings.Contains(prompt, "Tool guidance for tools datasources") {
		t.Error("missing tools guidance section")
	}
	if !strings.Contains(prompt, `"zo-docs" (index)`) {
		t.Error("missing zo-docs with index label")
	}
	if !strings.Contains(prompt, `"src" (tools: list_files, read_file)`) {
		t.Error("missing src with tools label")
	}
}

func TestBuildHistoryMessages(t *testing.T) {
	history := []ChatHistoryItem{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "  "}, // empty after trim, should be skipped
		{Role: "user", Content: "How?"},
	}

	messages := buildHistoryMessages(history)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	if messages[0].Role != "user" {
		t.Errorf("expected user role, got %s", messages[0].Role)
	}
	if messages[1].Role != "assistant" {
		t.Errorf("expected assistant role, got %s", messages[1].Role)
	}
	if messages[2].Role != "user" {
		t.Errorf("expected user role, got %s", messages[2].Role)
	}
}

func TestBuildHistoryMessages_Empty(t *testing.T) {
	messages := buildHistoryMessages(nil)
	if len(messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(messages))
	}
}

func TestExtractConverseMessage_NilOutput(t *testing.T) {
	_, err := extractConverseMessage(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractConverseMessage_NilMember(t *testing.T) {
	_, err := extractConverseMessage(&bedrockruntime.ConverseOutput{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractConverseMessage_MessageOutput(t *testing.T) {
	msg := types.Message{Role: types.ConversationRoleAssistant}
	got, err := extractConverseMessage(&bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{Value: msg},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Role != types.ConversationRoleAssistant {
		t.Fatalf("expected assistant role, got %s", got.Role)
	}
}
