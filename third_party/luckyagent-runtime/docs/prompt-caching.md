# Prompt caching request options

The OpenAI adapter keeps tool requests stable and supports an optional explicit
`prompt_cache_key`. Both Chat Completions and Responses, streaming and ordinary
calls, use the same settings.

## Configuration

The canonical LuckyAgent configuration field is:

```json
{
  "models": {
    "endpoints": {
      "chat": {
        "prompt_cache_key": "luckyagent-project-a"
      }
    }
  }
}
```

```bash
lh config set models.endpoints.chat.prompt_cache_key luckyagent-project-a
lh config set models.endpoints.chat.prompt_cache_key ""
```

The second command clears the value and omits the wire field. The default is
empty, preserving endpoints that reject unknown fields. Only configure a value
for an endpoint that supports this OpenAI request option.

The legacy `llm_provider.prompt_cache_key` is accepted and migrates to the
canonical models field. Each `fallbacks[]` entry has an independent
`prompt_cache_key`; an empty fallback value does not inherit the primary key.
For direct library use, set `provider.Config.LlmProvider.PromptCacheKey` or
`provider.FallbackConfig.PromptCacheKey`. Leading and trailing whitespace is
trimmed before sending.

Keys are explicit namespaces; the runtime does not generate them from a system
prompt, user message or credential. Avoid putting personal data or secrets in a
key. Configuration changes follow the existing provider reload behavior.

## Stable tool definitions

The adapter clones tool schema maps and arrays, sorts tools by function name
(with serialized JSON as a tie breaker), and sorts JSON Schema `required`
names. It only follows schema-bearing keywords. Ordered arrays such as `enum`,
`oneOf`, defaults and examples retain their order, and caller-owned input is
not changed. The tools module also sorts required parameter names at creation.

This reduces avoidable request-prefix changes. It does not make different
prompts equivalent, cache model answers or guarantee a cache hit. Cached-token
usage continues to use the existing usage parsers. No hit-rate, cost or latency
improvement has been measured for this change.

The provider controls caching behavior; consult the [OpenAI prompt caching
guide](https://developers.openai.com/api/docs/guides/prompt-caching) for supported
models, prefix matching and the meaning of the request key.
