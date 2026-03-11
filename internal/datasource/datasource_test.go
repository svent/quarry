package datasource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseIndexEntries_Normal(t *testing.T) {
	raw := `{"filename":"./a.md","content":"desc a","keywords":["go","test"]}
{"filename":"./b.md","content":"desc b","keywords":["rust"]}`

	entries, err := ParseIndexEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Filename != "./a.md" {
		t.Errorf("expected ./a.md, got %s", entries[0].Filename)
	}
	if len(entries[0].Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(entries[0].Keywords))
	}
}

func TestParseIndexEntries_EmptyLines(t *testing.T) {
	raw := `
{"filename":"./a.md","content":"desc","keywords":["x"]}

{"filename":"./b.md","content":"desc","keywords":["y"]}

`
	entries, err := ParseIndexEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestParseIndexEntries_EmptyString(t *testing.T) {
	entries, err := ParseIndexEntries("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil, got %v", entries)
	}
}

func TestParseIndexEntries_WhitespaceOnly(t *testing.T) {
	entries, err := ParseIndexEntries("   \n  \n  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil, got %v", entries)
	}
}

func TestDeriveName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"zo-docs.jsonl", "zo-docs"},
		{"my-data.json", "my-data"},
		{"simple", "simple"},
		{"path/to/file.jsonl", "file"},
	}
	for _, tt := range tests {
		result := DeriveName(tt.input)
		if result != tt.expected {
			t.Errorf("DeriveName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestResolveFilePath_ValidPath(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	ds := &Datasource{
		ResolvedDatapath: filepath.Join(tmpDir, "docs"),
	}

	result := ResolveFilePath(ds, "readme.md")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	canonicalRoot, err := filepath.EvalSymlinks(ds.ResolvedDatapath)
	if err != nil {
		canonicalRoot = ds.ResolvedDatapath
	}
	expected := filepath.Join(canonicalRoot, "readme.md")
	if *result != expected {
		t.Errorf("expected %q, got %q", expected, *result)
	}
}

func TestResolveFilePath_Subdirectory(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	ds := &Datasource{
		ResolvedDatapath: filepath.Join(tmpDir, "docs"),
	}

	result := ResolveFilePath(ds, "sub/file.md")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	canonicalRoot, err := filepath.EvalSymlinks(ds.ResolvedDatapath)
	if err != nil {
		canonicalRoot = ds.ResolvedDatapath
	}
	expected := filepath.Join(canonicalRoot, "sub/file.md")
	if *result != expected {
		t.Errorf("expected %q, got %q", expected, *result)
	}
}

func TestResolveFilePath_TraversalBlocked(t *testing.T) {
	ds := &Datasource{
		ResolvedDatapath: "/data/docs",
	}

	result := ResolveFilePath(ds, "../../etc/passwd")
	if result != nil {
		t.Errorf("expected nil for path traversal, got %q", *result)
	}
}

func TestResolveFilePath_TraversalWithDots(t *testing.T) {
	ds := &Datasource{
		ResolvedDatapath: "/data/docs",
	}

	result := ResolveFilePath(ds, "../docs-secret/file.md")
	if result != nil {
		t.Errorf("expected nil for path traversal, got %q", *result)
	}
}

func TestResolveFilePath_SymlinkEscapeBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "root")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	ds := &Datasource{
		ResolvedDatapath: root,
	}

	result := ResolveFilePath(ds, "escape/secret.txt")
	if result != nil {
		t.Errorf("expected nil for symlink escape, got %q", *result)
	}
}

func TestResolveFilePath_SymlinkInsideAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "root")
	realDir := filepath.Join(root, "docs")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "intro.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	ds := &Datasource{
		ResolvedDatapath: root,
	}

	result := ResolveFilePath(ds, "alias/intro.md")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	expected := filepath.Join(realDir, "intro.md")
	canonicalExpected, err := filepath.EvalSymlinks(expected)
	if err != nil {
		t.Fatal(err)
	}
	if *result != canonicalExpected {
		t.Errorf("expected %q, got %q", canonicalExpected, *result)
	}
}

