package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/bmatcuk/doublestar/v4"

	"github.com/svent/quarry/internal/bedrock"
	"github.com/svent/quarry/internal/config"
	"github.com/svent/quarry/internal/datasource"
	"github.com/svent/quarry/internal/embeddings"
)

const grepMaxResults = 50
const listFilesMaxResults = 200
const semanticFallbackTopK = 5

var embedKeywordForLookup = defaultEmbedKeywordForLookup

// CreateToolDefinitions builds the Bedrock tool specs dynamically based on datasource types.
func CreateToolDefinitions(datasources []*datasource.Datasource) []types.Tool {
	var tools []types.Tool

	var indexNames []string
	toolsByName := map[string][]string{}

	for _, ds := range datasources {
		switch ds.Type {
		case "index":
			indexNames = append(indexNames, ds.Name)
		case "tools":
			for _, t := range ds.Tools {
				toolsByName[t] = append(toolsByName[t], ds.Name)
			}
		}
	}

	if len(indexNames) > 0 {
		tools = append(tools,
			createListKeywordsTool(indexNames),
			createLookupKeywordsTool(indexNames),
			createFetchContentTool(indexNames),
		)
	}

	if names, ok := toolsByName["list_files"]; ok {
		tools = append(tools, createListFilesTool(names))
	}
	if names, ok := toolsByName["read_file"]; ok {
		tools = append(tools, createReadFileTool(names))
	}
	if names, ok := toolsByName["grep"]; ok {
		tools = append(tools, createGrepTool(names))
	}

	return tools
}

func dsDescription(names []string) string {
	namesJSON, _ := json.Marshal(names)
	return fmt.Sprintf("Name of the datasource. Must be one of: %s", string(namesJSON))
}

func createListKeywordsTool(names []string) types.Tool {
	return &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String("list_keywords"),
			Description: aws.String("List all available keywords for a datasource. Use this to discover what keywords you can search for."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: newDocument(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"datasource": map[string]interface{}{
							"type":        "string",
							"description": dsDescription(names),
						},
					},
					"required": []interface{}{"datasource"},
				}),
			},
		},
	}
}

func createLookupKeywordsTool(names []string) types.Tool {
	return &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String("lookup_keywords"),
			Description: aws.String("Look up doc index entries by keyword within a datasource."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: newDocument(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"datasource": map[string]interface{}{
							"type":        "string",
							"description": dsDescription(names),
						},
						"keywords": map[string]interface{}{
							"type":     "array",
							"items":    map[string]interface{}{"type": "string"},
							"minItems": 1,
						},
					},
					"required": []interface{}{"datasource", "keywords"},
				}),
			},
		},
	}
}

func createFetchContentTool(names []string) types.Tool {
	return &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String("fetch_content"),
			Description: aws.String("Fetch the full text of a document by filename from a datasource index."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: newDocument(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"datasource": map[string]interface{}{
							"type":        "string",
							"description": dsDescription(names),
						},
						"filename": map[string]interface{}{
							"type":      "string",
							"minLength": 1,
						},
					},
					"required": []interface{}{"datasource", "filename"},
				}),
			},
		},
	}
}

func createListFilesTool(names []string) types.Tool {
	return &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String("list_files"),
			Description: aws.String("List files matching a path or glob pattern within a datasource directory. Supports ** for recursive matching."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: newDocument(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"datasource": map[string]interface{}{
							"type":        "string",
							"description": dsDescription(names),
						},
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Glob pattern relative to the datasource root. Examples: \".\", \"*.go\", \"src/**/*.ts\", \"docs/\".",
							"minLength":   1,
						},
					},
					"required": []interface{}{"datasource", "pattern"},
				}),
			},
		},
	}
}

func createReadFileTool(names []string) types.Tool {
	return &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String("read_file"),
			Description: aws.String("Read the contents of a file from a datasource directory."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: newDocument(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"datasource": map[string]interface{}{
							"type":        "string",
							"description": dsDescription(names),
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path relative to the datasource root.",
							"minLength":   1,
						},
					},
					"required": []interface{}{"datasource", "path"},
				}),
			},
		},
	}
}

