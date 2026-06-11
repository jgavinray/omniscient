# LLM Scope of Work

What the LLM is — and is not — responsible for in this pipeline, and how to
qualify a model. The pipeline is designed to work with **local 14B-class
models** on any OpenAI-compatible or Anthropic-compatible endpoint.

## Responsibilities

The LLM performs exactly two tasks per transcript:

### 1. Classify

- **Input:** the first 1,000 characters of the transcript plus the configured
  `classify_prompt`, which lists the allowed template keys.
- **Output contract:** exactly one of the configured template keys, as a
  single word.
- **Enforcement:** the pipeline lowercases/trims the answer and checks it
  against the configured templates. An invalid answer gets **one corrective
  retry** quoting the bad answer; if that also fails, the pipeline falls back
  to the first template key. The LLM cannot invent meeting types.

### 2. Extract

- **Input:** the full transcript plus the meeting-type extraction prompt.
- **Output contract:** YAML front-matter between `---` delimiters, followed
  by a markdown body. Parsed by `models.ParseExtractionOutput`; markdown code
  fences around the output are stripped automatically.
- **Enforcement:** a parse failure triggers **one corrective retry** with the
  parse error appended to the prompt. A second failure marks the transcript
  failed (visible in `omniscient status`, retryable with
  `omniscient retry-failed`).

## Out of scope for the LLM

Deduplication, page naming and placement, publishing, retry policy, and the
set of meeting types are all code/config decisions, not model judgment.

## Settings that matter for small models

- **Temperature is pinned to 0** in both providers — deterministic structure
  beats creative prose for extraction, and small models drift badly at
  higher temperatures.
- Transcripts longer than `llm.max_transcript_chars` (default 100,000 chars
  ≈ 25k tokens) log a warning — check your model's context window. An
  hour-long meeting is typically 8–12k tokens and fits a 32k context.
- Extraction prompts must show the expected output skeleton (the defaults
  do). Small models imitate structure far better than they follow abstract
  instructions; keep the skeleton in any custom template.

## Endpoint options

| Setup | Config |
|-------|--------|
| Anthropic API | `provider: anthropic` + `anthropic_api_key` |
| Local, OpenAI-compatible (vLLM, Ollama, LM Studio) | `provider: openai-compatible` + `openai_base_url` |
| Local, Anthropic-compatible (llama.cpp, proxies) | `provider: anthropic` + `anthropic_base_url` (key optional) |

## Qualifying a new model

1. Point the config at the candidate model/endpoint.
2. Set `dry_run: true` and run `omniscient sync` over a folder with ~10
   representative transcripts. Use `omniscient forget <key>` or a fresh
   `sync.database_path` to re-run the same set.
3. Score from the logs:
   - **Classification accuracy:** count `classification_succeeded` events
     whose type matches what you'd assign by hand. Target ≥ 8/10, with zero
     fallback warnings ("classification invalid after retry").
   - **Parse rate:** extractions that succeed without the corrective retry
     ("extraction output malformed" warnings). Target ≥ 9/10 first-try.
   - **Content spot-check:** action items and decisions present and
     attributed to the right people.
4. A model that frequently needs the corrective retry still works, but it
   doubles cost and latency — prefer one that passes first-try.