func TestUniqueKeywords(t *testing.T) {
	entries := []DocIndexEntry{
		{Keywords: []string{"go", "test", "cli"}},
		{Keywords: []string{"test", "api", "go"}},
		{Keywords: []string{"docs"}},
	}

	result := UniqueKeywords(entries)
	expected := []string{"api", "cli", "docs", "go", "test"}

	if len(result) != len(expected) {
		t.Fatalf("expected %d keywords, got %d: %v", len(expected), len(result), result)
	}
	for i, kw := range expected {
		if result[i] != kw {
			t.Errorf("keyword[%d] = %q, want %q", i, result[i], kw)
		}
	}
}

func TestUniqueKeywords_Empty(t *testing.T) {
	result := UniqueKeywords(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestLookupKeywords_CaseInsensitive(t *testing.T) {
	entries := []DocIndexEntry{
		{Filename: "a.md", Keywords: []string{"Go", "CLI"}},
		{Filename: "b.md", Keywords: []string{"rust", "api"}},
		{Filename: "c.md", Keywords: []string{"Python", "go"}},
	}

	matches := LookupKeywords(entries, []string{"go"})
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Filename != "a.md" || matches[1].Filename != "c.md" {
		t.Errorf("unexpected matches: %v", matches)
	}
}

func TestLookupKeywords_NoMatch(t *testing.T) {
	entries := []DocIndexEntry{
		{Filename: "a.md", Keywords: []string{"go", "cli"}},
	}

	matches := LookupKeywords(entries, []string{"rust"})
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestLookupKeywords_MultipleQueryKeywords(t *testing.T) {
	entries := []DocIndexEntry{
		{Filename: "a.md", Keywords: []string{"go", "cli"}},
		{Filename: "b.md", Keywords: []string{"rust", "api"}},
		{Filename: "c.md", Keywords: []string{"python"}},
	}

	matches := LookupKeywords(entries, []string{"api", "python"})
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
}

func TestPartitionKeywords(t *testing.T) {
	entries := []DocIndexEntry{
		{Keywords: []string{"api", "reference"}},
	}

	known, unknown := PartitionKeywords(entries, []string{"API", "foo", "foo", "  "})
	if len(known) != 1 || known[0] != "API" {
		t.Fatalf("unexpected known keywords: %v", known)
	}
	if len(unknown) != 1 || unknown[0] != "foo" {
		t.Fatalf("unexpected unknown keywords: %v", unknown)
	}
}

func TestEmbeddingIndex_ReadWriteAndValidate(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "docs.emb.gob")

	index := &EmbeddingIndex{
		Version:    EmbeddingIndexVersion,
		ModelID:    "test-model",
		Dimensions: 2,
		Entries: []EmbeddingDoc{
			{Filename: "./a.md", Vector: []float32{1, 0}},
			{Filename: "./b.md", Vector: []float32{0, 1}},
		},
	}
	if err := WriteEmbeddingIndex(path, index); err != nil {
		t.Fatalf("failed to write embedding index: %v", err)
	}

	loaded, err := ReadEmbeddingIndex(path)
	if err != nil {
		t.Fatalf("failed to read embedding index: %v", err)
	}

	entries := []DocIndexEntry{
		{Filename: "./a.md"},
		{Filename: "./b.md"},
	}
	if err := ValidateEmbeddingIndex(loaded, entries); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateEmbeddingIndex_Mismatch(t *testing.T) {
	index := &EmbeddingIndex{
		Version:    EmbeddingIndexVersion,
		ModelID:    "test-model",
		Dimensions: 2,
		Entries: []EmbeddingDoc{
			{Filename: "./a.md", Vector: []float32{1, 0}},
		},
	}
	entries := []DocIndexEntry{
		{Filename: "./a.md"},
		{Filename: "./b.md"},
	}
	if err := ValidateEmbeddingIndex(index, entries); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSearchEmbedding_TopK(t *testing.T) {
	index := &EmbeddingIndex{
		Version:    EmbeddingIndexVersion,
		ModelID:    "test-model",
		Dimensions: 2,
		Entries: []EmbeddingDoc{
			{Filename: "./a.md", Vector: []float32{1, 0}},
			{Filename: "./b.md", Vector: []float32{0.5, 0.5}},
			{Filename: "./c.md", Vector: []float32{0, 1}},
		},
	}

	hits := SearchEmbedding(index, []float32{1, 0}, 2)
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Filename != "./a.md" {
		t.Fatalf("expected first hit ./a.md, got %s", hits[0].Filename)
	}
}

func TestValidateConfig_Valid(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"file":        "test.jsonl",
				"datapath":    "./data",
				"description": "Test datasource",
			},
		},
	}

	result, err := validateConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Datasources) != 1 {
		t.Fatalf("expected 1 datasource, got %d", len(result.Datasources))
	}
}

