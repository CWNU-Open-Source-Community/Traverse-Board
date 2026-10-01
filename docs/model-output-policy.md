# Model output policy

Output allowance is selected for the actual provider, transport and mapped wire
model. It is independent of file edit size; no new per-edit limit is imposed.

- Unknown OpenAI Chat, Responses and Ollama omit an unspecified output limit. A local
  context reserve is still used for prompt fitting; it is not a wire cap.
- Anthropic Messages requires a positive `max_tokens`. The adapter uses the
  known model default or the conservative policy for an unknown model.
- Exact official OpenAI Chat/Responses endpoints and `gpt-6-astra`,
  `gpt-6.1-sol`, `gpt-6-luna` use a 1,050,000-token local window and
  128,000-token maximum based on the [official catalog](https://developers.openai.com/api/docs/models),
  checked 2026-10-01. The 16,384-token default is an application choice and
  is sent explicitly, even without a Run budget. It is not an upstream default.
- Exact official DeepSeek Chat/Messages endpoints and documented Flash/Pro IDs
  use the published model metadata. Non-thinking defaults to 8192 tokens;
  thinking defaults to 65536. Existing reasoning options are read, never enabled
  or increased by this policy. This metadata was verified on 2026-09-29 against
  [the official API](https://api-docs.deepseek.com/api/create-chat-completion/) and
  [model specifications](https://api-docs.deepseek.com/quick_start/pricing/).
- A positive call limit is preserved, capped by model capacity and any remaining
  Run token budget. A monetary budget always requires an explicit wire limit
  before reserving cost. Ordinary unknown/zero remote usage remains conservative.
- Auxiliary summaries and specialist/fan-out calls retain their own explicit
  limits. A larger discovered context window does not increase history retention.
- A truncated response still dispatches none of its tools.

## Custom provider configuration

Provider settings now expose a per-model default output, single-request maximum
and local context window. Enable the custom switch to override the inherited
policy. Aliases mapped to the same wire model share the policy. An unknown
provider/model is labeled as a conservative local fallback, not an advertised
capacity. Saving a changed definition invalidates its prior Harness qualification;
requests prepared before the Registry change are still rejected before dispatch.

The model list can be fetched with the current draft key or the saved key for
the unchanged endpoint, transport and definition revision. Discovery itself
does not save a draft key, qualify models or merge entries automatically: select
the returned models to add. Manual names and the current default are retained;
more than 128 configured models requires explicit selection. Failed fetches leave
the draft intact. Redirects are rejected and pagination is bounded.

[OpenAI's model list](https://developers.openai.com/api/reference/resources/models/methods/list)
provides IDs, not token capacities. [Claude's model list](https://platform.claude.com/docs/en/api/models/list)
and [Gemini model metadata](https://ai.google.dev/api/models) can include separate
input/output limits. These are displayed when present; they do not automatically
become a combined local context window. No unverified Gemini 4 million-output
claim is embedded in the defaults.

`advanced_config.model_context_windows` is local operator configuration. Keys are
the **wire model after `model_mapping`**, not the local alias. These fields do not
enter the provider request body. The configured default is sent explicitly,
including for protocols where the field is otherwise optional.

```json
{
  "model_mapping": {"my-local-model": "actual-upstream-model"},
  "model_context_windows": {
    "actual-upstream-model": {
      "window_tokens": 131072,
      "default_output_tokens": 8192,
      "max_output_tokens": 32000
    }
  }
}
```

Use the endpoint's documented limits. Values are validated at configuration
time; output must leave room for input and safety margin and cannot exceed the
adapter's 1,000,000-token request ceiling. Unknown models retain the existing
32K local planning window; for required tool requests the conservative fallback
is 4096. This fallback is not a claim about the remote model's capacity.

Preparation captures one Router generation, exact model, context policy and
qualification. Changes before dispatch reject the stale request locally. Such a
failure has a durable `dispatch: not_sent` receipt bound to its original start;
monetary recovery releases that reservation. Missing receipts remain uncertain.
