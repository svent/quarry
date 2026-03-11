package config

import (
	"os"
	"path/filepath"
	"testing"
)

func clearEnv() {
	os.Unsetenv("AWS_ACCESS_KEY_ID")
	os.Unsetenv("AWS_SECRET_ACCESS_KEY")
	os.Unsetenv("AWS_SESSION_TOKEN")
	os.Unsetenv("AWS_REGION")
	os.Unsetenv("AWS_DEFAULT_REGION")
	os.Unsetenv("AWS_ACCOUNT_ID")
	os.Unsetenv("CHAT_MODEL_ID")
	os.Unsetenv("EMBEDDING_MODEL_ID")
}

func TestLoadEnvCredentials_BothSet(t *testing.T) {
	clearEnv()
	os.Setenv("AWS_ACCESS_KEY_ID", "AKID")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "SECRET")
	os.Setenv("AWS_SESSION_TOKEN", "TOKEN")

	creds, err := loadEnvCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected credentials, got nil")
	}
	if creds.AccessKeyID != "AKID" {
		t.Errorf("expected AKID, got %s", creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "SECRET" {
		t.Errorf("expected SECRET, got %s", creds.SecretAccessKey)
	}
	if creds.SessionToken != "TOKEN" {
		t.Errorf("expected TOKEN, got %s", creds.SessionToken)
	}
}

func TestLoadEnvCredentials_NoneSet(t *testing.T) {
	clearEnv()

	creds, err := loadEnvCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds != nil {
		t.Fatal("expected nil credentials")
	}
}

func TestLoadEnvCredentials_Incomplete_OnlyKeyID(t *testing.T) {
	clearEnv()
	os.Setenv("AWS_ACCESS_KEY_ID", "AKID")

	_, err := loadEnvCredentials()
	if err == nil {
		t.Fatal("expected error for incomplete credentials")
	}
	expected := "Incomplete AWS credentials. Set both AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or rely on AWS_PROFILE/default credentials."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestLoadEnvCredentials_Incomplete_OnlySecret(t *testing.T) {
	clearEnv()
	os.Setenv("AWS_SECRET_ACCESS_KEY", "SECRET")

	_, err := loadEnvCredentials()
	if err == nil {
		t.Fatal("expected error for incomplete credentials")
	}
}

func TestBuildAwsConfig_RegionFallback(t *testing.T) {
	clearEnv()

	// No region set -> default
	cfg, err := buildAwsConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Region != "eu-central-1" {
		t.Errorf("expected eu-central-1, got %s", cfg.Region)
	}

	// AWS_DEFAULT_REGION set
	os.Setenv("AWS_DEFAULT_REGION", "us-west-2")
	cfg, err = buildAwsConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Region != "us-west-2" {
		t.Errorf("expected us-west-2, got %s", cfg.Region)
	}

	// AWS_REGION takes priority
	os.Setenv("AWS_REGION", "ap-southeast-1")
	cfg, err = buildAwsConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Region != "ap-southeast-1" {
		t.Errorf("expected ap-southeast-1, got %s", cfg.Region)
	}
}

func TestBuildAwsConfig_NilCredentialsWhenEnvEmpty(t *testing.T) {
	clearEnv()

	cfg, err := buildAwsConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Credentials != nil {
		t.Error("expected nil credentials when env is empty")
	}
}

func TestBuildAwsConfig_CredentialsProviderWhenSet(t *testing.T) {
	clearEnv()
	os.Setenv("AWS_ACCESS_KEY_ID", "AKID")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "SECRET")

	cfg, err := buildAwsConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Credentials == nil {
		t.Error("expected credentials provider, got nil")
	}
}

func TestConstants(t *testing.T) {
	if ChatModelARNTemplate == "" {
		t.Error("ChatModelARNTemplate should not be empty")
	}
	if DefaultEmbeddingModelID == "" {
		t.Error("DefaultEmbeddingModelID should not be empty")
	}
}

func TestGetEmbeddingModelID_Default(t *testing.T) {
	clearEnv()
	if modelID := GetEmbeddingModelID(); modelID != DefaultEmbeddingModelID {
		t.Fatalf("expected default embedding model %q, got %q", DefaultEmbeddingModelID, modelID)
	}
}

func TestGetEmbeddingModelID_Override(t *testing.T) {
	clearEnv()
	os.Setenv("EMBEDDING_MODEL_ID", "custom.embedding.model")

	if modelID := GetEmbeddingModelID(); modelID != "custom.embedding.model" {
		t.Fatalf("expected override embedding model, got %q", modelID)
	}
}

func TestGetChatModelID_UsesOverride(t *testing.T) {
	clearEnv()
	ResetForTesting()
	t.Cleanup(func() {
		clearEnv()
		ResetForTesting()
	})

	override := "arn:aws:bedrock:eu-central-1:111122223333:inference-profile/custom.model-v1:0"
	os.Setenv("CHAT_MODEL_ID", override)

	modelID, err := GetChatModelID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != override {
		t.Fatalf("expected override model ID %q, got %q", override, modelID)
	}
}

func TestGetChatModelID_UsesAWSAccountID(t *testing.T) {
	clearEnv()
	ResetForTesting()
	t.Cleanup(func() {
		clearEnv()
		ResetForTesting()
	})

	os.Setenv("AWS_ACCOUNT_ID", "111122223333")

	modelID, err := GetChatModelID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "arn:aws:bedrock:eu-central-1:111122223333:inference-profile/eu.amazon.nova-2-lite-v1:0"
	if modelID != expected {
		t.Fatalf("expected %q, got %q", expected, modelID)
	}
}

func TestGetChatModelID_InvalidAWSAccountID(t *testing.T) {
	clearEnv()
	ResetForTesting()
	t.Cleanup(func() {
		clearEnv()
		ResetForTesting()
	})

	os.Setenv("AWS_ACCOUNT_ID", "invalid")

	_, err := GetChatModelID()
	if err == nil {
		t.Fatal("expected error for invalid AWS_ACCOUNT_ID")
	}
}

func TestDatasourcesConfigPath_NotEmpty(t *testing.T) {
	if DatasourcesConfigPath == "" {
		t.Error("DatasourcesConfigPath should not be empty")
	}
}

func TestResolveDatasourcesConfigPath_Fallback(t *testing.T) {
	// When no datasources dir exists next to the test binary,
	// the function should fall back to the CWD-relative path.
	path := resolveDatasourcesConfigPath()
	if path == "" {
		t.Error("resolveDatasourcesConfigPath should not return empty string")
	}
	// In the test environment, the executable-relative path won't exist,
	// so we expect the CWD fallback.
	expected := "./" + datasourcesRelPath
	if path != expected {
		// If it resolved to an absolute path, that means the file was
		// found next to the test binary — also valid.
		if !filepath.IsAbs(path) {
			t.Errorf("expected %q or an absolute path, got %q", expected, path)
		}
	}
}