func TestValidateConfig_DefaultTypeIsIndex(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"file":        "test.jsonl",
				"datapath":    "./data",
				"description": "Test datasource",
			},
		},
	}

	result, err := validateConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Datasources[0].Type != "index" {
		t.Errorf("expected type 'index', got %q", result.Datasources[0].Type)
	}
}

func TestValidateConfig_ExplicitIndexType(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "index",
				"file":        "test.jsonl",
				"datapath":    "./data",
				"description": "Test datasource",
			},
		},
	}

	result, err := validateConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Datasources[0].Type != "index" {
		t.Errorf("expected type 'index', got %q", result.Datasources[0].Type)
	}
}

func TestValidateConfig_MissingDatasourcesKey(t *testing.T) {
	raw := map[string]interface{}{}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := `datasources config must contain a "datasources" array.`
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_EmptyArray(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{},
	}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources config must contain at least one entry."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_MissingFile(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"datapath":    "./data",
				"description": "Test",
			},
		},
	}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].file must be a non-empty string."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_InvalidEmbeddingsFile(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"file":            "test.jsonl",
				"embeddings_file": 123,
				"datapath":        "./data",
				"description":     "Test",
			},
		},
	}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].embeddings_file must be a non-empty string when provided."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_MissingDatapath(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"file":        "test.jsonl",
				"description": "Test",
			},
		},
	}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].datapath must be a non-empty string."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_MissingDescription(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"file":     "test.jsonl",
				"datapath": "./data",
			},
		},
	}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].description must be a non-empty string."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_EntryNotObject(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{"not-an-object"},
	}
	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0] must be an object."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_ToolsDatasource(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "tools",
				"name":        "my-code",
				"datapath":    "./src",
				"description": "Source code",
				"tools":       []interface{}{"list_files", "read_file", "grep"},
			},
		},
	}

	result, err := validateConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Datasources) != 1 {
		t.Fatalf("expected 1 datasource, got %d", len(result.Datasources))
	}
	ds := result.Datasources[0]
	if ds.Type != "tools" {
		t.Errorf("expected type 'tools', got %q", ds.Type)
	}
	if ds.Name != "my-code" {
		t.Errorf("expected name 'my-code', got %q", ds.Name)
	}
	if len(ds.Tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(ds.Tools))
	}
}

