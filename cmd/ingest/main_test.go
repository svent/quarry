package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

type fakeConverseClient struct {
	input *bedrockruntime.ConverseInput
}

func (f *fakeConverseClient) Converse(
	_ context.Context,
	input *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	f.input = input
	return &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: `{"filename":"./doc.md","content":"summary","keywords":["doc"]}`},
				},
			},
		},
	}, nil
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

func TestIndexFileUsesFlexServiceTier(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	client := &fakeConverseClient{}

	entry, err := indexFile(context.Background(), client, "model", filePath, dir, []byte("content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Filename != "./doc.md" {
		t.Fatalf("expected normalized filename, got %q", entry.Filename)
	}
	if client.input == nil {
		t.Fatal("expected converse input")
	}
	if client.input.ServiceTier == nil {
		t.Fatal("expected service tier")
	}
	if client.input.ServiceTier.Type != types.ServiceTierTypeFlex {
		t.Fatalf("expected flex service tier, got %q", client.input.ServiceTier.Type)
	}
}
