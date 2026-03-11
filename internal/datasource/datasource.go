package datasource

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DocIndexEntry represents a single entry in a JSONL keyword index file.
type DocIndexEntry struct {
	Filename string   `json:"filename"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
}

// DatasourceConfig represents a single datasource entry in the config file.
type DatasourceConfig struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	File        string   `json:"file"`
	Embeddings  string   `json:"embeddings_file"`
	Datapath    string   `json:"datapath"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

// DatasourcesFile represents the top-level structure of datasources.json.
type DatasourcesFile struct {
	Datasources []DatasourceConfig `json:"datasources"`
}

// Datasource is a loaded and validated datasource ready for use.
type Datasource struct {
	Type             string
	Name             string
	Description      string
	Entries          []DocIndexEntry
	Embeddings       *EmbeddingIndex
	ResolvedDatapath string
	Tools            []string
}

const EmbeddingIndexVersion = 1

// EmbeddingDoc stores one document vector entry for semantic lookup.
type EmbeddingDoc struct {
	Filename string
	Vector   []float32
}

// EmbeddingIndex stores a persisted embedding sidecar.
type EmbeddingIndex struct {
	Version    int
	ModelID    string
	Dimensions int
	Entries    []EmbeddingDoc
}

// ScoredEmbeddingHit is one semantic lookup hit.
type ScoredEmbeddingHit struct {
	Filename string
	Score    float64
}

// AllowedTools defines the set of valid tool names for "tools" type datasources.
var AllowedTools = map[string]bool{
	"list_files": true,
	"read_file":  true,
	"grep":       true,
}

var (
	datasourceCache []*Datasource
	cacheMu         sync.Mutex
)

// validateConfig validates the raw parsed JSON as a valid datasources config.
func validateConfig(raw map[string]interface{}) (*DatasourcesFile, error) {
	dsRaw, ok := raw["datasources"]
	if !ok {
		return nil, fmt.Errorf(`datasources config must contain a "datasources" array.`)
	}

	dsArray, ok := dsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf(`datasources config must contain a "datasources" array.`)
	}

	var datasources []DatasourceConfig

	for i, entry := range dsArray {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("datasources[%d] must be an object.", i)
		}

		// Determine type, defaulting to "index" for backwards compatibility.
		dsType, _ := entryMap["type"].(string)
		if dsType == "" {
			dsType = "index"
		}

		datapath, _ := entryMap["datapath"].(string)
		if datapath == "" {
			return nil, fmt.Errorf("datasources[%d].datapath must be a non-empty string.", i)
		}

		description, _ := entryMap["description"].(string)
		if description == "" {
			return nil, fmt.Errorf("datasources[%d].description must be a non-empty string.", i)
		}

		name, _ := entryMap["name"].(string)

		switch dsType {
		case "index":
			file, _ := entryMap["file"].(string)
			if file == "" {
				return nil, fmt.Errorf("datasources[%d].file must be a non-empty string.", i)
			}
			embeddingsFile, hasEmbeddingsFile := entryMap["embeddings_file"]
			embeddingsPath := ""
			if hasEmbeddingsFile {
				embeddingsPath, ok = embeddingsFile.(string)
				if !ok || embeddingsPath == "" {
					return nil, fmt.Errorf("datasources[%d].embeddings_file must be a non-empty string when provided.", i)
				}
			}

			datasources = append(datasources, DatasourceConfig{
				Type:        dsType,
				Name:        name,
				File:        file,
				Embeddings:  embeddingsPath,
				Datapath:    datapath,
				Description: description,
			})

		case "tools":
			if name == "" {
				return nil, fmt.Errorf("datasources[%d].name must be a non-empty string for tools type.", i)
			}

			toolsRaw, ok := entryMap["tools"]
			if !ok {
				return nil, fmt.Errorf("datasources[%d].tools must be a non-empty array for tools type.", i)
			}
			toolsArray, ok := toolsRaw.([]interface{})
			if !ok || len(toolsArray) == 0 {
				return nil, fmt.Errorf("datasources[%d].tools must be a non-empty array for tools type.", i)
			}

			var tools []string
			for j, t := range toolsArray {
				toolName, ok := t.(string)
				if !ok || toolName == "" {
					return nil, fmt.Errorf("datasources[%d].tools[%d] must be a non-empty string.", i, j)
				}
				if !AllowedTools[toolName] {
					allowed := make([]string, 0, len(AllowedTools))
					for k := range AllowedTools {
						allowed = append(allowed, k)
					}
					sort.Strings(allowed)
					return nil, fmt.Errorf(
						"datasources[%d].tools[%d]: unknown tool %q. Allowed: %s",
						i, j, toolName, strings.Join(allowed, ", "),
					)
				}
				tools = append(tools, toolName)
			}

			datasources = append(datasources, DatasourceConfig{
				Type:        dsType,
				Name:        name,
				Datapath:    datapath,
				Description: description,
				Tools:       tools,
			})

		default:
			return nil, fmt.Errorf("datasources[%d].type must be \"index\" or \"tools\", got %q.", i, dsType)
		}
	}

	if len(datasources) == 0 {
		return nil, fmt.Errorf("datasources config must contain at least one entry.")
	}

	return &DatasourcesFile{Datasources: datasources}, nil
}

