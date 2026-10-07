# Grok Bot importer

This package parses Grok Bot transcript files in the layout:

`<data-root>/agent-transcripts/<agent-id>/<agent-id>.jsonl`

Each transcript file is treated as one long-running session for that agent ID.

## Enabling in agentsview

`agentsview` exposes this importer as the `grok-bot` agent source.

- Environment variable override: `GROK_BOT_DATA_DIR`
- Config setting: `[agents.grok-bot].dirs = ["/path/to/data-root"]`
- Default root when not configured: `~/agent-data`

The provider watches and parses under `<root>/agent-transcripts`.

## Notes

- User turns beginning with `[GROK_BOT_HIDDEN_PROMPT]` or
  `[SAND_HIDDEN_PROMPT]` are tagged as system messages so they are not counted
  as user-typed prompts.
- Malformed JSONL lines are skipped and counted.
- Empty transcript files produce no session rows.

## Out of scope / extension points

- Per-agent SQLite stores like `agents/<id>/store.db` and
  `conversation-blobs.db` are intentionally not read yet.
- Sibling `<agent-id>.journal-mode` files are currently ignored.