func TestValidateConfig_ToolsMissingName(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "tools",
				"datapath":    "./src",
				"description": "Source code",
				"tools":       []interface{}{"list_files"},
			},
		},
	}

	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].name must be a non-empty string for tools type."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_ToolsMissingTools(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "tools",
				"name":        "my-code",
				"datapath":    "./src",
				"description": "Source code",
			},
		},
	}

	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].tools must be a non-empty array for tools type."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_ToolsEmptyToolsArray(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "tools",
				"name":        "my-code",
				"datapath":    "./src",
				"description": "Source code",
				"tools":       []interface{}{},
			},
		},
	}

	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "datasources[0].tools must be a non-empty array for tools type."
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_ToolsInvalidToolName(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "tools",
				"name":        "my-code",
				"datapath":    "./src",
				"description": "Source code",
				"tools":       []interface{}{"list_files", "unknown_tool"},
			},
		},
	}

	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := `datasources[0].tools[1]: unknown tool "unknown_tool". Allowed: grep, list_files, read_file`
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_UnknownType(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "unknown",
				"datapath":    "./src",
				"description": "Source code",
			},
		},
	}

	_, err := validateConfig(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expected := `datasources[0].type must be "index" or "tools", got "unknown".`
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestValidateConfig_MixedTypes(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "index",
				"file":        "docs.jsonl",
				"datapath":    "./docs",
				"description": "Documentation",
			},
			map[string]interface{}{
				"type":        "tools",
				"name":        "src",
				"datapath":    "./src",
				"description": "Source code",
				"tools":       []interface{}{"list_files", "read_file"},
			},
		},
	}

	result, err := validateConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Datasources) != 2 {
		t.Fatalf("expected 2 datasources, got %d", len(result.Datasources))
	}
	if result.Datasources[0].Type != "index" {
		t.Errorf("expected first type 'index', got %q", result.Datasources[0].Type)
	}
	if result.Datasources[1].Type != "tools" {
		t.Errorf("expected second type 'tools', got %q", result.Datasources[1].Type)
	}
}

func TestValidateConfig_IndexWithExplicitName(t *testing.T) {
	raw := map[string]interface{}{
		"datasources": []interface{}{
			map[string]interface{}{
				"type":        "index",
				"name":        "custom-name",
				"file":        "docs.jsonl",
				"datapath":    "./docs",
				"description": "Documentation",
			},
		},
	}

	result, err := validateConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Datasources[0].Name != "custom-name" {
		t.Errorf("expected name 'custom-name', got %q", result.Datasources[0].Name)
	}
}

