package embeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

const DefaultChunkSizeRunes = 3000

// InvokeModelAPI is the minimal Bedrock runtime surface used for embedding calls.
type InvokeModelAPI interface {
	InvokeModel(
		ctx context.Context,
		params *bedrockruntime.InvokeModelInput,
		optFns ...func(*bedrockruntime.Options),
	) (*bedrockruntime.InvokeModelOutput, error)
}

// EmbedText invokes a Bedrock embedding model and returns one vector.
func EmbedText(ctx context.Context, client InvokeModelAPI, modelID, text string) ([]float32, error) {
	if client == nil {
		return nil, fmt.Errorf("embedding client is nil")
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, fmt.Errorf("embedding model ID is empty")
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embedding input text is empty")
	}

	body, err := json.Marshal(map[string]string{
		"inputText": text,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	out, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock invoke model error: %w", err)
	}
	if out == nil || len(out.Body) == 0 {
		return nil, fmt.Errorf("embedding response body is empty")
	}

	vector, err := parseEmbeddingResponse(out.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedding response: %w", err)
	}
	if len(vector) == 0 {
		return nil, fmt.Errorf("embedding response vector is empty")
	}
	return vector, nil
}

// EmbedTextChunkAverage embeds long text by chunking and averaging chunk vectors.
func EmbedTextChunkAverage(ctx context.Context, client InvokeModelAPI, modelID, text string, chunkSizeRunes int) ([]float32, error) {
	chunks := SplitIntoChunks(text, chunkSizeRunes)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("embedding input text is empty")
	}

	var sum []float64
	for i, chunk := range chunks {
		vector, err := EmbedText(ctx, client, modelID, chunk)
		if err != nil {
			return nil, fmt.Errorf("embedding chunk %d/%d failed: %w", i+1, len(chunks), err)
		}
		if len(sum) == 0 {
			sum = make([]float64, len(vector))
		}
		if len(vector) != len(sum) {
			return nil, fmt.Errorf("embedding chunk %d dimension mismatch", i+1)
		}
		for j, v := range vector {
			sum[j] += float64(v)
		}
	}

	avg := make([]float32, len(sum))
	divisor := float64(len(chunks))
	for i, v := range sum {
		avg[i] = float32(v / divisor)
	}
	return avg, nil
}

// SplitIntoChunks splits by rune count. Empty/whitespace-only text yields zero chunks.
func SplitIntoChunks(text string, chunkSizeRunes int) []string {
	if chunkSizeRunes <= 0 {
		chunkSizeRunes = DefaultChunkSizeRunes
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+chunkSizeRunes-1)/chunkSizeRunes)
	for start := 0; start < len(runes); start += chunkSizeRunes {
		end := start + chunkSizeRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

// NormalizeVector returns a normalized vector copy.
func NormalizeVector(vector []float32) ([]float32, error) {
	if len(vector) == 0 {
		return nil, fmt.Errorf("cannot normalize empty vector")
	}
	var norm float64
	for _, v := range vector {
		f := float64(v)
		norm += f * f
	}
	if norm == 0 {
		return nil, fmt.Errorf("cannot normalize zero vector")
	}
	norm = math.Sqrt(norm)

	normalized := make([]float32, len(vector))
	for i, v := range vector {
		normalized[i] = float32(float64(v) / norm)
	}
	return normalized, nil
}

func parseEmbeddingResponse(body []byte) ([]float32, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	if vector, ok := payload["embedding"]; ok {
		return parseFloatVector(vector)
	}
	if vector, ok := payload["vector"]; ok {
		return parseFloatVector(vector)
	}
	if embeddingsRaw, ok := payload["embeddings"].([]interface{}); ok && len(embeddingsRaw) > 0 {
		if first, ok := embeddingsRaw[0].(map[string]interface{}); ok {
			if vector, ok := first["embedding"]; ok {
				return parseFloatVector(vector)
			}
		}
	}

	return nil, fmt.Errorf("embedding response has no supported vector field")
}

func parseFloatVector(raw interface{}) ([]float32, error) {
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("embedding vector is not an array")
	}

	vector := make([]float32, 0, len(items))
	for _, item := range items {
		switch n := item.(type) {
		case float64:
			vector = append(vector, float32(n))
		case float32:
			vector = append(vector, n)
		case int:
			vector = append(vector, float32(n))
		default:
			return nil, fmt.Errorf("embedding vector contains non-numeric value")
		}
	}
	return vector, nil
}
