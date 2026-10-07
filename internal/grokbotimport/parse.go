package grokbotimport

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	layoutRootDirectory       = "agent-transcripts"
	hiddenPromptMarkerGrokBot = "[GROK_BOT_HIDDEN_PROMPT]"
	hiddenPromptMarkerSand    = "[SAND_HIDDEN_PROMPT]"
)

var timestampTagRE = regexp.MustCompile(`(?is)<timestamp>\s*(.*?)\s*</timestamp>`)
var utcOffsetRE = regexp.MustCompile(`^UTC([+-])(\d{1,2})(?::?(\d{2}))?$`)

// Role is the normalized message role parsed from a Grok Bot transcript row.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
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
	Role         Role
	Content      string
	IsSystem     bool
	HiddenInput  bool
	Timestamp    time.Time
	HasTimestamp bool
	HasThinking  bool
	ThinkingText string
	ToolCalls    []ToolCall
	ToolResults  []ToolResult
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
	Result     json.RawMessage `json:"result"`
	Thinking   string          `json:"thinking"`
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

// MatchPathUnderDataRoot checks whether path matches either supported layout
// under dataRoot:
//   - <root>/agent-transcripts/<agentID>/<agentID>.jsonl
//   - <root>/<agentID>/<agentID>.jsonl
func MatchPathUnderDataRoot(dataRoot, path string) (string, bool) {
	cleanRoot := filepath.Clean(dataRoot)
	cleanPath := filepath.Clean(path)
	if agentID, ok := MatchTranscriptPath(TranscriptRoot(cleanRoot), cleanPath); ok {
		return agentID, true
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
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
	var carriedTimestamp time.Time
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
		if msg.HasTimestamp {
			carriedTimestamp = msg.Timestamp
		} else if !carriedTimestamp.IsZero() &&
			(msg.Role == RoleAssistant || msg.Role == RoleTool) {
			msg.Timestamp = carriedTimestamp
			msg.HasTimestamp = true
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
	case "user", "assistant", "system", "tool":
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
		textParts     []string
		thinkingParts []string
		toolCalls     []ToolCall
		toolResults   []ToolResult
	)
	for _, block := range blocks {
		switch normalizeBlockType(block.Type) {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				textParts = append(textParts, text)
			}
		case "thinking":
			thinking := strings.TrimSpace(block.Thinking)
			if thinking != "" {
				thinkingParts = append(thinkingParts, thinking)
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
		if len(thinkingParts) > 0 {
			msg.HasThinking = true
			msg.ThinkingText = strings.Join(thinkingParts, "\n\n")
			msg.Content = "[Thinking]\n" + msg.ThinkingText + "\n[/Thinking]\n" + msg.Content
		}
	case "system":
		msg.Role = RoleSystem
		msg.IsSystem = true
	case "tool":
		msg.Role = RoleTool
	default:
		msg.Role = RoleUser
	}

	if msg.Role == RoleUser {
		if cleaned, timestamp, ok := extractTaggedTimestamp(msg.Content); ok {
			msg.Content = cleaned
			msg.Timestamp = timestamp
			msg.HasTimestamp = true
		}
	}

	if msg.Role == RoleUser && hasHiddenPromptMarker(msg.Content) {
		msg.Role = RoleSystem
		msg.IsSystem = true
		msg.HiddenInput = true
	}
	msg.Content = strings.TrimSpace(msg.Content)
	if msg.Role == RoleUser && strings.TrimSpace(msg.Content) == "" &&
		len(msg.ToolCalls) == 0 && len(msg.ToolResults) == 0 {
		return Message{}, false
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
	case "thinking":
		return "thinking"
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
	if contentRaw == "[]" {
		contentRaw = normalizeJSONContainer(block.Result)
	}
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

func extractTaggedTimestamp(content string) (string, time.Time, bool) {
	matches := timestampTagRE.FindStringSubmatchIndex(content)
	if matches == nil {
		return content, time.Time{}, false
	}
	rawTimestamp := strings.TrimSpace(content[matches[2]:matches[3]])
	parsed, ok := parseEmbeddedTimestamp(rawTimestamp)
	if !ok {
		return content, time.Time{}, false
	}
	cleaned := strings.TrimSpace(content[:matches[0]] + content[matches[1]:])
	return cleaned, parsed, true
}

func parseEmbeddedTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, ok := parseWithUTCOffset(raw); ok {
		return t, true
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"Monday, Jan 2, 2006, 3:04 PM",
		"Mon, Jan 2, 2006, 3:04 PM",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseWithUTCOffset(raw string) (time.Time, bool) {
	open := strings.LastIndex(raw, "(")
	close := strings.LastIndex(raw, ")")
	if open < 0 || close <= open {
		return time.Time{}, false
	}
	datePart := strings.TrimSpace(raw[:open])
	zonePart := strings.TrimSpace(raw[open+1 : close])
	offsetSeconds, ok := parseUTCOffset(zonePart)
	if !ok {
		return time.Time{}, false
	}
	location := time.FixedZone(zonePart, offsetSeconds)
	layouts := []string{
		"Monday, Jan 2, 2006, 3:04 PM",
		"Mon, Jan 2, 2006, 3:04 PM",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, datePart, location); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseUTCOffset(zone string) (int, bool) {
	if zone == "UTC" {
		return 0, true
	}
	matches := utcOffsetRE.FindStringSubmatch(zone)
	if matches == nil {
		return 0, false
	}
	sign := 1
	if matches[1] == "-" {
		sign = -1
	}
	hours := parseDigits(matches[2])
	minutes := 0
	if matches[3] != "" {
		minutes = parseDigits(matches[3])
	}
	if hours > 23 || minutes > 59 {
		return 0, false
	}
	return sign * ((hours * 60 * 60) + (minutes * 60)), true
}

func parseDigits(value string) int {
	total := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		total = total*10 + int(r-'0')
	}
	return total
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
