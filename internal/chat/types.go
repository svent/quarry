package chat

// ChatHistoryItem represents a single message in conversation history.
type ChatHistoryItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AnswerOptions configures the behavior of AnswerQuestion and StreamAnswer.
type AnswerOptions struct {
	Debug   bool
	History []ChatHistoryItem
}

// StreamEvent represents an event emitted during streaming answer generation.
type StreamEvent struct {
	Type string // "thinking" | "delta" | "tool_start" | "tool_end" | "done"
	Text string // only for "delta"
	Name string // only for "tool_start" / "tool_end"
}
