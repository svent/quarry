package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/svent/quarry/internal/datasource"
)

func testIndexDatasources() []*datasource.Datasource {
	return []*datasource.Datasource{
		{
			Type:        "index",
			Name:        "test-ds",
			Description: "Test datasource",
			Entries: []datasource.DocIndexEntry{
				{Filename: "./docs/intro.md", Content: "Introduction", Keywords: []string{"intro", "getting started"}},
				{Filename: "./docs/api.md", Content: "API reference", Keywords: []string{"api", "reference"}},
			},
			ResolvedDatapath: "/tmp/test-data",
		},
	}
}

func testIndexDatasourcesWithEmbeddings() []*datasource.Datasource {
	return []*datasource.Datasource{
		{
			Type:        "index",
			Name:        "test-ds",
			Description: "Test datasource",
			Entries: []datasource.DocIndexEntry{
				{Filename: "./docs/intro.md", Content: "Introduction", Keywords: []string{"intro", "getting started"}},
				{Filename: "./docs/api.md", Content: "API reference", Keywords: []string{"api", "reference"}},
				{Filename: "./docs/ops.md", Content: "Operations guide", Keywords: []string{"operations", "deploy"}},
			},
			Embeddings: &datasource.EmbeddingIndex{
				Version:    datasource.EmbeddingIndexVersion,
				ModelID:    "test-model",
				Dimensions: 2,
				Entries: []datasource.EmbeddingDoc{
					{Filename: "./docs/intro.md", Vector: []float32{1, 0}},
					{Filename: "./docs/api.md", Vector: []float32{0.9, 0.1}},
					{Filename: "./docs/ops.md", Vector: []float32{0, 1}},
				},
			},
			ResolvedDatapath: "/tmp/test-data",
		},
	}
}

func testToolsDatasources(tmpDir string) []*datasource.Datasource {
	return []*datasource.Datasource{
		{
			Type:             "tools",
			Name:             "code",
			Description:      "Source code",
			ResolvedDatapath: tmpDir,
			Tools:            []string{"list_files", "read_file", "grep"},
		},
	}
}

func testMixedDatasources(tmpDir string) []*datasource.Datasource {
	return []*datasource.Datasource{
		{
			Type:        "index",
			Name:        "test-ds",
			Description: "Test datasource",
			Entries: []datasource.DocIndexEntry{
				{Filename: "./docs/intro.md", Content: "Introduction", Keywords: []string{"intro"}},
			},
			ResolvedDatapath: "/tmp/test-data",
		},
		{
			Type:             "tools",
			Name:             "code",
			Description:      "Source code",
			ResolvedDatapath: tmpDir,
			Tools:            []string{"list_files", "read_file", "grep"},
		},
	}
}

func setupTestDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)
	os.WriteFile(filepath.Join(srcDir, "lib.go"), []byte("package src\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"), 0644)
	os.WriteFile(filepath.Join(srcDir, "lib_test.go"), []byte("package src\n\nfunc TestAdd(t *testing.T) {}\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test Project\nThis is a test.\n"), 0644)
	return tmpDir
}

// --- CreateToolDefinitions tests ---

func TestCreateToolDefinitions_IndexOnly(t *testing.T) {
	ds := testIndexDatasources()
	tools := CreateToolDefinitions(ds)

	if len(tools) != 3 {
		t.Fatalf("expected 3 tools for index-only, got %d", len(tools))
	}
}

func TestCreateToolDefinitions_ToolsOnly(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)
	tools := CreateToolDefinitions(ds)

	if len(tools) != 3 {
		t.Fatalf("expected 3 tools for tools-only (list_files, read_file, grep), got %d", len(tools))
	}
}

func TestCreateToolDefinitions_ToolsSubset(t *testing.T) {
	ds := []*datasource.Datasource{
		{
			Type:             "tools",
			Name:             "code",
			Description:      "Source code",
			ResolvedDatapath: "/tmp",
			Tools:            []string{"list_files", "read_file"},
		},
	}
	tools := CreateToolDefinitions(ds)

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools for subset config, got %d", len(tools))
	}
}

func TestCreateToolDefinitions_Mixed(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testMixedDatasources(tmpDir)
	tools := CreateToolDefinitions(ds)

	// 3 index tools + 3 tools-type tools = 6
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools for mixed config, got %d", len(tools))
	}
}