// ParseIndexEntries parses a JSONL string into a slice of DocIndexEntry.
func ParseIndexEntries(raw string) ([]DocIndexEntry, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	lines := strings.Split(trimmed, "\n")
	var entries []DocIndexEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry DocIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("failed to parse JSONL line: %w", err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// WriteEmbeddingIndex writes an embedding sidecar in gob format.
func WriteEmbeddingIndex(path string, index *EmbeddingIndex) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := gob.NewEncoder(f)
	return enc.Encode(index)
}

// ReadEmbeddingIndex reads an embedding sidecar from gob format.
func ReadEmbeddingIndex(path string) (*EmbeddingIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var index EmbeddingIndex
	dec := gob.NewDecoder(f)
	if err := dec.Decode(&index); err != nil {
		return nil, err
	}
	return &index, nil
}

// ValidateEmbeddingIndex validates embedding shape and checks filenames against index entries.
func ValidateEmbeddingIndex(index *EmbeddingIndex, entries []DocIndexEntry) error {
	if index == nil {
		return fmt.Errorf("embedding index is nil")
	}
	if index.Version != EmbeddingIndexVersion {
		return fmt.Errorf("unsupported embedding index version %d", index.Version)
	}
	if strings.TrimSpace(index.ModelID) == "" {
		return fmt.Errorf("embedding index model_id is empty")
	}
	if index.Dimensions <= 0 {
		return fmt.Errorf("embedding index dimensions must be > 0")
	}

	seenEmbeddings := make(map[string]bool, len(index.Entries))
	for i, item := range index.Entries {
		if strings.TrimSpace(item.Filename) == "" {
			return fmt.Errorf("embedding index entries[%d] has empty filename", i)
		}
		if seenEmbeddings[item.Filename] {
			return fmt.Errorf("embedding index has duplicate filename %q", item.Filename)
		}
		if len(item.Vector) != index.Dimensions {
			return fmt.Errorf(
				"embedding index entries[%d] has dimension %d (expected %d)",
				i,
				len(item.Vector),
				index.Dimensions,
			)
		}
		for _, value := range item.Vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return fmt.Errorf("embedding index entries[%d] contains non-finite values", i)
			}
		}
		seenEmbeddings[item.Filename] = true
	}

	seenDocs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		seenDocs[entry.Filename] = true
	}
	if len(seenDocs) != len(seenEmbeddings) {
		return fmt.Errorf(
			"embedding index/doc index mismatch: docs=%d embeddings=%d",
			len(seenDocs),
			len(seenEmbeddings),
		)
	}
	for filename := range seenDocs {
		if !seenEmbeddings[filename] {
			return fmt.Errorf("embedding index is missing filename %q", filename)
		}
	}
	for filename := range seenEmbeddings {
		if !seenDocs[filename] {
			return fmt.Errorf("embedding index contains unknown filename %q", filename)
		}
	}

	return nil
}

