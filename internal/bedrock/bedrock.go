package bedrock

import (
	"context"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/svent/quarry/internal/config"
)

var (
	chatClient    *bedrockruntime.Client
	lastRefreshAt *time.Time
	mu            sync.Mutex
)

func buildClient(cfg *config.AwsConfig) (*bedrockruntime.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}

	if cfg.Credentials != nil {
		opts = append(opts, awsconfig.WithCredentialsProvider(*cfg.Credentials))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}

	return bedrockruntime.NewFromConfig(awsCfg), nil
}

func markRefreshed() {
	now := time.Now()
	lastRefreshAt = &now
}

// RefreshBedrockClient reloads AWS config and rebuilds the Bedrock client.
func RefreshBedrockClient() error {
	mu.Lock()
	defer mu.Unlock()

	cfg, err := config.ReloadAwsConfig()
	if err != nil {
		return err
	}

	client, err := buildClient(cfg)
	if err != nil {
		return err
	}

	chatClient = client
	markRefreshed()
	return nil
}

// RefreshBedrockClientIfStale rebuilds the client if it is nil or older than maxAgeMs.
func RefreshBedrockClientIfStale(maxAgeMs int64) error {
	mu.Lock()
	needsRefresh := chatClient == nil || lastRefreshAt == nil ||
		time.Since(*lastRefreshAt).Milliseconds() >= maxAgeMs
	mu.Unlock()

	if needsRefresh {
		return RefreshBedrockClient()
	}
	return nil
}

// GetChatClient returns the Bedrock runtime client, creating it on first call.
func GetChatClient() (*bedrockruntime.Client, error) {
	mu.Lock()
	defer mu.Unlock()

	if chatClient != nil {
		return chatClient, nil
	}

	cfg, err := config.GetAwsConfig()
	if err != nil {
		return nil, err
	}

	client, err := buildClient(cfg)
	if err != nil {
		return nil, err
	}

	chatClient = client
	if lastRefreshAt == nil {
		markRefreshed()
	}
	return chatClient, nil
}

// ResetForTesting clears the cached client (for tests only).
func ResetForTesting() {
	mu.Lock()
	defer mu.Unlock()
	chatClient = nil
	lastRefreshAt = nil
}
