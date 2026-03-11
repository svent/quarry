package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
	flag "github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/svent/quarry/internal/chat"
)

var spinnerFrames = []string{"|", "/", "-", "\\"}

func chatStyle() ansi.StyleConfig {
	style := styles.NoTTYStyleConfig
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if termenv.HasDarkBackground() {
			style = styles.DarkStyleConfig
		} else {
			style = styles.LightStyleConfig
		}
	}
	if style.CodeBlock.Chroma != nil {
		style.CodeBlock.Chroma.Error = ansi.StylePrimitive{}
	}
	return style
}

func main() {
	isDebug := flag.Bool("debug", false, "Enable debug output")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: chat [--debug] <question>\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	question := strings.TrimSpace(strings.Join(flag.Args(), " "))

	if question == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Build a custom style based on the auto-detected dark/light theme,
	// but override the Chroma Error token to remove its red background.
	// The default glamour styles render syntax-highlighter "Error" tokens
	// with a bright red background, which fires on partial code snippets
	// the LLM commonly produces (e.g. trailing quotes, colons).
	style := chatStyle()
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create markdown renderer: %v\n", err)
		os.Exit(1)
	}

	renderMarkdown := func(text string) string {
		rendered, err := renderer.Render(text)
		if err != nil {
			return text
		}
		return strings.TrimRight(rendered, "\n")
	}

	// Spinner state.
	var spinnerMu sync.Mutex
	var spinnerTicker *time.Ticker
	var spinnerDone chan struct{}
	spinnerVisible := false

	clearSpinner := func() {
		spinnerMu.Lock()
		defer spinnerMu.Unlock()

		if spinnerDone != nil {
			close(spinnerDone)
			spinnerDone = nil
		}
		if spinnerTicker != nil {
			spinnerTicker.Stop()
			spinnerTicker = nil
		}
		if spinnerVisible {
			fmt.Fprint(os.Stderr, "\r\x1b[K")
			spinnerVisible = false
		}
	}

	startSpinner := func(label string) {
		clearSpinner()

		spinnerMu.Lock()
		defer spinnerMu.Unlock()

		done := make(chan struct{})
		spinnerDone = done
		frame := 0

		spinnerVisible = true
		fmt.Fprintf(os.Stderr, "  %s %s...", spinnerFrames[0], label)
		ticker := time.NewTicker(100 * time.Millisecond)
		spinnerTicker = ticker

		go func() {
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					spinnerMu.Lock()
					frame = (frame + 1) % len(spinnerFrames)
					fmt.Fprintf(os.Stderr, "\r  %s %s...", spinnerFrames[frame], label)
					spinnerMu.Unlock()
				}
			}
		}()
	}

	defer clearSpinner()

	ch, err := chat.StreamAnswer(context.Background(), question, chat.AnswerOptions{
		Debug: *isDebug,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to answer question: %v\n", err)
		os.Exit(1)
	}

	// Maximum number of lines we will rewind with cursor-up. Lines older
	// than this are "committed" to scrollback and will not be rewritten,
	// keeping the terminal scrollback buffer clean.
	const maxRewind = 10

	accumulated := ""
	var flushedLines []string

	flushRendered := func(rendered string, isFinal bool) {
		lines := strings.Split(rendered, "\n")

		// Determine how many complete lines we can show.
		// On final flush, all lines are complete; during streaming,
		// withhold the last line as it may still be incomplete.
		completeCount := len(lines)
		if !isFinal {
			completeCount = len(lines) - 1
		}
		if completeCount < 0 {
			completeCount = 0
		}

		newComplete := lines[:completeCount]

		// Find the first line that diverges from what we already flushed.
		divergeAt := 0
		for divergeAt < len(flushedLines) && divergeAt < len(newComplete) {
			if flushedLines[divergeAt] != newComplete[divergeAt] {
				break
			}
			divergeAt++
		}

		// Cap the rewind so we never overwrite lines that have scrolled
		// into the terminal's scrollback buffer.
		rewindCount := len(flushedLines) - divergeAt
		if rewindCount > maxRewind {
			divergeAt = len(flushedLines) - maxRewind
			rewindCount = maxRewind
		}

		// If previously flushed lines changed, rewrite from the divergence point.
		if rewindCount > 0 {
			// Move cursor up and clear from there to end of screen.
			fmt.Fprintf(os.Stdout, "\x1b[%dA\x1b[J", rewindCount)
		}

		// Print lines from the divergence point onward.
		if divergeAt < len(newComplete) {
			output := strings.Join(newComplete[divergeAt:], "\n") + "\n"
			fmt.Print(output)
		}

		flushedLines = newComplete
	}

	// Throttle re-renders so we do not rewrite the screen on every token.
	// Deltas accumulate and are flushed at most once per tick.
	const flushInterval = 50 * time.Millisecond
	flushTicker := time.NewTicker(flushInterval)
	defer flushTicker.Stop()
	dirty := false

	flushDirty := func() {
		if !dirty {
			return
		}
		dirty = false
		rendered := renderMarkdown(accumulated)
		flushRendered(rendered, false)
	}

	running := true
	for running {
		select {
		case event, ok := <-ch:
			if !ok {
				running = false
				break
			}

			switch event.Type {
			case "thinking":
				flushDirty()
				startSpinner("Thinking")

			case "tool_start":
				flushDirty()
				label := "Researching"
				switch event.Name {
				case "list_keywords":
					label = "Listing keywords"
				case "lookup_keywords":
					label = "Searching index"
				case "fetch_content":
					label = "Fetching content"
				case "list_files":
					label = "Listing files"
				case "read_file":
					label = "Reading file"
				case "grep":
					label = "Searching files"
				}
				startSpinner(label)

			case "tool_end":
				// Keep the last tool label visible on the spinner.
				// It will be replaced by the next tool_start, delta, or done event.

			case "delta":
				clearSpinner()
				accumulated += event.Text
				dirty = true

			case "done":
				flushDirty()
				clearSpinner()
			}

		case <-flushTicker.C:
			flushDirty()
		}
	}

	// Final flush: render the complete markdown and output any remaining lines.
	if accumulated != "" {
		rendered := renderMarkdown(accumulated)
		flushRendered(rendered, true)
	}
}