func TestLoadDatasources_Integration(t *testing.T) {
	// Create temp directory with test datasource files.
	tmpDir := t.TempDir()

	jsonlContent := `{"filename":"./test.md","content":"test doc","keywords":["test","go"]}
{"filename":"./other.md","content":"other doc","keywords":["other"]}`

	jsonlPath := filepath.Join(tmpDir, "test-ds.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	configContent := `{
  "datasources": [
    {
      "file": "test-ds.jsonl",
      "datapath": ".",
      "description": "Test datasource for unit tests"
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "datasources.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Clear cache before test.
	ClearDatasourceCache()

	datasources, err := LoadDatasources(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(datasources) != 1 {
		t.Fatalf("expected 1 datasource, got %d", len(datasources))
	}

	ds := datasources[0]
	if ds.Name != "test-ds" {
		t.Errorf("expected name 'test-ds', got %q", ds.Name)
	}
	if ds.Type != "index" {
		t.Errorf("expected type 'index', got %q", ds.Type)
	}
	if len(ds.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(ds.Entries))
	}
	if ds.Description != "Test datasource for unit tests" {
		t.Errorf("unexpected description: %q", ds.Description)
	}

	// Clear cache after test.
	ClearDatasourceCache()
}

func TestLoadDatasources_IndexWithExplicitName(t *testing.T) {
	tmpDir := t.TempDir()

	jsonlContent := `{"filename":"./test.md","content":"test doc","keywords":["test"]}`

	jsonlPath := filepath.Join(tmpDir, "test-ds.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	configContent := `{
  "datasources": [
    {
      "name": "custom-name",
      "file": "test-ds.jsonl",
      "datapath": ".",
      "description": "Test datasource"
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "datasources.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ClearDatasourceCache()

	datasources, err := LoadDatasources(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if datasources[0].Name != "custom-name" {
		t.Errorf("expected name 'custom-name', got %q", datasources[0].Name)
	}

	ClearDatasourceCache()
}

func TestLoadDatasources_IndexWithEmbeddingsFile(t *testing.T) {
	tmpDir := t.TempDir()

	jsonlContent := `{"filename":"./test.md","content":"test doc","keywords":["test"]}`
	jsonlPath := filepath.Join(tmpDir, "test-ds.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	embeddingPath := filepath.Join(tmpDir, "test-ds.emb.gob")
	if err := WriteEmbeddingIndex(embeddingPath, &EmbeddingIndex{
		Version:    EmbeddingIndexVersion,
		ModelID:    "test-model",
		Dimensions: 2,
		Entries: []EmbeddingDoc{
			{Filename: "./test.md", Vector: []float32{1, 0}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	configContent := `{
  "datasources": [
    {
      "name": "custom-name",
      "file": "test-ds.jsonl",
      "embeddings_file": "test-ds.emb.gob",
      "datapath": ".",
      "description": "Test datasource"
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "datasources.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ClearDatasourceCache()
	datasources, err := LoadDatasources(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if datasources[0].Embeddings == nil {
		t.Fatal("expected embeddings to be loaded")
	}
	if datasources[0].Embeddings.ModelID != "test-model" {
		t.Fatalf("unexpected embedding model id: %s", datasources[0].Embeddings.ModelID)
	}

	ClearDatasourceCache()
}

func TestLoadDatasources_IndexWithEmbeddingsFileMissing(t *testing.T) {
	tmpDir := t.TempDir()

	jsonlContent := `{"filename":"./test.md","content":"test doc","keywords":["test"]}`
	jsonlPath := filepath.Join(tmpDir, "test-ds.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	configContent := `{
  "datasources": [
    {
      "name": "custom-name",
      "file": "test-ds.jsonl",
      "embeddings_file": "missing.emb.gob",
      "datapath": ".",
      "description": "Test datasource"
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "datasources.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ClearDatasourceCache()
	_, err := LoadDatasources(configPath)
	if err == nil {
		t.Fatal("expected error")
	}

	ClearDatasourceCache()
}

func TestLoadDatasources_ToolsType(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `{
  "datasources": [
    {
      "type": "tools",
      "name": "my-code",
      "datapath": ".",
      "description": "Source code",
      "tools": ["list_files", "read_file"]
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "datasources.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ClearDatasourceCache()

	datasources, err := LoadDatasources(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(datasources) != 1 {
		t.Fatalf("expected 1 datasource, got %d", len(datasources))
	}

	ds := datasources[0]
	if ds.Type != "tools" {
		t.Errorf("expected type 'tools', got %q", ds.Type)
	}
	if ds.Name != "my-code" {
		t.Errorf("expected name 'my-code', got %q", ds.Name)
	}
	if len(ds.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(ds.Tools))
	}
	if len(ds.Entries) != 0 {
		t.Errorf("expected 0 entries for tools type, got %d", len(ds.Entries))
	}

	ClearDatasourceCache()
}

func TestLoadDatasources_DuplicateNames(t *testing.T) {
	tmpDir := t.TempDir()

	jsonlContent := `{"filename":"./a.md","content":"a","keywords":["a"]}`

	// Create two JSONL files with the same base name in different directories.
	if err := os.WriteFile(filepath.Join(tmpDir, "same.jsonl"), []byte(jsonlContent), 0644); err != nil {
		t.Fatal(err)
	}

	configContent := `{
  "datasources": [
    {
      "file": "same.jsonl",
      "datapath": ".",
      "description": "First"
    },
    {
      "file": "same.jsonl",
      "datapath": ".",
      "description": "Second"
    }
  ]
}`
	configPath := filepath.Join(tmpDir, "datasources.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ClearDatasourceCache()

	_, err := LoadDatasources(configPath)
	if err == nil {
		t.Fatal("expected error for duplicate names")
	}
	expected := `Duplicate datasource name "same". Each datasource must have a unique name.`
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}

	ClearDatasourceCache()
}