// PartitionKeywords returns query keywords split into known and unknown sets (case-insensitive).
func PartitionKeywords(entries []DocIndexEntry, keywords []string) (known []string, unknown []string) {
	entryKeywords := make(map[string]bool)
	for _, entry := range entries {
		for _, kw := range entry.Keywords {
			normalized := strings.ToLower(strings.TrimSpace(kw))
			if normalized != "" {
				entryKeywords[normalized] = true
			}
		}
	}

	seen := make(map[string]bool)
	for _, kw := range keywords {
		trimmed := strings.TrimSpace(kw)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		if entryKeywords[normalized] {
			known = append(known, trimmed)
		} else {
			unknown = append(unknown, trimmed)
		}
	}

	return known, unknown
}

// SearchEmbedding returns the top-K cosine similarity hits in descending order.
func SearchEmbedding(index *EmbeddingIndex, query []float32, topK int) []ScoredEmbeddingHit {
	if index == nil || topK <= 0 || len(query) != index.Dimensions {
		return nil
	}

	hits := make([]ScoredEmbeddingHit, 0, len(index.Entries))
	for _, item := range index.Entries {
		score, ok := cosineSimilarity(query, item.Vector)
		if !ok {
			continue
		}
		hits = append(hits, ScoredEmbeddingHit{
			Filename: item.Filename,
			Score:    score,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Filename < hits[j].Filename
		}
		return hits[i].Score > hits[j].Score
	})

	if len(hits) > topK {
		return hits[:topK]
	}
	return hits
}

func cosineSimilarity(a []float32, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}

	var dot float64
	var normA float64
	var normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), true
}

// DeriveName extracts a datasource name from a JSONL file path.
func DeriveName(filePath string) string {
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// ResolveFilePath resolves a filename relative to the datasource's datapath,
// guarding against path traversal.
func ResolveFilePath(ds *Datasource, filename string) *string {
	datapathAbs, err := filepath.Abs(ds.ResolvedDatapath)
	if err != nil {
		return nil
	}

	resolved, err := filepath.Abs(filepath.Join(datapathAbs, filename))
	if err != nil {
		return nil
	}

	// Lexical guard: resolved path must stay within the datapath.
	if resolved != datapathAbs &&
		!strings.HasPrefix(resolved, datapathAbs+string(filepath.Separator)) {
		return nil
	}

	canonicalDatapath, ok := canonicalizeExistingPath(datapathAbs)
	if !ok {
		return nil
	}

	rel, err := filepath.Rel(datapathAbs, resolved)
	if err != nil {
		return nil
	}
	candidateUnderCanonicalRoot := filepath.Join(canonicalDatapath, rel)

	canonicalResolved, ok := canonicalizePathForContainment(candidateUnderCanonicalRoot)
	if !ok {
		return nil
	}

	// Symlink guard: canonical target must also stay within canonical datapath.
	if canonicalResolved != canonicalDatapath &&
		!strings.HasPrefix(canonicalResolved, canonicalDatapath+string(filepath.Separator)) {
		return nil
	}

	return &canonicalResolved
}

func canonicalizeExistingPath(path string) (string, bool) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	absCanonical, err := filepath.Abs(canonical)
	if err != nil {
		return "", false
	}
	return absCanonical, true
}

func canonicalizePathForContainment(path string) (string, bool) {
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		absCanonical, absErr := filepath.Abs(canonical)
		if absErr != nil {
			return "", false
		}
		return absCanonical, true
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false
	}

	parent := filepath.Dir(path)
	canonicalParent, parentErr := filepath.EvalSymlinks(parent)
	if parentErr != nil {
		if !errors.Is(parentErr, os.ErrNotExist) {
			return "", false
		}
		return path, true
	}

	joined := filepath.Join(canonicalParent, filepath.Base(path))
	absJoined, absErr := filepath.Abs(joined)
	if absErr != nil {
		return "", false
	}
	return absJoined, true
}

