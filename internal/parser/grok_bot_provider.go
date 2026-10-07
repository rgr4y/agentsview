package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.kenn.io/agentsview/internal/grokbotimport"
)

const grokBotSourceVersion = "grok-bot-jsonl-v1"

func newGrokBotProviderFactory(def AgentDef) ProviderFactory {
	return NewSourceSetFactory(
		def,
		grokBotProviderCapabilities(),
		func(cfg ProviderConfig) SourceSet {
			return newGrokBotSourceSet(cfg.Roots)
		},
	)
}

func newGrokBotSourceSet(roots []string) JSONLSourceSet {
	transcriptRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if filepath.Base(cleanRoot) == "agent-transcripts" {
			transcriptRoots = append(transcriptRoots, cleanRoot)
			continue
		}
		transcriptRoots = append(transcriptRoots, grokbotimport.TranscriptRoot(cleanRoot))
	}
	return NewJSONLSourceSet(AgentGrokBot, transcriptRoots,
		WithRecursive(),
		WithContentHashing(),
		WithIncludePath(isGrokBotSourcePath),
		WithProjectHint(grokBotProjectHintFromPath),
		WithSessionIDFromPath(grokBotSessionIDFromPath),
		WithLookupIDValid(func(rawID string) bool {
			return strings.TrimSpace(rawID) != ""
		}),
		WithParseFile(grokBotParseFile),
	)
}

func isGrokBotSourcePath(root, path string) bool {
	_, ok := grokbotimport.MatchTranscriptPath(root, path)
	return ok
}

func grokBotProjectHintFromPath(root, path string) string {
	agentID, ok := grokbotimport.MatchTranscriptPath(root, path)
	if !ok {
		return ""
	}
	return agentID
}

func grokBotSessionIDFromPath(root, path string) string {
	agentID, ok := grokbotimport.MatchTranscriptPath(root, path)
	if !ok {
		return ""
	}
	return grokbotimport.SessionIDFromAgentID(agentID)
}

func grokBotParseFile(
	ctx context.Context, path string, req ParseRequest,
) ([]ParseResult, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}

	parsed, err := grokbotimport.ParseFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(parsed.Messages) == 0 {
		return nil, nil, nil
	}

	msgs := make([]ParsedMessage, 0, len(parsed.Messages))
	fileTime := info.ModTime()
	for ordinal, message := range parsed.Messages {
		pm := ParsedMessage{
			Ordinal:       ordinal,
			Role:          mapGrokBotRole(message.Role),
			IsSystem:      message.IsSystem,
			Content:       message.Content,
			ContentLength: len(message.Content),
			Timestamp:     fileTime,
		}
		for _, call := range message.ToolCalls {
			input := call.InputJSON
			tc := ParsedToolCall{
				ToolUseID: call.ID,
				ToolName:  call.Name,
				Category:  NormalizeToolCategory(call.Name),
				InputJSON: input,
				SkillName: inferToolSkillName(ctx, call.Name, input),
			}
			pm.ToolCalls = append(pm.ToolCalls, tc)
		}
		pm.HasToolUse = len(pm.ToolCalls) > 0
		for _, result := range message.ToolResults {
			pm.ToolResults = append(pm.ToolResults, ParsedToolResult{
				ToolUseID:     result.ToolUseID,
				ContentRaw:    result.ContentRaw,
				ContentLength: result.ContentLength,
			})
		}
		msgs = append(msgs, pm)
	}

	rawSessionID := parsed.RawSessionID
	sessionID := string(AgentGrokBot) + ":" + rawSessionID
	sessionTime := fallbackSessionTime(fileTime, req.Fingerprint.MTimeNS)
	result := ParseResult{
		Session: ParsedSession{
			ID:               sessionID,
			Project:          firstNonEmptyJSONLString(parsed.AgentID, req.Source.ProjectHint),
			Machine:          req.Machine,
			Agent:            AgentGrokBot,
			SourceSessionID:  parsed.AgentID,
			SourceVersion:    grokBotSourceVersion,
			MalformedLines:   parsed.MalformedLines,
			FirstMessage:     truncate(strings.ReplaceAll(parsed.FirstUserMessage, "\n", " "), 300),
			StartedAt:        sessionTime,
			EndedAt:          sessionTime,
			MessageCount:     len(msgs),
			UserMessageCount: parsed.UserMessageCount,
			File: FileInfo{
				Path:  path,
				Size:  info.Size(),
				Mtime: info.ModTime().UnixNano(),
			},
		},
		Messages: msgs,
	}
	if req.Fingerprint.Hash != "" {
		result.Session.File.Hash = req.Fingerprint.Hash
	}
	return []ParseResult{result}, nil, nil
}

func mapGrokBotRole(role grokbotimport.Role) RoleType {
	switch role {
	case grokbotimport.RoleAssistant:
		return RoleAssistant
	case grokbotimport.RoleSystem:
		return RoleSystem
	default:
		return RoleUser
	}
}

func fallbackSessionTime(fileTime time.Time, fingerprintMtime int64) time.Time {
	if !fileTime.IsZero() {
		return fileTime
	}
	if fingerprintMtime <= 0 {
		return time.Time{}
	}
	return time.Unix(0, fingerprintMtime)
}

func grokBotProviderCapabilities() Capabilities {
	return Capabilities{
		Source: jsonlFileProviderSourceCapabilities(),
		Content: ContentCapabilities{
			FirstMessage:       CapabilitySupported,
			ToolCalls:          CapabilitySupported,
			ToolResults:        CapabilitySupported,
			MalformedLineCount: CapabilitySupported,
		},
	}
}