// --- Index tool execution tests ---

func TestExecuteTool_ListKeywords(t *testing.T) {
	ds := testIndexDatasources()
	result := ExecuteTool("list_keywords", map[string]interface{}{
		"datasource": "test-ds",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if parsed["datasource"] != "test-ds" {
		t.Errorf("expected datasource test-ds, got %v", parsed["datasource"])
	}

	keywords, ok := parsed["keywords"].([]interface{})
	if !ok {
		t.Fatal("keywords is not an array")
	}
	if len(keywords) != 4 {
		t.Errorf("expected 4 unique keywords, got %d", len(keywords))
	}
}

func TestExecuteTool_ListKeywords_UnknownDatasource(t *testing.T) {
	ds := testIndexDatasources()
	result := ExecuteTool("list_keywords", map[string]interface{}{
		"datasource": "unknown",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if _, ok := parsed["error"]; !ok {
		t.Error("expected error field in result")
	}
}

func TestExecuteTool_LookupKeywords(t *testing.T) {
	ds := testIndexDatasources()
	result := ExecuteTool("lookup_keywords", map[string]interface{}{
		"datasource": "test-ds",
		"keywords":   []interface{}{"api"},
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	entries, ok := parsed["entries"].([]interface{})
	if !ok {
		t.Fatal("entries is not an array")
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 matching entry, got %d", len(entries))
	}
}

func TestExecuteTool_LookupKeywords_SemanticFallbackForUnknownKeyword(t *testing.T) {
	ds := testIndexDatasourcesWithEmbeddings()

	original := embedKeywordForLookup
	embedKeywordForLookup = func(_ context.Context, keyword string) ([]float32, error) {
		if keyword == "unknown-topic" {
			return []float32{1, 0}, nil
		}
		return []float32{0, 1}, nil
	}
	t.Cleanup(func() {
		embedKeywordForLookup = original
	})

	result := ExecuteTool("lookup_keywords", map[string]interface{}{
		"datasource": "test-ds",
		"keywords":   []interface{}{"unknown-topic"},
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	entries, ok := parsed["entries"].([]interface{})
	if !ok {
		t.Fatal("entries is not an array")
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 semantic entries, got %d", len(entries))
	}

	first, ok := entries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("entry[0] is not an object")
	}
	if first["filename"] != "./docs/intro.md" {
		t.Fatalf("expected intro as top semantic hit, got %v", first["filename"])
	}
}

func TestExecuteTool_LookupKeywords_MergeExactThenSemantic(t *testing.T) {
	ds := testIndexDatasourcesWithEmbeddings()

	original := embedKeywordForLookup
	embedKeywordForLookup = func(_ context.Context, _ string) ([]float32, error) {
		return []float32{0.9, 0.1}, nil
	}
	t.Cleanup(func() {
		embedKeywordForLookup = original
	})

	result := ExecuteTool("lookup_keywords", map[string]interface{}{
		"datasource": "test-ds",
		"keywords":   []interface{}{"api", "unknown-topic"},
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	entries, ok := parsed["entries"].([]interface{})
	if !ok {
		t.Fatal("entries is not an array")
	}
	if len(entries) < 2 {
		t.Fatalf("expected exact+semantic entries, got %d", len(entries))
	}

	first, ok := entries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("entry[0] is not an object")
	}
	if first["filename"] != "./docs/api.md" {
		t.Fatalf("expected exact api entry first, got %v", first["filename"])
	}

	seen := map[string]bool{}
	for _, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		filename, _ := entry["filename"].(string)
		if seen[filename] {
			t.Fatalf("duplicate filename returned: %s", filename)
		}
		seen[filename] = true
	}
}

func TestExecuteTool_FetchContent_UnknownFilename(t *testing.T) {
	ds := testIndexDatasources()
	result := ExecuteTool("fetch_content", map[string]interface{}{
		"datasource": "test-ds",
		"filename":   "./nonexistent.md",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	errMsg, ok := parsed["error"].(string)
	if !ok || errMsg != "Unknown filename. Use lookup_keywords first." {
		t.Errorf("unexpected error: %v", parsed["error"])
	}
}

func TestExecuteTool_FetchContent_Success(t *testing.T) {
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	os.MkdirAll(docsDir, 0755)
	os.WriteFile(filepath.Join(docsDir, "intro.md"), []byte("# Introduction\nHello world"), 0644)

	ds := []*datasource.Datasource{
		{
			Type:        "index",
			Name:        "test-ds",
			Description: "Test",
			Entries: []datasource.DocIndexEntry{
				{Filename: "./docs/intro.md", Content: "Intro", Keywords: []string{"intro"}},
			},
			ResolvedDatapath: tmpDir,
		},
	}

	result := ExecuteTool("fetch_content", map[string]interface{}{
		"datasource": "test-ds",
		"filename":   "./docs/intro.md",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if parsed["filename"] != "./docs/intro.md" {
		t.Errorf("unexpected filename: %v", parsed["filename"])
	}
	content, ok := parsed["content"].(string)
	if !ok || content != "# Introduction\nHello world" {
		t.Errorf("unexpected content: %v", parsed["content"])
	}
}

// --- Tools-type tool execution tests ---

func TestExecuteTool_ListFiles(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "code",
		"pattern":    "*.go",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files, ok := parsed["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file matching *.go at root, got %d: %v", len(files), files)
	}
	if files[0] != "main.go" {
		t.Errorf("expected main.go, got %v", files[0])
	}
}

func TestExecuteTool_ListFiles_Recursive(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "code",
		"pattern":    "**/*.go",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files, ok := parsed["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 .go files recursively, got %d: %v", len(files), files)
	}
}

func TestExecuteTool_ListFiles_DirectoryPath(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	// Passing a plain directory path should list its contents.
	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "code",
		"pattern":    "src",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files, ok := parsed["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files in src/, got %d: %v", len(files), files)
	}
}

func TestExecuteTool_ListFiles_RootDot(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	// "." should list the root directory contents.
	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "code",
		"pattern":    ".",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files, ok := parsed["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}
	// setupTestDir creates: main.go, README.md at root + src/ dir
	if len(files) != 2 {
		t.Fatalf("expected 2 files at root (main.go, README.md), got %d: %v", len(files), files)
	}
}

func TestExecuteTool_ListFiles_TrailingSlash(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	// "src/" should also list directory contents.
	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "code",
		"pattern":    "src/",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files, ok := parsed["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files in src/, got %d: %v", len(files), files)
	}
}

func TestExecuteTool_ListFiles_Limit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create more than listFilesMaxResults files.
	for i := 0; i < listFilesMaxResults+50; i++ {
		os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("file_%03d.txt", i)), []byte("x"), 0644)
	}

	ds := []*datasource.Datasource{
		{
			Type:             "tools",
			Name:             "many",
			Description:      "Many files test",
			ResolvedDatapath: tmpDir,
			Tools:            []string{"list_files"},
		},
	}

	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "many",
		"pattern":    "*.txt",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files, ok := parsed["files"].([]interface{})
	if !ok {
		t.Fatal("files is not an array")
	}
	if len(files) != listFilesMaxResults {
		t.Errorf("expected %d files, got %d", listFilesMaxResults, len(files))
	}

	truncated, ok := parsed["truncated"].(bool)
	if !ok || !truncated {
		t.Error("expected truncated to be true")
	}
}

func TestExecuteTool_ListFiles_TraversalBlocked(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("list_files", map[string]interface{}{
		"datasource": "code",
		"pattern":    "../etc/*",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if _, ok := parsed["error"]; !ok {
		t.Error("expected error for path traversal")
	}
}

func TestExecuteTool_ReadFile_Tools(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("read_file", map[string]interface{}{
		"datasource": "code",
		"path":       "main.go",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if parsed["path"] != "main.go" {
		t.Errorf("unexpected path: %v", parsed["path"])
	}
	content, ok := parsed["content"].(string)
	if !ok || content == "" {
		t.Error("expected non-empty content")
	}
}

func TestExecuteTool_ReadFile_Tools_NotFound(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("read_file", map[string]interface{}{
		"datasource": "code",
		"path":       "nonexistent.go",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	errMsg, ok := parsed["error"].(string)
	if !ok || errMsg != "File not found: nonexistent.go" {
		t.Errorf("unexpected error: %v", parsed["error"])
	}
}

func TestExecuteTool_ReadFile_Tools_TraversalBlocked(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("read_file", map[string]interface{}{
		"datasource": "code",
		"path":       "../../etc/passwd",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	errMsg, ok := parsed["error"].(string)
	if !ok || errMsg != "Invalid file path." {
		t.Errorf("unexpected error: %v", parsed["error"])
	}
}

func TestExecuteTool_Grep(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "code",
		"pattern":    "func",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	matches, ok := parsed["matches"].(string)
	if !ok || matches == "" {
		t.Error("expected non-empty matches")
	}

	total, ok := parsed["total"].(float64)
	if !ok || total == 0 {
		t.Error("expected non-zero total")
	}

	truncated, ok := parsed["truncated"].(bool)
	if !ok || truncated {
		t.Error("expected truncated to be false")
	}
}

func TestExecuteTool_Grep_NoMatch(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "code",
		"pattern":    "zzz_nonexistent_pattern_zzz",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	total, ok := parsed["total"].(float64)
	if !ok || total != 0 {
		t.Errorf("expected total 0, got %v", parsed["total"])
	}
	if _, hasError := parsed["error"]; hasError {
		t.Errorf("expected no error, got %v", parsed["error"])
	}
}

func TestExecuteTool_Grep_WithPath(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "code",
		"pattern":    "func",
		"path":       "src",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	matches, ok := parsed["matches"].(string)
	if !ok || matches == "" {
		t.Error("expected non-empty matches")
	}

	// Results should be relative paths within the datasource.
	if parsed["total"].(float64) < 1 {
		t.Error("expected at least 1 match in src/")
	}
}

func TestExecuteTool_Grep_Limit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with more than grepMaxResults matching lines.
	var content string
	for i := 0; i < 100; i++ {
		content += fmt.Sprintf("line %d: match_target\n", i)
	}
	os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte(content), 0644)

	ds := []*datasource.Datasource{
		{
			Type:             "tools",
			Name:             "big",
			Description:      "Big file test",
			ResolvedDatapath: tmpDir,
			Tools:            []string{"grep"},
		},
	}

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "big",
		"pattern":    "match_target",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	total := int(parsed["total"].(float64))
	if total != grepMaxResults {
		t.Errorf("expected total %d, got %d", grepMaxResults, total)
	}

	truncated, ok := parsed["truncated"].(bool)
	if !ok || !truncated {
		t.Error("expected truncated to be true")
	}
}

func TestExecuteTool_Grep_RelativePaths(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "code",
		"pattern":    "package",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	matches, ok := parsed["matches"].(string)
	if !ok {
		t.Fatal("matches is not a string")
	}

	// Output should not contain the absolute tmpDir path.
	if len(matches) > 0 && matches[0] == '/' {
		t.Errorf("expected relative paths in output, got: %s", matches)
	}
}

func TestExecuteTool_Grep_InvalidRegex(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "code",
		"pattern":    "[",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	errMsg, ok := parsed["error"].(string)
	if !ok || errMsg == "" {
		t.Fatalf("expected grep error, got %v", parsed["error"])
	}
}

func TestExecuteTool_Grep_NonexistentPath(t *testing.T) {
	tmpDir := setupTestDir(t)
	ds := testToolsDatasources(tmpDir)

	result := ExecuteTool("grep", map[string]interface{}{
		"datasource": "code",
		"pattern":    "func",
		"path":       "no-such-dir",
	}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	errMsg, ok := parsed["error"].(string)
	if !ok || errMsg == "" {
		t.Fatalf("expected grep error, got %v", parsed["error"])
	}
}

// --- General tests ---

func TestExecuteTool_UnknownTool(t *testing.T) {
	ds := testIndexDatasources()
	result := ExecuteTool("nonexistent_tool", map[string]interface{}{}, ds, false)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	errMsg, ok := parsed["error"].(string)
	if !ok || errMsg != "Unknown tool: nonexistent_tool" {
		t.Errorf("unexpected error: %v", parsed["error"])
	}
}

func TestTruncateDebugValue(t *testing.T) {
	short := "hello"
	result := truncateDebugValue(short)
	if result != "hello" {
		t.Errorf("expected hello, got %v", result)
	}

	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	result = truncateDebugValue(long)
	s, ok := result.(string)
	if !ok {
		t.Fatal("expected string")
	}
	if len(s) != 103 { // 100 + "..."
		t.Errorf("expected truncated to 103 chars, got %d", len(s))
	}
}