func loadSingleDatasource(cfg DatasourceConfig, configDir string) (*Datasource, error) {
	resolvedDatapath, err := filepath.Abs(filepath.Join(configDir, cfg.Datapath))
	if err != nil {
		return nil, err
	}

	switch cfg.Type {
	case "tools":
		name := cfg.Name
		return &Datasource{
			Type:             "tools",
			Name:             name,
			Description:      cfg.Description,
			ResolvedDatapath: resolvedDatapath,
			Tools:            cfg.Tools,
		}, nil

	default:
		// "index" type
		resolvedFile, err := filepath.Abs(filepath.Join(configDir, cfg.File))
		if err != nil {
			return nil, err
		}

		raw, err := os.ReadFile(resolvedFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read JSONL file %s: %w", resolvedFile, err)
		}

		entries, err := ParseIndexEntries(string(raw))
		if err != nil {
			return nil, fmt.Errorf("failed to parse JSONL file %s: %w", resolvedFile, err)
		}

		name := cfg.Name
		if name == "" {
			name = DeriveName(cfg.File)
		}

		var embeddingIndex *EmbeddingIndex
		if cfg.Embeddings != "" {
			resolvedEmbeddingsFile, err := filepath.Abs(filepath.Join(configDir, cfg.Embeddings))
			if err != nil {
				return nil, err
			}

			embeddingIndex, err = ReadEmbeddingIndex(resolvedEmbeddingsFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read embeddings file %s: %w", resolvedEmbeddingsFile, err)
			}
			if err := ValidateEmbeddingIndex(embeddingIndex, entries); err != nil {
				return nil, fmt.Errorf("invalid embeddings file %s: %w", resolvedEmbeddingsFile, err)
			}
		}

		return &Datasource{
			Type:             "index",
			Name:             name,
			Description:      cfg.Description,
			Entries:          entries,
			Embeddings:       embeddingIndex,
			ResolvedDatapath: resolvedDatapath,
		}, nil
	}
}

// LoadDatasources loads and validates all datasources from the config file.
// Results are cached after first successful load.
func LoadDatasources(configPath string) ([]*Datasource, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if datasourceCache != nil {
		return datasourceCache, nil
	}

	resolvedConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	configDir := filepath.Dir(resolvedConfigPath)

	rawJSON, err := os.ReadFile(resolvedConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read datasources config: %w", err)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(rawJSON, &rawMap); err != nil {
		return nil, fmt.Errorf("datasources config must be a JSON object.")
	}

	config, err := validateConfig(rawMap)
	if err != nil {
		return nil, err
	}

	var datasources []*Datasource
	for _, entry := range config.Datasources {
		ds, err := loadSingleDatasource(entry, configDir)
		if err != nil {
			return nil, err
		}
		datasources = append(datasources, ds)
	}

	// Validate unique names.
	names := make(map[string]bool)
	for _, ds := range datasources {
		if names[ds.Name] {
			return nil, fmt.Errorf(`Duplicate datasource name %q. Each datasource must have a unique name.`, ds.Name)
		}
		names[ds.Name] = true
	}

	datasourceCache = datasources
	return datasources, nil
}

// ClearDatasourceCache resets the cached datasources.
func ClearDatasourceCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	datasourceCache = nil
}

// UniqueKeywords collects all unique keywords from entries and returns them sorted.
func UniqueKeywords(entries []DocIndexEntry) []string {
	set := make(map[string]bool)
	for _, entry := range entries {
		for _, kw := range entry.Keywords {
			set[kw] = true
		}
	}

	keywords := make([]string, 0, len(set))
	for kw := range set {
		keywords = append(keywords, kw)
	}
	sort.Strings(keywords)
	return keywords
}

// LookupKeywords finds entries matching any of the given keywords (case-insensitive).
func LookupKeywords(entries []DocIndexEntry, keywords []string) []DocIndexEntry {
	querySet := make(map[string]bool, len(keywords))
	for _, kw := range keywords {
		querySet[strings.ToLower(kw)] = true
	}

	var matches []DocIndexEntry
	for _, entry := range entries {
		for _, kw := range entry.Keywords {
			if querySet[strings.ToLower(kw)] {
				matches = append(matches, entry)
				break
			}
		}
	}

	return matches
}
