package embeddings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

type fakeInvokeClient struct {
	responses [][]float32
	calls     int
}

func (f *fakeInvokeClient) InvokeModel(
	_ context.Context,
	_ *bedrockruntime.InvokeModelInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.InvokeModelOutput, error) {
	vector := f.responses[f.calls]
	f.calls++
	body, _ := json.Marshal(map[string]interface{}{"embedding": vector})
	return &bedrockruntime.InvokeModelOutput{Body: body}, nil
}

func TestSplitIntoChunks(t *testing.T) {
	chunks := SplitIntoChunks("abcdef", 2)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[0] != "ab" || chunks[1] != "cd" || chunks[2] != "ef" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestEmbedTextChunkAverage(t *testing.T) {
	client := &fakeInvokeClient{
		responses: [][]float32{
			{1, 0},
			{0, 1},
		},
	}

	vector, err := EmbedTextChunkAverage(context.Background(), client, "model", "abcd", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vector) != 2 {
		t.Fatalf("expected 2 dimensions, got %d", len(vector))
	}
	if vector[0] != 0.5 || vector[1] != 0.5 {
		t.Fatalf("unexpected averaged vector: %v", vector)
	}
}

func TestNormalizeVector(t *testing.T) {
	normalized, err := NormalizeVector([]float32{3, 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(normalized) != 2 {
		t.Fatalf("expected 2 dimensions, got %d", len(normalized))
	}
	if normalized[0] < 0.59 || normalized[0] > 0.61 {
		t.Fatalf("unexpected normalized vector: %v", normalized)
	}
}
