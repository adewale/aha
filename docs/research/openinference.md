# OpenInference

Research notes captured 2026-05. Use as a reference when designing aha's
canonical event/span model.

## What it is

OpenInference is a **semantic-convention specification for AI application
observability, built on OpenTelemetry**, originated and maintained by Arize
AI. The spec README frames it as "a semantic convention specification for AI
application observability, built on OpenTelemetry," and is explicit about the
layering: *"Every OpenInference trace is a valid OTLP trace; the conventions
give attribute names their AI-specific meaning."*

Relationship to **OTel GenAI semconv**: OpenInference is effectively a
parallel, broader, AI-focused superset that predates and runs alongside
OTel's own `gen_ai.*` namespace. Both target standardized LLM-trace
attributes, but OpenInference also covers retrievers, rerankers, agents,
guardrails, and evaluator spans that OTel GenAI semconv does not yet
formalize. It is transport- and file-format agnostic in principle, but in
practice it travels as OTLP.

Where OTel says `gen_ai.usage.input_tokens`, OpenInference says
`llm.token_count.prompt`. The two namespaces coexist; some platforms
(Langfuse, Phoenix) accept both. Don't read OpenInference as the standard —
read it as one of two strong dialects, with broader span coverage than OTel
GenAI today.

Source: [github.com/Arize-ai/openinference](https://github.com/Arize-ai/openinference), `spec/README.md`, `spec/traces.md`.

## Span kinds

`openinference.span.kind` is an attribute (not the OTel span kind) carrying
one of:

| Kind          | Represents                                                                  |
| ------------- | --------------------------------------------------------------------------- |
| `LLM`         | A call to a large language model                                            |
| `EMBEDDING`   | A call to an embedding model                                                |
| `CHAIN`       | A link between application steps (the LangChain-style composite)            |
| `RETRIEVER`   | A data-retrieval span (vector store, search)                                |
| `RERANKER`    | A reranking of retrieved documents                                          |
| `TOOL`        | An external tool/function invocation (calculator, weather API, shell)       |
| `AGENT`       | The encompassing span around an LLM + tool loop                             |
| `GUARDRAIL`   | An input/output safety check                                                |
| `EVALUATOR`   | An evaluation function/process span                                         |
| `PROMPT`      | A prompt-template rendering                                                 |

The reason it's an attribute rather than OTel's native `span_kind` is
explicit in `spec/traces.md`: *"`span_kind` is an OpenTelemetry concept and
thus conflicts with the OpenInference concept of `span_kind`. When OTLP is
used as the transport, the OpenInference `span_kind` is stored in the
`openinference.span.kind` attribute."* That's a workaround, but it means an
OTel-only consumer sees these spans as `INTERNAL` and ignores the AI
classification unless it parses the attribute.

## Universal attributes

These carry on every span regardless of kind:

- `input.value` — raw input of the operation, typically the full JSON
  request body
- `output.value` — raw output, typically the full JSON response body
- `input.mime_type` / `output.mime_type` — `application/json` or `text/plain`
- `metadata` — arbitrary JSON key-value
- `tag.tags` — list of string tags for categorization
- `session.id` — multi-turn session correlator
- `user.id` — end-user identifier

Context propagation is by OTel context, so child spans inherit `session.id`
and `user.id` automatically.

## Per-kind attributes

### `LLM`

The richest span kind. From `spec/llm_spans.md` and `spec/semantic_conventions.md`:

- `llm.model_name`, `llm.system` (e.g. `"openai"`, `"anthropic"`), `llm.provider`
- `llm.input_messages.<i>.message.{role, content, name, tool_calls}` (flattened dot-notation)
- `llm.output_messages.<i>.message.{role, content, tool_calls}`
- `llm.token_count.{prompt, completion, total}`
- `llm.token_count.prompt_details.{cache_read, cache_write}` (Anthropic-style prompt caching)
- `llm.token_count.completion_details.{reasoning, audio}` (added v0.1.30, May 2026)
- `llm.invocation_parameters` (JSON of temperature, max_tokens, top_p, …)
- `llm.tools.<i>.{name, description, json_schema}`
- `llm.function_call`, `llm.finish_reason`
- `llm.cost.{prompt, completion, total}` (USD)
- Prompt-template family: `llm.prompt_template.{template, variables, version}`

Multimodal/reasoning content uses `message_content.type` values
`text | image | audio | reasoning | tool_use` plus `message_content.{id, data}`
(supports Anthropic's encrypted thinking blocks) and
`tool_call.reasoning_signature` for Gemini.

### `TOOL`

- `tool.name`, `tool.description`, `tool.parameters`, `tool.json_schema`, `tool.id`

### `RETRIEVER`

- `retrieval.documents.<i>.document.{id, content, score, metadata}`

### `EMBEDDING`

- `embedding.model_name`, `embedding.invocation_parameters`
- `embedding.embeddings.<i>.embedding.{text, vector}` (raw float arrays)

### `AGENT`

- `agent.name`, plus inherited universal attributes and tags

### `GUARDRAIL` / `EVALUATOR`

No dedicated reserved attribute schema; both piggyback on `input.value` /
`output.value` / `metadata` plus tags to describe rule names, scores, labels,
and explanations.

## Concrete payload example

```json
{
  "name": "ChatCompletion",
  "trace_id": "...",
  "span_id": "...",
  "attributes": {
    "openinference.span.kind": "LLM",
    "llm.system": "openai",
    "llm.provider": "openai",
    "llm.model_name": "gpt-4o-mini",
    "llm.invocation_parameters": "{\"temperature\":0.2,\"max_tokens\":256}",
    "input.value": "{\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
    "input.mime_type": "application/json",
    "llm.input_messages.0.message.role": "user",
    "llm.input_messages.0.message.content": "hi",
    "llm.output_messages.0.message.role": "assistant",
    "llm.output_messages.0.message.content": "Hello!",
    "llm.token_count.prompt": 8,
    "llm.token_count.completion": 2,
    "llm.token_count.total": 10,
    "llm.token_count.prompt_details.cache_read": 0,
    "session.id": "sess_123",
    "user.id": "adewale@gmail.com"
  },
  "events": [],
  "status": {"code": "OK"}
}
```

Note the flattened dot-notation for arrays: messages and tool calls are
emitted as `llm.input_messages.<i>.message.<field>`, not nested objects.
This is so OTel-attribute consumers (which prefer flat key-value maps) can
read the structure without a JSON parser, at the cost of redundancy with
`input.value`.

## Transport

Pure OTLP — gRPC or HTTP. No custom format. The OpenAI instrumentation
README confirms: *"The traces emitted by this instrumentation are fully
OpenTelemetry compatible and can be sent to an OpenTelemetry collector"*
(`python/instrumentation/openinference-instrumentation-openai/README.md`).
Setup uses standard `OTLPSpanExporter` and `TracerProvider` from
`opentelemetry-sdk`. Phoenix happens to expose `/v1/traces` on port 6006.

## SDK ecosystem

Substantial auto-instrumentation coverage:

- **Python**: OpenAI, Anthropic, Claude Agent SDK, MistralAI, Groq, Google
  GenAI, Bedrock, VertexAI, LiteLLM, LangChain, LlamaIndex, DSPy, Haystack,
  CrewAI, Agno, OpenAI Agents, Microsoft Autogen, PydanticAI, BeeAI,
  smolagents, Pipecat, Portkey, Guardrails, instructor, MCP, plus
  span-processor bridges for OpenLIT and OpenLLMetry.
- **JavaScript**: OpenAI, Anthropic, Claude Agent SDK, LangChain.js, Bedrock,
  BeeAI, MCP, Vercel AI SDK, TanStack AI middleware.
- **Java**: LangChain4j, Spring AI, annotation-based tracing.
- **Go**: Anthropic, OpenAI SDKs with env-var-driven masking.

All providers above are auto-instrumented via monkey-patching once
`XxxInstrumentor().instrument()` is called.

## Versioning / stability

Each language's semconv package is independently versioned:

- Python `openinference-semantic-conventions` — **v0.1.30** (2026-05-22)
- Java — **v0.1.13**
- Go — **v0.1.1**

Sub-1.0 indicates active development; no explicit per-attribute
Stable/Experimental annotations in the spec README. Stability is
communicated via release notes. The May 2026 reasoning-conventions release
(#3112) is a recent additive change.

## Adoption

Primary consumer is **Arize Phoenix** (open-source LLM observability) and
**Arize AX** (commercial). Because spans are plain OTLP, **Langfuse, SigNoz,
Jaeger, Honeycomb, Datadog,** and **Grafana Tempo** can ingest the data —
but only Phoenix and Langfuse render the AI-specific attributes
(`llm.input_messages.*`, retrieval docs) as first-class UI elements. Many
third-party LLM trace tools (OpenLLMetry, OpenLIT) maintain conversion
bridges, and OpenInference's instrumentations are the de facto
auto-instrumentation library for the OTel GenAI ecosystem in many Python
projects.

## Storage-cost considerations

The same characteristic that makes OpenInference rich — verbatim
`input.value` / `output.value` plus a flattened message redundancy —
makes individual spans large.

- For a 50K-token RAG context, a single span can exceed **200 KB**.
- `retrieval.documents.<i>.document.content` stores documents verbatim,
  so retriever spans balloon similarly.
- `embedding.embeddings.<i>.embedding.vector` stores raw float arrays
  (often 1536+ dims per doc).

Mitigations live in `spec/configuration.md` via environment variables:
`OPENINFERENCE_HIDE_INPUTS`, `OPENINFERENCE_HIDE_OUTPUTS`,
`OPENINFERENCE_HIDE_INPUT_IMAGES`, `OPENINFERENCE_HIDE_INPUT_TEXT`,
`OPENINFERENCE_HIDE_LLM_INVOCATION_PARAMETERS`,
`OPENINFERENCE_HIDE_EMBEDDINGS_VECTORS`, `OPENINFERENCE_HIDE_EMBEDDINGS_TEXT`.

Hidden fields become `"__REDACTED__"`. Base64 image payloads are capped via
`OPENINFERENCE_BASE64_IMAGE_MAX_LENGTH` (default **32,000 chars**).
Precedence: code TraceConfig > env vars > defaults.

The practical implication: downstream backends (Clickhouse, Postgres, S3
parquet) must budget for multi-MB spans in agent workflows where input/output
values are retained, or operators must aggressively mask before export.

## Key takeaways for aha

1. **OpenInference is the natural target for naming.** If aha adopts a
   schema for tool/LLM/agent spans, the attribute names should match
   OpenInference verbatim. That gets us free interop with Phoenix and
   Langfuse and a clear lineage path to OTel GenAI as it stabilizes.
2. **Don't store everything OpenInference defines.** `input.value` and
   `llm.input_messages.*` are redundant; aha already has `entries.raw_json`
   which can play the role of `input.value`. Skip the flattened
   `llm.input_messages.*` projection unless we need it for FTS.
3. **Cache-read / reasoning tokens are worth adopting now.** Claude Code
   transcripts already carry `cache_creation_input_tokens` and
   `cache_read_input_tokens`; mapping those to
   `llm.token_count.prompt_details.cache_{read,write}` is a one-line change.
4. **Treat OpenInference as a dialect, not a contract.** The spec is at
   v0.1.x. Pin aha's internal model to its own schema and translate at
   the boundary (export OTLP if anyone asks). Don't couple our SQL columns
   to attribute names that may move.
5. **Steal the masking knobs.** The env-var hide pattern
   (`OPENINFERENCE_HIDE_INPUTS=true` → `__REDACTED__`) is good prior art
   for aha's own redaction spec.

## File references

Absolute paths in `Arize-ai/openinference`:
- `/README.md` — project overview, supported integrations
- `/spec/README.md` — spec purpose, design goals
- `/spec/semantic_conventions.md` — full attribute catalog
- `/spec/llm_spans.md` — LLM span structure and flattening rules
- `/spec/traces.md` — OTLP transport notes, span-kind mapping
- `/spec/configuration.md` — env-var masking knobs
- `/python/openinference-semantic-conventions/` — Python constants package
- `/js/packages/openinference-semantic-conventions/` — `@arizeai/openinference-semantic-conventions` npm package
- `/python/instrumentation/openinference-instrumentation-openai/README.md` — confirms pure OTLP transport