func createGrepTool(names []string) types.Tool {
	return &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String("grep"),
			Description: aws.String("Search file contents using a regex pattern within a datasource directory. Uses smart-case (case-insensitive unless pattern contains uppercase)."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: newDocument(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"datasource": map[string]interface{}{
							"type":        "string",
							"description": dsDescription(names),
						},
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Regex pattern to search for.",
							"minLength":   1,
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File or directory path relative to the datasource root. Defaults to \".\" (entire datasource).",
						},
					},
					"required": []interface{}{"datasource", "pattern"},
				}),
			},
		},
	}
}

// ExecuteTool executes a named tool with the given input and returns the result as a JSON string.
func ExecuteTool(
	name string,
	input map[string]interface{},
	datasources []*datasource.Datasource,
	debug bool,
) string {
	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Tool request: %s\n", formatDebug(map[string]interface{}{
			"name": name,
			"args": input,
		}))
		fmt.Fprintln(os.Stderr, "[ END ] Tool request")
	}

	var result interface{}

	switch name {
	case "list_keywords":
		result = executeListKeywords(input, datasources)
	case "lookup_keywords":
		result = executeLookupKeywords(input, datasources)
	case "fetch_content":
		result = executeFetchContent(input, datasources)
	case "list_files":
		result = executeListFiles(input, datasources)
	case "read_file":
		result = executeReadFile(input, datasources)
	case "grep":
		result = executeGrep(input, datasources)
	default:
		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] unknown tool call: %s\n", name)
		}
		result = map[string]string{"error": fmt.Sprintf("Unknown tool: %s", name)}
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] %s result: %s\n", name, formatDebug(result))
		fmt.Fprintf(os.Stderr, "[ END ] %s result\n", name)
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b)
}

func findDatasource(name string, datasources []*datasource.Datasource) *datasource.Datasource {
	for _, ds := range datasources {
		if ds.Name == name {
			return ds
		}
	}
	return nil
}

func datasourceNames(datasources []*datasource.Datasource) []string {
	names := make([]string, len(datasources))
	for i, ds := range datasources {
		names[i] = ds.Name
	}
	return names
}

func unknownDatasourceError(name string, datasources []*datasource.Datasource) interface{} {
	return map[string]string{
		"error": fmt.Sprintf(
			`Unknown datasource "%s". Available: %s`,
			name,
			strings.Join(datasourceNames(datasources), ", "),
		),
	}
}

// --- Index datasource tools ---

func executeListKeywords(input map[string]interface{}, datasources []*datasource.Datasource) interface{} {
	dsName, _ := input["datasource"].(string)
	ds := findDatasource(dsName, datasources)
	if ds == nil {
		return unknownDatasourceError(dsName, datasources)
	}
	return map[string]interface{}{
		"datasource": ds.Name,
		"keywords":   datasource.UniqueKeywords(ds.Entries),
	}
}

func executeLookupKeywords(input map[string]interface{}, datasources []*datasource.Datasource) interface{} {
	dsName, _ := input["datasource"].(string)
	ds := findDatasource(dsName, datasources)
	if ds == nil {
		return unknownDatasourceError(dsName, datasources)
	}

	keywordsRaw, _ := input["keywords"].([]interface{})
	keywords := make([]string, 0, len(keywordsRaw))
	for _, kw := range keywordsRaw {
		if s, ok := kw.(string); ok {
			keywords = append(keywords, s)
		}
	}

	entries := datasource.LookupKeywords(ds.Entries, keywords)
	_, unknownKeywords := datasource.PartitionKeywords(ds.Entries, keywords)

	if ds.Embeddings != nil && len(unknownKeywords) > 0 {
		semanticEntries := lookupSemanticFallback(ds, unknownKeywords, semanticFallbackTopK)
		entries = mergeKeywordAndSemanticEntries(entries, semanticEntries)
	}

	return map[string]interface{}{
		"entries": entries,
	}
}

func defaultEmbedKeywordForLookup(ctx context.Context, keyword string) ([]float32, error) {
	client, err := bedrock.GetChatClient()
	if err != nil {
		return nil, err
	}
	return embeddings.EmbedText(ctx, client, config.GetEmbeddingModelID(), keyword)
}

