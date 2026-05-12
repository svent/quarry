package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/joho/godotenv"
)

const (
	// ChatModelARNTemplate uses a dynamic AWS account ID.
	// The account ID is resolved in this order:
	// 1) CHAT_MODEL_ID (full override)
	// 2) AWS_ACCOUNT_ID
	// 3) STS GetCallerIdentity
	ChatModelARNTemplate = "arn:aws:bedrock:eu-central-1:%s:inference-profile/eu.amazon.nova-2-lite-v1:0"
	// "arn:aws:bedrock:eu-central-1:%s:inference-profile/eu.anthropic.claude-sonnet-4-5-20250929-v1:0"
	DefaultEmbeddingModelID = "amazon.titan-embed-text-v2:0"

	datasourcesRelPath = "datasources/datasources.json"
)

var accountIDRegex = regexp.MustCompile(`^\d{12}$`)

// DatasourcesConfigPath is the resolved path to datasources.json.
// It first checks for the file relative to the running executable
// (e.g. bin/../datasources/datasources.json), then falls back to
// the path relative to the current working directory.
var DatasourcesConfigPath = resolveDatasourcesConfigPath()

// resolveDatasourcesConfigPath returns an absolute path to datasources.json.
// It prefers the path relative to the executable's directory (so binaries
// work regardless of the working directory), and falls back to the path
// relative to the current working directory.
func resolveDatasourcesConfigPath() string {
	// Try relative to the executable (e.g. bin/../datasources/datasources.json).
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidate := filepath.Join(execDir, "..", datasourcesRelPath)
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	// Fall back to CWD-relative path.
	return "./" + datasourcesRelPath
}

// AwsConfig holds the AWS region and optional static credentials provider.
type AwsConfig struct {
	Region      string
	Credentials *aws.CredentialsProvider
}

var (
	cachedConfig      *AwsConfig
	cachedChatModelID string
	configMu          sync.Mutex
)

func init() {
	// Load .env on startup (ignore error if file doesn't exist).
	_ = godotenv.Load()
}

type envCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func loadEnvCredentials() (*envCredentials, error) {
	accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	sessionToken := os.Getenv("AWS_SESSION_TOKEN")

	hasAccessKeyID := accessKeyID != ""
	hasSecretAccessKey := secretAccessKey != ""

	if !hasAccessKeyID && !hasSecretAccessKey {
		return nil, nil
	}

	if hasAccessKeyID != hasSecretAccessKey {
		return nil, fmt.Errorf(
			"Incomplete AWS credentials. Set both AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or rely on AWS_PROFILE/default credentials.",
		)
	}

	return &envCredentials{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
	}, nil
}

func buildAwsConfig() (*AwsConfig, error) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "eu-central-1"
	}

	creds, err := loadEnvCredentials()
	if err != nil {
		return nil, err
	}

	cfg := &AwsConfig{
		Region: region,
	}

	if creds != nil {
		// Create a credentials provider that re-reads .env on each call
		// for credential rotation support.
		provider := aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			_ = godotenv.Overload()
			refreshed, err := loadEnvCredentials()
			if err != nil {
				return aws.Credentials{}, err
			}
			if refreshed == nil {
				return aws.Credentials{}, fmt.Errorf(
					"Missing AWS credentials. Set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or rely on AWS_PROFILE/default credentials.",
				)
			}
			return credentials.NewStaticCredentialsProvider(
				refreshed.AccessKeyID,
				refreshed.SecretAccessKey,
				refreshed.SessionToken,
			).Retrieve(ctx)
		})
		p := aws.CredentialsProvider(provider)
		cfg.Credentials = &p
	}

	return cfg, nil
}

func accountIDFromEnv() (string, bool, error) {
	accountID := os.Getenv("AWS_ACCOUNT_ID")
	if accountID == "" {
		return "", false, nil
	}

	if !accountIDRegex.MatchString(accountID) {
		return "", false, fmt.Errorf("AWS_ACCOUNT_ID must be a 12-digit AWS account number")
	}

	return accountID, true, nil
}

func resolveAccountIDWithSTS(ctx context.Context, cfg *AwsConfig) (string, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}

	if cfg.Credentials != nil {
		opts = append(opts, awsconfig.WithCredentialsProvider(*cfg.Credentials))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return "", err
	}

	identity, err := sts.NewFromConfig(awsCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}

	accountID := aws.ToString(identity.Account)
	if !accountIDRegex.MatchString(accountID) {
		return "", fmt.Errorf("resolved AWS account ID is invalid: %q", accountID)
	}

	return accountID, nil
}

// GetChatModelID returns the configured model ARN with dynamic AWS account ID resolution.
func GetChatModelID() (string, error) {
	configMu.Lock()
	defer configMu.Unlock()

	if override := os.Getenv("CHAT_MODEL_ID"); override != "" {
		return override, nil
	}

	if cachedChatModelID != "" {
		return cachedChatModelID, nil
	}

	if accountID, ok, err := accountIDFromEnv(); err != nil {
		return "", err
	} else if ok {
		cachedChatModelID = fmt.Sprintf(ChatModelARNTemplate, accountID)
		return cachedChatModelID, nil
	}

	cfg, err := buildAwsConfig()
	if err != nil {
		return "", err
	}

	accountID, err := resolveAccountIDWithSTS(context.Background(), cfg)
	if err != nil {
		return "", fmt.Errorf("failed to resolve AWS account ID for ChatModelID (set AWS_ACCOUNT_ID or CHAT_MODEL_ID to avoid STS lookup): %w", err)
	}

	cachedChatModelID = fmt.Sprintf(ChatModelARNTemplate, accountID)
	return cachedChatModelID, nil
}

// GetEmbeddingModelID returns the embedding model ID from environment or fallback default.
func GetEmbeddingModelID() string {
	if override := os.Getenv("EMBEDDING_MODEL_ID"); override != "" {
		return override
	}
	return DefaultEmbeddingModelID
}

// GetAwsConfig returns the cached AWS config, building it on first call.
func GetAwsConfig() (*AwsConfig, error) {
	configMu.Lock()
	defer configMu.Unlock()

	if cachedConfig != nil {
		return cachedConfig, nil
	}

	cfg, err := buildAwsConfig()
	if err != nil {
		return nil, err
	}
	cachedConfig = cfg
	return cachedConfig, nil
}

// ReloadAwsConfig re-reads .env with override and rebuilds the AWS config.
func ReloadAwsConfig() (*AwsConfig, error) {
	configMu.Lock()
	defer configMu.Unlock()

	_ = godotenv.Overload()
	cfg, err := buildAwsConfig()
	if err != nil {
		return nil, err
	}
	cachedConfig = cfg
	cachedChatModelID = ""
	return cachedConfig, nil
}

// ResetForTesting clears the cached config (for tests only).
func ResetForTesting() {
	configMu.Lock()
	defer configMu.Unlock()
	cachedConfig = nil
	cachedChatModelID = ""
}
