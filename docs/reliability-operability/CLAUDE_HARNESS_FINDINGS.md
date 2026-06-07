# Claude Harness Findings

Purpose: preserve the current local-Claude/policy-engine behavior observed while
trying to implement the reliability and operability requirements.

## Required Invocation

Use the local deployment without `--allowedTools`:

```bash
ANTHROPIC_BASE_URL=http://hyper01:8088 \
ANTHROPIC_AUTH_TOKEN=sk-agent-clean-002 \
CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000 \
claude ...
```

Do not point Claude at `../agentic-os`. If context from that repo is needed,
summarize it in the prompt.

## Observed Policy Behavior

Default `claude -p` loads `CLAUDE.md` before the task text. In observed runs,
repo memory text appeared to dominate the extracted user content used for live
classification. Several stored summaries and response excerpts are truncated to
500 characters, so do not infer classifier input solely from a displayed summary.
In this repo the default route often sets:

- `intent=explain`
- `domain=llm_inference`
- `edit_policy=read_only`
- `allowed_tools=[]`
- `tool_mediation.reason=policy_filtered_all_tools`

Result: trajectories are abandoned with `total_tool_calls=0` and
`files_touched=[]`.

`claude --bare -p` avoids `CLAUDE.md` auto-discovery and allowed the first
request to classify as `modify_config` with `edit_policy=scoped_edit`. The first
request offered `Bash`, `Edit`, and `Read` without using `--allowedTools`.

For headless file edits, `--permission-mode acceptEdits` is also needed. This
does not pass an explicit tool allowlist, but it allows the CLI to accept Edit
tool writes without an interactive permission prompt.

## Raw DB Checks

Run these from `../agentic-os`:

```bash
docker compose exec -T postgres psql -U agent -d agentstack -P pager=off -c \
"select created_at, event_type, actor, left(coalesce(summary,''), 1200) as summary,
metadata->'tool_mediation'->>'intent' as mediation_intent,
metadata->'tool_mediation'->>'decision' as mediation_decision,
metadata->'tool_mediation'->>'reason' as mediation_reason,
metadata->'tool_mediation'->'allowed_tools' as allowed_tools,
metadata->'orchestration_policy' as policy
from agent_events
where event_type='user_message'
order by created_at desc
limit 12;"
```

```bash
docker compose exec -T postgres psql -U agent -d agentstack -P pager=off -c \
"select created_at, summary, metadata->'payload' as payload
from agent_events
where event_type='trajectory_result'
order by created_at desc
limit 12;"
```

For outbound tool/context enforcement, inspect `forwarded_request_body`, not
`raw_request_body`. Raw request capture intentionally preserves the unmodified
Claude Code payload that arrived at the proxy.

```bash
docker compose exec -T postgres psql -U agent -d agentstack_capture -P pager=off -c \
"select received_at,
jsonb_array_length(parsed_request_body->'tools') as inbound_tools,
jsonb_array_length(convert_from(forwarded_request_body, 'UTF8')::jsonb->'tools') as forwarded_tools,
length(raw_request_body) as inbound_bytes,
length(forwarded_request_body) as forwarded_bytes
from raw_http_exchanges
where endpoint='messages'
order by received_at desc
limit 10;"
```

Expected failure signature:

- `policy_filtered_all_tools`
- `allowed_tools=[]`
- `final_status=abandoned`
- `total_tool_calls=0`
- `files_touched=[]`

## Prompt Shape That Got Furthest

Use `--bare` and put the edit instruction first:

```text
Edit /archive/omniscient/<target-file>. Use repository file tools only inside
/archive/omniscient. Do not access parent directories or sibling repositories.
Do not change any file except <target-file>.

Task: <one narrow implementation task>. After editing, run the narrow test and
report the result.
```

Keep the first 500 characters free of LLM-provider wording, template braces, and
large repo context. Add only the minimum requirement detail after the first
paragraph.

## Working Edit Invocation

This shape produced actual file edits:

```bash
ANTHROPIC_BASE_URL=http://hyper01:8088 \
ANTHROPIC_AUTH_TOKEN=sk-agent-clean-002 \
CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000 \
claude --bare --permission-mode acceptEdits -p --verbose \
  --output-format stream-json --include-partial-messages \
  --append-system-prompt "For file modifications in this session, use the Edit tool only. The Write tool is unavailable. Edit parameters should be file_path, old_string, and new_string. Do not include replace_all." \
  "<tight task prompt>"
```

Known model/tool issues:

- It may call unavailable `Write`; remind it to use `Edit`.
- It may call unavailable `Run`; remind it to use `Bash` for tests.
- It may send `replace_all` as a string. Omitting `replace_all` is more reliable
  than asking it to send a boolean.

## Current Blocker

The plain no-allowlist route can complete `Read` and `Edit` calls when run with
`--bare`, `--permission-mode acceptEdits`, and tight tool guidance. The raw
trajectory aggregate can still report `total_tool_calls=0`, so verify against
the stream output and the actual git diff, not only the trajectory summary.

As of 2026-06-05 19:22 UTC, the default non-`--bare` Claude Code route still
must not be used for implementation work until it is re-smoked against the
latest deployed classifier/policy build. The classifier and policy layer now
record tool mediation decisions in `agentstack.agent_events.metadata`. To prove
outbound enforcement, compare `raw_request_body` with `forwarded_request_body`
in `agentstack_capture.raw_http_exchanges`; the raw request is expected to keep
the original full Claude Code tool list.

Observed default smoke command:

```bash
ANTHROPIC_BASE_URL=http://hyper01:8088 \
ANTHROPIC_AUTH_TOKEN=sk-agent-clean-002 \
CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000 \
claude -p --no-session-persistence --verbose \
  --output-format stream-json --include-partial-messages \
  --append-system-prompt 'Work only under /archive/omniscient. Do not access parent directories or sibling repositories. Do not read ../agentic-os. For this smoke test, run exactly one harmless command if a shell tool is available: pwd. Then stop.' \
  'Run the smoke test command.'
```

Observed mediation rows:

- first request: `decision=shape`, `mediation_intent=file_read`,
  `allowed_tools=[Glob,Grep,Read]`, `hidden_count=69`
- second request: `decision=shape`, `mediation_intent=unknown`,
  `allowed_tools=[]`, `hidden_count=93`

Observed inbound raw capture rows for the same run:

- first request: `tool_count=72`, `raw_request_bytes=355590`
- second request: `tool_count=93`, `raw_request_bytes=403750`

The latest classifier row for this smoke task was:

- `intent=explain`
- `domain=llm_inference`
- `risk={none}`
- `complexity=l2_moderate`
- `recommended_route=small_local_model`
- `response_contract=validation_required`

Conclusion from this run: policy shaping was recorded, but this evidence alone
does not prove whether outbound API payload shaping was enforced because it
measured the inbound raw request. Do not resume Claude-driven implementation
through the default route until `forwarded_request_body` tool arrays match the
mediated allowed tool set.

Follow-up smoke at 2026-06-05 19:25 UTC showed the same broad inbound raw
payload pattern and surfaced a second model/tool mismatch:

- inbound raw capture rows: one `tool_count=72` request followed by four
  `tool_count=93` requests
- mediation rows: first request allowed `Glob,Grep,Read`; later requests
  allowed `[]`
- model behavior: attempted tool calls named `Shell` with input
  `{"command":"pwd"}`; Claude Code returned `No such tool available: Shell`

This is not safe for implementation until revalidated: the model may still be
receiving a broad forwarded tool catalog, and the small local model may also
invent platform tool names that are not present in the Claude Code tool set.

## Read Probe Result

A `claude --bare -p --verbose --output-format stream-json` prompt asking to read
`internal/config/config_test.go` returned the correct last test name, but the
raw trajectory aggregate still recorded:

- `final_status=abandoned`
- `total_tool_calls=0`
- `files_touched=[]`

The stream output showed the real `Read` tool call and tool result. Conclusion:
successful-looking text output alone is not proof that the agent used tools, and
the current trajectory aggregate is not sufficient proof that it did not. Verify
tool execution through the stream and verify edits through the worktree diff.
