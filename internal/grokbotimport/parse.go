package grokbotimport

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	layoutRootDirectory       = "agent-transcripts"
	hiddenPromptMarkerGrokBot = "[GROK_BOT_HIDDEN_PROMPT]"
	hiddenPromptMarkerSand    = "[SAND_HIDDEN_PROMPT]"
)

// Role is the normalized message role parsed from a Grok Bot transcript row.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// ToolCall captures one assistant tool call block.
type ToolCall struct {
	ID        string
	Name      string
	InputJSON string
}

// ToolResult captures one tool_result content block.
type ToolResult struct {
	ToolUseID     string
	ContentRaw    string
	ContentLength int
}

// Message is one normalized transcript message.
type Message struct {
	Role        Role
	Content     string
	IsSystem    bool
	HiddenInput bool
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// Result is the parser output for a single Grok Bot transcript file.
type Result struct {
	AgentID          string
	RawSessionID     string
	Messages         []Message
	MalformedLines   int
	FirstUserMessage string
	UserMessageCount int
}

type transcriptRow struct {
	Role    string `json:"role"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	Arguments  json.RawMessage `json:"arguments"`
	ToolUseID  string          `json:"tool_use_id"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Content    json.RawMessage `json:"content"`
	Function   struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// TranscriptRoot returns the Grok Bot transcript root below a configured data
// root (<data-root>/agent-transcripts).
func TranscriptRoot(dataRoot string) string {
	return filepath.Join(dataRoot, layoutRootDirectory)
}

// MatchTranscriptPath checks whether path matches
// <transcript-root>/<agentID>/<agentID>.jsonl and returns the agent ID.
func MatchTranscriptPath(transcriptRoot, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(transcriptRoot), filepath.Clean(path))
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 {
		return "", false
	}
	agentID := strings.TrimSpace(parts[0])
	if agentID == "" || agentID == "." || agentID == ".." {
		return "", false
	}
	if parts[1] != agentID+".jsonl" {
		return "", false
	}
	return agentID, true
}

// SessionIDFromAgentID derives a parser-safe raw session ID from an agent ID.
func SessionIDFromAgentID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	if isValidSessionID(agentID) {
		return agentID
	}
	var b strings.Builder
	lastDash := false
	for _, r := range agentID {
		if isAlphaNum(r) || r == '_' || r == '-' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	sanitized := strings.Trim(b.String(), "-_")
	if sanitized == "" {
		sanitized = "agent"
	}
	digest := sha256.Sum256([]byte(agentID))
	return fmt.Sprintf("%s-%s", sanitized, hex.EncodeToString(digest[:6]))
}

// ParseFile parses one Grok Bot transcript file.
func ParseFile(path string) (Result, error) {
	agentID := filepath.Base(filepath.Dir(path))
	rawSessionID := SessionIDFromAgentID(agentID)
	if rawSessionID == "" {
		return Result{}, fmt.Errorf("derive session id from %q", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	result := Result{
		AgentID:      agentID,
		RawSessionID: rawSessionID,
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		row, malformed, ok := parseRow(line)
		if malformed {
			result.MalformedLines++
		}
		if !ok {
			continue
		}
		msg, ok := normalizeMessage(row)
		if !ok {
			continue
		}
		result.Messages = append(result.Messages, msg)
		if msg.Role == RoleUser && !msg.IsSystem && strings.TrimSpace(msg.Content) != "" {
			result.UserMessageCount++
			if result.FirstUserMessage == "" {
				result.FirstUserMessage = msg.Content
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	return result, nil
}

func parseRow(line string) (transcriptRow, bool, bool) {
	var row transcriptRow
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		return transcriptRow{}, true, false
	}
	role := strings.TrimSpace(row.Role)
	switch role {
	case "user", "assistant", "system":
		return row, false, true
	default:
		return transcriptRow{}, false, false
	}
}

func normalizeMessage(row transcriptRow) (Message, bool) {
	blocks := decodeContentBlocks(row.Message.Content)
	if len(blocks) == 0 {
		return Message{}, false
	}

	var (
		textParts   []string
		toolCalls   []ToolCall
		toolResults []ToolResult
	)
	for _, block := range blocks {
		switch normalizeBlockType(block.Type) {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			if call, ok := normalizeToolCall(block); ok {
				toolCalls = append(toolCalls, call)
			}
		case "tool_result":
			if result, ok := normalizeToolResult(block); ok {
				toolResults = append(toolResults, result)
			}
		}
	}

	content := strings.Join(textParts, "\n")
	msg := Message{
		Content:     content,
		ToolCalls:   toolCalls,
		ToolResults: toolResults,
	}
	if len(msg.ToolCalls) == 0 && len(msg.ToolResults) == 0 && strings.TrimSpace(msg.Content) == "" {
		return Message{}, false
	}

	switch strings.TrimSpace(row.Role) {
	case "assistant":
		msg.Role = RoleAssistant
	case "system":
		msg.Role = RoleSystem
		msg.IsSystem = true
	default:
		msg.Role = RoleUser
	}

	if msg.Role == RoleUser && hasHiddenPromptMarker(msg.Content) {
		msg.Role = RoleSystem
		msg.IsSystem = true
		msg.HiddenInput = true
	}

	return msg, true
}

func decodeContentBlocks(content json.RawMessage) []contentBlock {
	raw := strings.TrimSpace(string(content))
	if raw == "" || raw == "null" {
		return nil
	}
	var blocks []contentBlock
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal(content, &blocks); err == nil {
			return blocks
		}
		return nil
	}
	var single contentBlock
	if err := json.Unmarshal(content, &single); err != nil {
		return nil
	}
	return []contentBlock{single}
}

func normalizeBlockType(raw string) string {
	switch strings.TrimSpace(raw) {
	case "text":
		return "text"
	case "tool_use", "tool-use", "toolUse", "tool_call", "tool-call":
		return "tool_use"
	case "tool_result", "tool-result", "toolResult":
		return "tool_result"
	default:
		return ""
	}
}

func normalizeToolCall(block contentBlock) (ToolCall, bool) {
	name := firstNonEmpty(
		strings.TrimSpace(block.Name),
		strings.TrimSpace(block.ToolName),
		strings.TrimSpace(block.Function.Name),
	)
	if name == "" {
		return ToolCall{}, false
	}
	inputJSON := normalizeJSONValue(
		firstRaw(block.Input, block.Arguments, block.Function.Arguments),
	)
	return ToolCall{
		ID: firstNonEmpty(
			strings.TrimSpace(block.ID),
			strings.TrimSpace(block.ToolUseID),
			strings.TrimSpace(block.ToolCallID),
		),
		Name:      name,
		InputJSON: inputJSON,
	}, true
}

func normalizeToolResult(block contentBlock) (ToolResult, bool) {
	toolUseID := firstNonEmpty(
		strings.TrimSpace(block.ToolUseID),
		strings.TrimSpace(block.ToolCallID),
		strings.TrimSpace(block.ID),
	)
	if toolUseID == "" {
		return ToolResult{}, false
	}
	contentRaw := normalizeJSONContainer(block.Content)
	return ToolResult{
		ToolUseID:     toolUseID,
		ContentRaw:    contentRaw,
		ContentLength: contentLength(contentRaw),
	}, true
}

func normalizeJSONValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var str string
	if err := json.Unmarshal([]byte(trimmed), &str); err == nil {
		return str
	}
	return trimmed
}

func normalizeJSONContainer(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "[]"
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "[]"
	}
	return trimmed
}

func contentLength(contentRaw string) int {
	if strings.TrimSpace(contentRaw) == "" {
		return 0
	}
	var text string
	if err := json.Unmarshal([]byte(contentRaw), &text); err == nil {
		return len(text)
	}

	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(contentRaw), &blocks); err == nil {
		total := 0
		for _, block := range blocks {
			total += len(block.Text)
		}
		return total
	}

	var object struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(contentRaw), &object); err == nil {
		return len(object.Text)
	}

	return 0
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(strings.TrimSpace(string(value))) > 0 &&
			strings.TrimSpace(string(value)) != "null" {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func hasHiddenPromptMarker(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, hiddenPromptMarkerGrokBot) ||
		strings.HasPrefix(trimmed, hiddenPromptMarkerSand)
}

func isValidSessionID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !isAlphaNum(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
