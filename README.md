# Quarry

Quarry is a **proof-of-concept / demo** for an AI-powered research chat
application backed by [AWS Bedrock](https://aws.amazon.com/bedrock/). It lets
you point an LLM at your own document collections and codebases, then ask
questions in natural language. The LLM uses tool calls to search indexes, browse
files, and read content before answering.

> **Note:** This is a demo project, not a production-ready product. Expect rough
> edges, hardcoded defaults, and minimal error handling in places.

## Components

| Binary | Source | Description |
|--------|--------|-------------|
| `server` | `cmd/server` | HTTP server that serves the web chat UI and the `/api/chat` endpoint (with SSE streaming). All HTML/CSS/JS assets are embedded in the binary. |
| `chat` | `cmd/chat` | Terminal chat client with streaming markdown rendering (via [glamour](https://github.com/charmbracelet/glamour)). |
| `ingest` | `cmd/ingest` | Indexing tool that walks a directory of files and generates a keyword index (`.jsonl`) plus an embeddings sidecar (`.emb.gob`). |
| `MenuBarChat` | `macos/MenuBarChat` | macOS menu bar app that wraps the web UI in a native popover. Launches the `server` binary automatically. |

## Prerequisites

### Go binaries (`server`, `chat`, `ingest`)

- **Go 1.23+**
- **AWS credentials** with access to [Amazon Bedrock](https://aws.amazon.com/bedrock/)
  (see [Configuration](#configuration) below)
- **ripgrep** (`rg`) — required at runtime if any datasource uses the `grep`
  tool. Install via `brew install ripgrep` or see
  [ripgrep installation](https://github.com/BurntSushi/ripgrep#installation).

### macOS menu bar app (`MenuBarChat`)

- **Xcode** (or Xcode Command Line Tools) — provides the Swift toolchain.
  No Apple Developer Certificate or paid membership is required; the binary is
  ad-hoc signed automatically.
- The `server` binary must be built first (`make server`).

## Building

```bash
# Build all Go binaries (chat, server, ingest)
make all

# Build only the server
make server

# Build the macOS menu bar app (universal binary: arm64 + x86_64)
make menubar

# Build server + menu bar app together
make server menubar
```

All binaries are placed in `bin/`.

## Configuration

### AWS Credentials

Quarry uses AWS Bedrock for LLM inference. Provide credentials in any of
these ways:

1. **Environment variables**: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and
   optionally `AWS_SESSION_TOKEN`.
2. **`.env` file** in the working directory — the server and chat binaries
   load this automatically on startup. The `.env` file is re-read periodically
   to support credential rotation.
3. **AWS profiles / default credential chain** — if no explicit credentials are
   set, the standard AWS SDK credential resolution applies.

Set the AWS region via `AWS_REGION` or `AWS_DEFAULT_REGION` (defaults to
`eu-central-1`).

Embedding model configuration:

- `EMBEDDING_MODEL_ID` (optional): Bedrock model ID used by `ingest` and semantic fallback in `lookup_keywords`.
  Defaults to `amazon.titan-embed-text-v2:0`.

Bedrock service tiers:

- Interactive chat requests (`server`, `chat`, and semantic fallback embeddings)
  use the Standard tier.
- `ingest` uses the Flex tier for file-summary/index generation. Ingest
  embeddings stay on Standard because the default Titan embedding model does not
  support Flex.

### Datasources

Quarry needs at least one datasource to be useful. Datasources are configured
in `datasources/datasources.json` (relative to the working directory).

There are two types:

#### Index datasources

Pre-indexed document collections. The LLM discovers content through keyword
search and then fetches individual files. If a keyword is unknown, `lookup_keywords`
can fall back to semantic search using document embeddings.

```json
{
  "datasources": [
    {
      "type": "index",
      "file": "docs-index.jsonl",
      "embeddings_file": "docs-index.emb.gob",
      "datapath": "../path/to/docs",
      "description": "Project documentation"
    }
  ]
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `type` | No | `"index"` (default if omitted) |
| `name` | No | Display name. Derived from `file` (minus extension) if omitted. |
| `file` | Yes | Path to the JSONL index file, relative to `datasources.json`. |
| `embeddings_file` | No | Path to the embeddings sidecar file, relative to `datasources.json`. If provided, it must be readable and valid. |
| `datapath` | Yes | Root directory of the source files, relative to `datasources.json`. |
| `description` | Yes | Description shown to the LLM so it knows what this datasource contains. |

**Generating the index** with `ingest`:

```bash
bin/ingest --output datasources/docs-index ./path/to/docs md mdx txt
```

This walks the directory, sends each file to the LLM, and produces a JSONL file
where each line looks like:

```json
{"filename": "./getting-started.md", "content": "Guide for initial setup and installation", "keywords": ["setup", "install", "quickstart"]}
```

It also writes a companion embeddings file for semantic fallback:

```
datasources/docs-index.emb.gob
```

Useful `ingest` options:

```
--output <base>    Output base path; writes <base>.jsonl and <base>.emb.gob (required unless --dry-run)
--dry-run          List files that would be indexed without calling the LLM
--filter <regex>   Only index files whose path matches this regex
```

#### Tools datasources

Give the LLM direct filesystem access (read-only, sandboxed to `datapath`) to
browse and search a codebase.

```json
{
  "datasources": [
    {
      "type": "tools",
      "name": "my-project",
      "datapath": "../path/to/code",
      "description": "Source code of the my-project repository",
      "tools": ["list_files", "read_file", "grep"]
    }
  ]
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `type` | Yes | Must be `"tools"`. |
| `name` | Yes | Display name for this datasource. |
| `datapath` | Yes | Root directory the tools operate in, relative to `datasources.json`. |
| `description` | Yes | Description shown to the LLM. |
| `tools` | Yes | Subset of: `list_files`, `read_file`, `grep`. |

#### Mixed example

You can combine both types:

```json
{
  "datasources": [
    {
      "type": "index",
      "file": "docs-index.jsonl",
      "embeddings_file": "docs-index.emb.gob",
      "datapath": "../docs",
      "description": "Product documentation and guides"
    },
    {
      "type": "tools",
      "name": "backend",
      "datapath": "../backend/src",
      "description": "Backend source code (Go)",
      "tools": ["list_files", "read_file", "grep"]
    }
  ]
}
```

## Usage

### Web UI (server)

```bash
bin/server                # starts on an OS-assigned port
bin/server --port 8080    # or a specific port
```

Open the printed URL (e.g. `http://localhost:8080/ui`) in a browser.

### Terminal chat

```bash
bin/chat "How does authentication work?"
bin/chat --debug "What files handle error logging?"
```

The binaries automatically find `datasources/datasources.json` relative to
their own location (i.e. `bin/../datasources/datasources.json`), so they work
from any working directory. The current working directory is used as a fallback.

### macOS menu bar app

```bash
bin/MenuBarChat
```

The app appears as an icon in the macOS menu bar. It automatically finds and
launches `bin/server` from the same directory, so both binaries must be in
`bin/` with the standard repository layout:

```
quarry/
  bin/
    server
    MenuBarChat
  datasources/
    datasources.json
```

The `CHATBOT_ROOT` environment variable is still supported as a fallback — set
it to the repository root if you run `MenuBarChat` from a different location.

## Project Layout

```
cmd/
  chat/          Terminal chat client
  server/        HTTP server + embedded web UI
  ingest/        Index generation tool
internal/
  chat/          Core chat logic, tool execution, system prompt
  config/        AWS config, credential loading
  datasource/    Datasource loading, validation, file sandboxing
  server/        Embedded HTML/CSS/JS assets
macos/
  MenuBarChat/   SwiftUI menu bar app (Swift Package Manager)
datasources/     Datasource config and index files (gitignored)
```

## License

[MIT](LICENSE)
