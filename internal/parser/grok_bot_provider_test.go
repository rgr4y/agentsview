package parser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/grokbotimport"
)

func TestGrokBotProviderParsesToolBlocks(t *testing.T) {
	root, transcriptPath := writeGrokBotFixture(
		t, "tool_blocks.jsonl", "agent-123", true,
	)
	outcome, result := parseGrokBotSingleResult(t, root)
	require.Equal(t, SkipNone, outcome.SkipReason)

	assert.Equal(t, "grok-bot:agent-123", result.Session.ID)
	assert.Equal(t, AgentGrokBot, result.Session.Agent)
	assert.Equal(t, "agent-123", result.Session.Project)
	assert.Equal(t, "agent-123", result.Session.SourceSessionID)
	assert.Equal(t, grokBotSourceVersion, result.Session.SourceVersion)
	assert.Equal(t, 4, result.Session.MessageCount)
	assert.Equal(t, 1, result.Session.UserMessageCount)
	assert.Equal(t, "Please read the project README.", result.Session.FirstMessage)

	info, err := os.Stat(transcriptPath)
	require.NoError(t, err)
	assert.Equal(t, info.ModTime().UnixNano(), result.Session.File.Mtime)
	assert.Equal(t, info.Size(), result.Session.File.Size)
	assert.False(t, result.Session.StartedAt.IsZero())
	assert.True(t, result.Session.StartedAt.Equal(result.Session.EndedAt))

	require.Len(t, result.Messages, 4)
	assert.Equal(t, RoleAssistant, result.Messages[1].Role)
	require.Len(t, result.Messages[1].ToolCalls, 1)
	assert.Equal(t, "call-read-1", result.Messages[1].ToolCalls[0].ToolUseID)
	assert.Equal(t, "Read", result.Messages[1].ToolCalls[0].ToolName)
	assert.Equal(t, "Read", result.Messages[1].ToolCalls[0].Category)
	assert.Contains(t, result.Messages[1].ToolCalls[0].InputJSON, "README.md")

	assert.Equal(t, RoleUser, result.Messages[2].Role)
	require.Len(t, result.Messages[2].ToolResults, 1)
	assert.Equal(t, "call-read-1", result.Messages[2].ToolResults[0].ToolUseID)
	assert.Contains(t, result.Messages[2].ToolResults[0].ContentRaw, "agentsview")
}

func TestGrokBotProviderMarksHiddenPromptMessagesAsSystem(t *testing.T) {
	root, _ := writeGrokBotFixture(
		t, "hidden_markers.jsonl", "help-bot", true,
	)
	_, result := parseGrokBotSingleResult(t, root)

	assert.Equal(t, 1, result.Session.UserMessageCount)
	assert.Equal(t, "Can you summarize today's changes?", result.Session.FirstMessage)
	require.Len(t, result.Messages, 4)
	assert.Equal(t, RoleSystem, result.Messages[0].Role)
	assert.True(t, result.Messages[0].IsSystem)
	assert.Equal(t, RoleUser, result.Messages[2].Role)
	assert.False(t, result.Messages[2].IsSystem)
}

func TestGrokBotProviderCountsMalformedLines(t *testing.T) {
	root, _ := writeGrokBotFixture(
		t, "malformed_lines.jsonl", "agent-malformed", true,
	)
	_, result := parseGrokBotSingleResult(t, root)

	assert.Equal(t, 1, result.Session.MalformedLines)
	assert.Equal(t, 2, result.Session.MessageCount)
	assert.Equal(t, 1, result.Session.UserMessageCount)
}

func TestGrokBotProviderSkipsEmptyFile(t *testing.T) {
	root, _ := writeGrokBotFixture(t, "empty.jsonl", "agent-empty", true)
	provider, ok := NewProvider(AgentGrokBot, ProviderConfig{
		Roots:   []string{root},
		Machine: "local",
	})
	require.True(t, ok)

	sources, err := provider.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	fingerprint, err := provider.Fingerprint(t.Context(), sources[0])
	require.NoError(t, err)

	outcome, err := provider.Parse(t.Context(), ParseRequest{
		Source:      sources[0],
		Fingerprint: fingerprint,
		Machine:     "local",
	})
	require.NoError(t, err)
	assert.Equal(t, SkipNoSession, outcome.SkipReason)
	assert.Empty(t, outcome.Results)
}

