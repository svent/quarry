package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	flag "github.com/spf13/pflag"

	"github.com/svent/quarry/internal/bedrock"
	"github.com/svent/quarry/internal/chat"
	"github.com/svent/quarry/internal/server"
)

const bedrockRefreshIntervalMs = 60_000

type chatRequest struct {
	Question string                 `json:"question"`
	History  []chat.ChatHistoryItem `json:"history"`
}

type chatResponse struct {
	Answer string `json:"answer"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func respondJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func normalizeHistory(raw []chat.ChatHistoryItem) []chat.ChatHistoryItem {
	var normalized []chat.ChatHistoryItem
	for _, entry := range raw {
		if entry.Role != "user" && entry.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		normalized = append(normalized, chat.ChatHistoryItem{
			Role:    entry.Role,
			Content: content,
		})
	}
	return normalized
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusNotFound, errorResponse{Error: "Not Found"})
		return
	}

	var body chatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, errorResponse{Error: "Invalid JSON"})
		return
	}

	question := strings.TrimSpace(body.Question)
	if question == "" {
		respondJSON(w, http.StatusBadRequest, errorResponse{Error: "Missing question"})
		return
	}

	history := normalizeHistory(body.History)

	if err := bedrock.RefreshBedrockClientIfStale(bedrockRefreshIntervalMs); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to refresh Bedrock client: %v\n", err)
	}

	wantsStream := strings.Contains(r.Header.Get("Accept"), "text/event-stream")

	if !wantsStream {
		answer, err := chat.AnswerQuestion(r.Context(), question, chat.AnswerOptions{
			History: history,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to handle chat request: %v\n", err)
			respondJSON(w, http.StatusInternalServerError, errorResponse{Error: "Failed to answer question"})
			return
		}
		respondJSON(w, http.StatusOK, chatResponse{Answer: answer})
		return
	}

	// SSE mode.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		fmt.Fprintf(os.Stderr, "Streaming not supported\n")
		return
	}

	sendEvent := func(event string, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	ch, err := chat.StreamAnswer(r.Context(), question, chat.AnswerOptions{
		History: history,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to handle chat request: %v\n", err)
		// Headers already sent as SSE, just end.
		return
	}

	for event := range ch {
		switch event.Type {
		case "delta":
			data, _ := json.Marshal(map[string]string{"text": event.Text})
			sendEvent("delta", string(data))
		case "tool_start":
			data, _ := json.Marshal(map[string]string{"name": event.Name})
			sendEvent("tool_start", string(data))
		case "tool_end":
			data, _ := json.Marshal(map[string]string{"name": event.Name})
			sendEvent("tool_end", string(data))
		case "done":
			sendEvent("done", "[DONE]")
		}
	}
}

func main() {
	portFlag := flag.Int("port", 0, "Port to listen on (default: PORT env var, or OS-assigned)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: server [--port <port>]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	mux := http.NewServeMux()

	assetsFS, _ := fs.Sub(server.Assets, "assets")
	assetHandler := http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.Method == http.MethodGet {
			http.Redirect(w, r, "/ui", http.StatusFound)
			return
		}
		if r.URL.Path == "/ui" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(server.UIHTML))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") && r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			assetHandler.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/chat" {
			handleChat(w, r)
			return
		}
		respondJSON(w, http.StatusNotFound, errorResponse{Error: "Not Found"})
	})

	var port int
	if flag.Lookup("port").Changed {
		port = *portFlag
	} else if envPort, err := strconv.Atoi(os.Getenv("PORT")); err == nil {
		port = envPort
	} else {
		port = 0
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to listen: %v\n", err)
		os.Exit(1)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(os.Stderr, "Chat server listening on http://localhost:%d/\n", actualPort)
	fmt.Printf("UI available at http://localhost:%d/ui\n", actualPort)

	httpServer := &http.Server{
		Handler: mux,
	}

	if err := httpServer.Serve(listener); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
