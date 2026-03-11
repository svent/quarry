package main

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

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