func TestGrokBotProviderSanitizesAgentIDsInSessionID(t *testing.T) {
	agentID := "help.bot"
	root, _ := writeGrokBotFixture(t, "tool_blocks.jsonl", agentID, true)
	_, result := parseGrokBotSingleResult(t, root)

	wantRaw := grokbotimport.SessionIDFromAgentID(agentID)
	assert.Equal(t, "grok-bot:"+wantRaw, result.Session.ID)
	assert.Equal(t, agentID, result.Session.SourceSessionID)
}

func TestGrokBotProviderSupportsDirectLayoutUnderRoot(t *testing.T) {
	root, _ := writeGrokBotFixture(t, "tool_blocks.jsonl", "agent-direct", false)
	_, result := parseGrokBotSingleResult(t, root)
	assert.Equal(t, "grok-bot:agent-direct", result.Session.ID)
	assert.Equal(t, "agent-direct", result.Session.SourceSessionID)
}

func TestGrokBotProviderParsesTimestampThinkingAndToolRole(t *testing.T) {
	root, _ := writeGrokBotFixture(
		t, "timestamp_tool_thinking.jsonl", "agent-time", true,
	)
	_, result := parseGrokBotSingleResult(t, root)

	require.Len(t, result.Messages, 4)
	userMessage := result.Messages[0]
	assert.Equal(t, RoleUser, userMessage.Role)
	assert.Equal(t, "Please run the checks.", userMessage.Content)

	wantTimestamp := time.Date(2026, 9, 23, 4, 26, 0, 0, time.UTC)
	assert.Equal(t, wantTimestamp, userMessage.Timestamp)
	assert.Equal(t, wantTimestamp, result.Messages[1].Timestamp)
	assert.Equal(t, wantTimestamp, result.Messages[2].Timestamp)
	assert.Equal(t, wantTimestamp, result.Messages[3].Timestamp)

	assistantWithThinking := result.Messages[1]
	assert.True(t, assistantWithThinking.HasThinking)
	assert.Contains(t, assistantWithThinking.ThinkingText, "inspect the current checks")
	assert.Contains(t, assistantWithThinking.Content, "[Thinking]")
	require.Len(t, assistantWithThinking.ToolCalls, 1)
	assert.Equal(t, "Shell", assistantWithThinking.ToolCalls[0].ToolName)

	toolMessage := result.Messages[2]
	assert.Equal(t, RoleTool, toolMessage.Role)
	require.Len(t, toolMessage.ToolResults, 1)
	assert.Equal(t, "call-checks-1", toolMessage.ToolResults[0].ToolUseID)
	assert.Equal(t, "ok", DecodeContent(toolMessage.ToolResults[0].ContentRaw))

	assert.Equal(t, wantTimestamp, result.Session.StartedAt)
	assert.Equal(t, wantTimestamp, result.Session.EndedAt)
}

func writeGrokBotFixture(
	t *testing.T, fixtureName, agentID string, underAgentTranscripts bool,
) (root string, transcriptPath string) {
	t.Helper()
	root = t.TempDir()
	transcriptDir := filepath.Join(root, agentID)
	if underAgentTranscripts {
		transcriptDir = filepath.Join(root, "agent-transcripts", agentID)
	}
	require.NoError(t, os.MkdirAll(transcriptDir, 0o755))

	content, err := os.ReadFile(filepath.Join("testdata", "grokbot", fixtureName))
	require.NoError(t, err)

	transcriptPath = filepath.Join(transcriptDir, agentID+".jsonl")
	require.NoError(t, os.WriteFile(transcriptPath, content, 0o644))
	return root, transcriptPath
}

func parseGrokBotSingleResult(
	t *testing.T, root string,
) (ParseOutcome, ParseResult) {
	t.Helper()
	provider, ok := NewProvider(AgentGrokBot, ProviderConfig{
		Roots:   []string{root},
		Machine: "local",
	})
	require.True(t, ok)

	sources, err := provider.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	fingerprint, err := provider.Fingerprint(t.Context(), sources[0])
	require.NoError(t, err)
	require.NotEmpty(t, fingerprint.Hash)

	outcome, err := provider.Parse(t.Context(), ParseRequest{
		Source:      sources[0],
		Fingerprint: fingerprint,
		Machine:     "local",
	})
	require.NoError(t, err)
	require.Len(t, outcome.Results, 1)
	return outcome, outcome.Results[0].Result
}