func lookupSemanticFallback(ds *datasource.Datasource, unknownKeywords []string, topK int) []datasource.DocIndexEntry {
	if ds == nil || ds.Embeddings == nil || len(unknownKeywords) == 0 || topK <= 0 {
		return nil
	}

	entryByFilename := make(map[string]datasource.DocIndexEntry, len(ds.Entries))
	for _, entry := range ds.Entries {
		entryByFilename[entry.Filename] = entry
	}

	bestScores := make(map[string]float64)
	for _, keyword := range unknownKeywords {
		queryVector, err := embedKeywordForLookup(context.Background(), keyword)
		if err != nil {
			continue
		}

		hits := datasource.SearchEmbedding(ds.Embeddings, queryVector, topK)
		for _, hit := range hits {
			if score, ok := bestScores[hit.Filename]; !ok || hit.Score > score {
				bestScores[hit.Filename] = hit.Score
			}
		}
	}
	if len(bestScores) == 0 {
		return nil
	}

	type scoredEntry struct {
		filename string
		score    float64
	}
	scored := make([]scoredEntry, 0, len(bestScores))
	for filename, score := range bestScores {
		scored = append(scored, scoredEntry{filename: filename, score: score})
	}
	slices.SortFunc(scored, func(a, b scoredEntry) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return strings.Compare(a.filename, b.filename)
	})
	if len(scored) > topK {
		scored = scored[:topK]
	}

	results := make([]datasource.DocIndexEntry, 0, len(scored))
	for _, item := range scored {
		entry, ok := entryByFilename[item.filename]
		if !ok {
			continue
		}
		results = append(results, entry)
	}
	return results
}

func mergeKeywordAndSemanticEntries(
	exact []datasource.DocIndexEntry,
	semantic []datasource.DocIndexEntry,
) []datasource.DocIndexEntry {
	if len(semantic) == 0 {
		return exact
	}

	seen := make(map[string]bool, len(exact)+len(semantic))
	merged := make([]datasource.DocIndexEntry, 0, len(exact)+len(semantic))
	for _, entry := range exact {
		if seen[entry.Filename] {
			continue
		}
		seen[entry.Filename] = true
		merged = append(merged, entry)
	}
	for _, entry := range semantic {
		if seen[entry.Filename] {
			continue
		}
		seen[entry.Filename] = true
		merged = append(merged, entry)
	}
	return merged
}

func executeFetchContent(input map[string]interface{}, datasources []*datasource.Datasource) interface{} {
	dsName, _ := input["datasource"].(string)
	ds := findDatasource(dsName, datasources)
	if ds == nil {
		return unknownDatasourceError(dsName, datasources)
	}

	filename, _ := input["filename"].(string)

	// Validate filename exists in datasource entries.
	found := false
	for _, entry := range ds.Entries {
		if entry.Filename == filename {
			found = true
			break
		}
	}
	if !found {
		return map[string]string{"error": "Unknown filename. Use lookup_keywords first."}
	}

	// Resolve file path with traversal guard.
	resolved := datasource.ResolveFilePath(ds, filename)
	if resolved == nil {
		return map[string]string{"error": "Invalid filename path."}
	}

	content, err := os.ReadFile(*resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{"error": fmt.Sprintf("File not found on disk: %s", filename)}
		}
		return map[string]string{"error": fmt.Sprintf("Failed to read file: %s", filename)}
	}

	return map[string]interface{}{
		"filename": filename,
		"content":  string(content),
	}
}

// --- Tools datasource tools ---

func executeListFiles(input map[string]interface{}, datasources []*datasource.Datasource) interface{} {
	dsName, _ := input["datasource"].(string)
	ds := findDatasource(dsName, datasources)
	if ds == nil {
		return unknownDatasourceError(dsName, datasources)
	}

	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return map[string]string{"error": "pattern is required."}
	}

	// Reject patterns that attempt path traversal.
	if strings.Contains(pattern, "..") {
		return map[string]string{"error": "Pattern must not contain '..'."}
	}

	// If the pattern points to a directory, list its immediate contents.
	resolved := datasource.ResolveFilePath(ds, pattern)
	if resolved != nil {
		info, err := os.Stat(*resolved)
		if err == nil && info.IsDir() {
			pattern = strings.TrimRight(pattern, "/") + "/*"
		}
	}

	fsys := os.DirFS(ds.ResolvedDatapath)
	matches, err := doublestar.Glob(fsys, pattern)
	if err != nil {
		return map[string]string{"error": fmt.Sprintf("Invalid glob pattern: %s", err.Error())}
	}

	// Filter out directories, keep only files.
	var files []string
	truncated := false
	for _, m := range matches {
		resolved := datasource.ResolveFilePath(ds, m)
		if resolved == nil {
			continue
		}
		info, err := os.Stat(*resolved)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, m)
		if len(files) >= listFilesMaxResults {
			truncated = true
			break
		}
	}

	return map[string]interface{}{
		"files":     files,
		"truncated": truncated,
	}
}

