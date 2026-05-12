package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	flag "github.com/spf13/pflag"

	"github.com/svent/quarry/internal/bedrock"
	"github.com/svent/quarry/internal/config"
	"github.com/svent/quarry/internal/datasource"
	"github.com/svent/quarry/internal/embeddings"
)

const concurrency = 4

var systemPrompt = `You are creating an index of files. This index should allow for automatic retrieval of relevant files by an LLM agent.

Return ONLY a single JSON object (no markdown, no code fences, no extra text) with these fields:
- "filename" (string): the relative path of the file as provided to you.
- "content" (string): a concise description of what topics are covered in the file. It must not reproduce details, but guide somebody searching for information to know what is detailed in that file. Be as concise as possible without skipping critical details.
- "keywords" (string array): a list of keywords that characterise the file content and make it discoverable.

Return NOTHING other than the JSON object.`

type cliArgs struct {
	outputBase string
	directory  string
	extensions []string
	dryRun     bool
	filter     *regexp.Regexp
}

type indexEntry struct {
	Filename string   `json:"filename"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
}

func parseArgs() (*cliArgs, error) {
	outputBase := flag.String("output", "", "Output base path (required unless --dry-run). Writes <output>.jsonl and <output>.emb.gob")
	dryRun := flag.Bool("dry-run", false, "List files that would be indexed, without indexing")
	filterStr := flag.String("filter", "", "Only index files whose relative path matches this regex")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ingest [options] <directory> <ext1> [ext2] ...")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nExamples:")
		fmt.Fprintln(os.Stderr, "  ingest --output datasources/docs-index ./docs mdx")
		fmt.Fprintln(os.Stderr, "  ingest --dry-run ./docs mdx")
		fmt.Fprintln(os.Stderr, `  ingest --output datasources/docs-index --filter "api/" ./docs mdx`)
	}
	flag.Parse()

	var filter *regexp.Regexp
	if *filterStr != "" {
		var err error
		filter, err = regexp.Compile(*filterStr)
		if err != nil {
			flag.Usage()
			fmt.Fprintf(os.Stderr, "\nError: invalid regex for --filter: %s\n", *filterStr)
			return nil, fmt.Errorf("invalid filter regex")
		}
	}

	positional := flag.Args()
	if len(positional) < 2 {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\nError: provide a directory and at least one file extension.")
		return nil, fmt.Errorf("missing positional arguments")
	}

	if !*dryRun && *outputBase == "" {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\nError: --output is required unless --dry-run is specified.")
		return nil, fmt.Errorf("missing --output argument")
	}
	if !*dryRun && filepath.Ext(*outputBase) != "" {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "\nError: --output must be a base path without extension.")
		return nil, fmt.Errorf("invalid --output argument")
	}

	directory := positional[0]
	extensions := make([]string, len(positional)-1)
	for i, ext := range positional[1:] {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		extensions[i] = ext
	}

	return &cliArgs{
		outputBase: *outputBase,
		directory:  directory,
		extensions: extensions,
		dryRun:     *dryRun,
		filter:     filter,
	}, nil
}

func indexOutputPath(base string) string {
	return base + ".jsonl"
}

func embeddingsOutputPath(base string) string {
	return base + ".emb.gob"
}

func discoverFiles(directory string, extensions []string, filter *regexp.Regexp) ([]string, error) {
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Directory does not exist or is not a directory: %s", directory)
	}

	extSet := make(map[string]bool)
	for _, ext := range extensions {
		extSet[strings.ToLower(ext)] = true
	}

	var matched []string
	err = filepath.WalkDir(directory, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !extSet[ext] {
			return nil
		}
		if filter != nil {
			relPath, _ := filepath.Rel(directory, path)
			relPath = "./" + relPath
			if !filter.MatchString(relPath) {
				return nil
			}
		}
		matched = append(matched, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(matched)
	return matched, nil
}

var codeFenceStartRe = regexp.MustCompile(`(?i)^\x60\x60\x60(?:json)?\s*`)
var codeFenceEndRe = regexp.MustCompile(`\s*\x60\x60\x60\s*$`)

type converseAPI interface {
	Converse(
		ctx context.Context,
		params *bedrockruntime.ConverseInput,
		optFns ...func(*bedrockruntime.Options),
	) (*bedrockruntime.ConverseOutput, error)
}

type ingestBedrockAPI interface {
	converseAPI
	embeddings.InvokeModelAPI
}

func extractConverseMessage(output *bedrockruntime.ConverseOutput) (types.Message, error) {
	if output == nil || output.Output == nil {
		return types.Message{}, fmt.Errorf("converse response did not include a message")
	}
	msg, ok := output.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return types.Message{}, fmt.Errorf("unexpected converse output type: %T", output.Output)
	}
	return msg.Value, nil
}

func indexFile(
	ctx context.Context,
	client converseAPI,
	modelID string,
	filePath string,
	relativeRoot string,
	content []byte,
) (*indexEntry, error) {
	relPath, err := filepath.Rel(relativeRoot, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute relative path: %w", err)
	}
	relativePath := "./" + relPath

	output, err := client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: &modelID,
		System: []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: systemPrompt},
		},
		Messages: []types.Message{
			{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{
						Value: fmt.Sprintf("Here is the file to index.\n\nFilename: %s\n\n---\n%s\n---", relativePath, string(content)),
					},
				},
			},
		},
		ServiceTier: &types.ServiceTier{Type: types.ServiceTierTypeFlex},
	})
	if err != nil {
		return nil, fmt.Errorf("Bedrock Converse error: %w", err)
	}

	responseMsg, err := extractConverseMessage(output)
	if err != nil {
		return nil, fmt.Errorf("invalid converse response for %s: %w", relativePath, err)
	}
	var textParts []string
	for _, block := range responseMsg.Content {
		if textBlock, ok := block.(*types.ContentBlockMemberText); ok {
			textParts = append(textParts, textBlock.Value)
		}
	}
	text := strings.Join(textParts, "")

	// Strip potential markdown code fences.
	cleaned := codeFenceStartRe.ReplaceAllString(text, "")
	cleaned = codeFenceEndRe.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response for %s: %w\nResponse: %s", relativePath, err, cleaned[:min(len(cleaned), 200)])
	}

	// Validate shape.
	if _, ok := parsed["filename"]; !ok {
		return nil, fmt.Errorf("invalid response shape for %s: missing 'filename'", relativePath)
	}
	if _, ok := parsed["content"]; !ok {
		return nil, fmt.Errorf("invalid response shape for %s: missing 'content'", relativePath)
	}
	if _, ok := parsed["keywords"]; !ok {
		return nil, fmt.Errorf("invalid response shape for %s: missing 'keywords'", relativePath)
	}

	// Re-parse into typed struct.
	entryJSON, _ := json.Marshal(parsed)
	var entry indexEntry
	if err := json.Unmarshal(entryJSON, &entry); err != nil {
		return nil, fmt.Errorf("failed to parse index entry for %s: %w", relativePath, err)
	}

	// Normalize filename to the one we provided.
	entry.Filename = relativePath

	return &entry, nil
}

type taskResult struct {
	index     int
	entry     *indexEntry
	embedding []float32
	err       error
}

func runWithConcurrency(
	ctx context.Context,
	client ingestBedrockAPI,
	chatModelID string,
	embeddingModelID string,
	files []string,
	resolvedDir string,
) []taskResult {
	results := make([]taskResult, len(files))
	var completed int64
	total := len(files)

	taskCh := make(chan int, len(files))
	for i := range files {
		taskCh <- i
	}
	close(taskCh)

	var wg sync.WaitGroup
	workerCount := concurrency
	if workerCount > len(files) {
		workerCount = len(files)
	}

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range taskCh {
				filePath := files[idx]
				relPath, _ := filepath.Rel(resolvedDir, filePath)
				n := atomic.AddInt64(&completed, 1)
				fmt.Fprintf(os.Stderr, "  [%d/%d] ./%s\n", n, total, relPath)

				content, err := os.ReadFile(filePath)
				if err != nil {
					results[idx] = taskResult{
						index: idx,
						err:   fmt.Errorf("failed to read file: %w", err),
					}
					continue
				}

				entry, err := indexFile(ctx, client, chatModelID, filePath, resolvedDir, content)
				if err != nil {
					results[idx] = taskResult{index: idx, err: err}
					continue
				}

				vector, err := embeddings.EmbedTextChunkAverage(
					ctx,
					client,
					embeddingModelID,
					string(content),
					embeddings.DefaultChunkSizeRunes,
				)
				if err != nil {
					results[idx] = taskResult{
						index: idx,
						err:   fmt.Errorf("failed to embed %s: %w", relPath, err),
					}
					continue
				}

				normalized, err := embeddings.NormalizeVector(vector)
				if err != nil {
					results[idx] = taskResult{
						index: idx,
						err:   fmt.Errorf("failed to normalize embedding for %s: %w", relPath, err),
					}
					continue
				}

				results[idx] = taskResult{
					index:     idx,
					entry:     entry,
					embedding: normalized,
				}
			}
		}()
	}

	wg.Wait()
	return results
}

func run() error {
	cliArgs, err := parseArgs()
	if err != nil {
		return err
	}

	resolvedDir, err := filepath.Abs(cliArgs.directory)
	if err != nil {
		return fmt.Errorf("failed to resolve directory: %w", err)
	}

	filterDesc := ""
	if cliArgs.filter != nil {
		filterDesc = fmt.Sprintf(" matching %s", cliArgs.filter.String())
	}
	fmt.Fprintf(os.Stderr, "Scanning %s for %s files%s...\n", resolvedDir, strings.Join(cliArgs.extensions, ", "), filterDesc)

	files, err := discoverFiles(resolvedDir, cliArgs.extensions, cliArgs.filter)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No matching files found.")
		os.Exit(1)
	}

	if cliArgs.dryRun {
		for _, filePath := range files {
			relPath, _ := filepath.Rel(resolvedDir, filePath)
			fmt.Println("./" + relPath)
		}
		fmt.Fprintf(os.Stderr, "\nFound %d file(s).\n", len(files))
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found %d file(s). Indexing with concurrency %d...\n\n", len(files), concurrency)

	chatModelID, err := config.GetChatModelID()
	if err != nil {
		return fmt.Errorf("failed to resolve chat model ID: %w", err)
	}
	embeddingModelID := config.GetEmbeddingModelID()

	client, err := bedrock.GetChatClient()
	if err != nil {
		return fmt.Errorf("failed to get Bedrock client: %w", err)
	}

	ctx := context.Background()
	results := runWithConcurrency(ctx, client, chatModelID, embeddingModelID, files, resolvedDir)

	var entries []string
	embeddingDocs := make([]datasource.EmbeddingDoc, 0, len(files))
	failures := 0

	for i, result := range results {
		if result.err != nil {
			failures++
			relPath, _ := filepath.Rel(resolvedDir, files[i])
			fmt.Fprintf(os.Stderr, "\n  ERROR for ./%s: %v\n", relPath, result.err)
		} else {
			b, _ := json.Marshal(result.entry)
			entries = append(entries, string(b))
			embeddingDocs = append(embeddingDocs, datasource.EmbeddingDoc{
				Filename: result.entry.Filename,
				Vector:   result.embedding,
			})
		}
	}
	if len(entries) == 0 {
		return fmt.Errorf("no files were indexed successfully")
	}

	output := strings.Join(entries, "\n") + "\n"
	indexPath := indexOutputPath(cliArgs.outputBase)
	embeddingsPath := embeddingsOutputPath(cliArgs.outputBase)

	if err := os.WriteFile(indexPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	embeddingIndex := &datasource.EmbeddingIndex{
		Version:    datasource.EmbeddingIndexVersion,
		ModelID:    embeddingModelID,
		Dimensions: 0,
		Entries:    embeddingDocs,
	}
	if len(embeddingDocs) > 0 {
		embeddingIndex.Dimensions = len(embeddingDocs[0].Vector)
	}
	if err := datasource.WriteEmbeddingIndex(embeddingsPath, embeddingIndex); err != nil {
		return fmt.Errorf("failed to write embeddings file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nWrote %d entries to %s\n", len(entries), indexPath)
	fmt.Fprintf(os.Stderr, "Wrote %d embeddings to %s\n", len(embeddingDocs), embeddingsPath)
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "%d file(s) failed.\n", failures)
		os.Exit(1)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}