func executeReadFile(input map[string]interface{}, datasources []*datasource.Datasource) interface{} {
	dsName, _ := input["datasource"].(string)
	ds := findDatasource(dsName, datasources)
	if ds == nil {
		return unknownDatasourceError(dsName, datasources)
	}

	filePath, _ := input["path"].(string)
	if filePath == "" {
		return map[string]string{"error": "path is required."}
	}

	resolved := datasource.ResolveFilePath(ds, filePath)
	if resolved == nil {
		return map[string]string{"error": "Invalid file path."}
	}

	content, err := os.ReadFile(*resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{"error": fmt.Sprintf("File not found: %s", filePath)}
		}
		return map[string]string{"error": fmt.Sprintf("Failed to read file: %s", filePath)}
	}

	return map[string]interface{}{
		"path":    filePath,
		"content": string(content),
	}
}

func executeGrep(input map[string]interface{}, datasources []*datasource.Datasource) interface{} {
	dsName, _ := input["datasource"].(string)
	ds := findDatasource(dsName, datasources)
	if ds == nil {
		return unknownDatasourceError(dsName, datasources)
	}

	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return map[string]string{"error": "pattern is required."}
	}

	searchPath, _ := input["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}

	resolved := datasource.ResolveFilePath(ds, searchPath)
	if resolved == nil {
		return map[string]string{"error": "Invalid search path."}
	}

	// Build rg command.
	args := []string{
		"--smart-case",
		"--line-number",
		"--no-heading",
		"--",
		pattern,
		*resolved,
	}

	cmd := exec.Command("rg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return map[string]string{"error": fmt.Sprintf("failed to create pipe: %s", err.Error())}
	}

	if err := cmd.Start(); err != nil {
		return map[string]string{"error": fmt.Sprintf("failed to start rg: %s", err.Error())}
	}

	// Read up to grepMaxResults lines.
	scanner := bufio.NewScanner(stdout)
	var lines []string
	truncated := false
	for scanner.Scan() {
		line := scanner.Text()
		// Strip the resolved datapath prefix to return relative paths.
		line = stripDatapathPrefix(line, ds.ResolvedDatapath)
		lines = append(lines, line)
		if len(lines) >= grepMaxResults {
			truncated = true
			break
		}
	}

	// Drain remaining stdout so rg doesn't get a broken pipe error, then wait.
	if truncated {
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return map[string]string{"error": fmt.Sprintf("grep read error: %s", err.Error())}
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !(errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1) {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = waitErr.Error()
			}
			return map[string]string{"error": fmt.Sprintf("grep failed: %s", detail)}
		}
	}

	return map[string]interface{}{
		"matches":   strings.Join(lines, "\n"),
		"total":     len(lines),
		"truncated": truncated,
	}
}

func stripDatapathPrefix(line string, datapath string) string {
	prefixBases := []string{datapath}
	if abs, err := filepath.Abs(datapath); err == nil {
		prefixBases = append(prefixBases, abs)
	}
	if canonical, err := filepath.EvalSymlinks(datapath); err == nil {
		prefixBases = append(prefixBases, canonical)
	}

	for _, base := range prefixBases {
		prefix := base + string(filepath.Separator)
		if strings.HasPrefix(line, prefix) {
			return line[len(prefix):]
		}
	}
	return line
}

// newDocument creates a Bedrock document from an interface value.
func newDocument(v interface{}) document.Interface {
	return document.NewLazyDocument(v)
}

// truncateDebugValue recursively truncates strings > 100 chars.
func truncateDebugValue(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		if len(v) > 100 {
			return v[:100] + "..."
		}
		return v
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = truncateDebugValue(item)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, val := range v {
			result[k] = truncateDebugValue(val)
		}
		return result
	case map[string]string:
		result := make(map[string]interface{})
		for k, val := range v {
			result[k] = truncateDebugValue(val)
		}
		return result
	default:
		return v
	}
}

func formatDebug(payload interface{}) string {
	truncated := truncateDebugValue(payload)
	b, _ := json.MarshalIndent(truncated, "", "  ")
	return string(b)
}
