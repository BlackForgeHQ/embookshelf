# Text-to-speech engines ship as a catalog, reversing the single-adapter stance

Audiobook generation calls out to a speech engine. embookshelf has said "no catalog" twice before — ADR-0020 for email, ADR-0024 for the LLM — and said "yes catalog" once, ADR-0008 for metadata providers. This ADR puts text-to-speech on the catalog side and states why the earlier refusals do not apply.

## Status

accepted (2026-07-27)

## Decisions bundled here

### 1. Why a catalog here, when SMTP and the LLM did not get one

ADR-0020 refused an email provider catalog because one SMTP transport genuinely covers Brevo, Mailjet, SES, Postmark, Mailgun, Gmail and Postfix: the vendors differ in billing, not in capability, so a second adapter would have been a copy of the first. ADR-0024 refused an LLM catalog for the same reason with one extra pull — an OpenAI-compatible base URL reaches OpenAI, OpenRouter, Ollama, LM Studio and vLLM, so a single adapter also delivered the local-model story self-hosted users need.

Speech engines are not like that. They diverge on things a caller has to know about:

| | Per-request cap | SSML | Word timings | ~$/550k chars |
|---|---|---|---|---|
| OpenAI-compatible (incl. local Kokoro, Piper) | 4096 | no | no | $0–8 |
| ElevenLabs | 5000 | partial | alignment API | $100–170 |
| Azure Speech | ~10 min audio | yes | word boundary | $8–16 |

A single OpenAI-shaped `text in, audio out` interface can be made to work, but it amputates exactly what the expensive engines are bought for, and per-request caps varying by 25% mean chunking is engine-specific whether or not the interface admits it. This is the `one-adapter-is-hypothetical-two-is-real` test coming back the other way: here the second and third adapters are real on day one.

The vocabulary consequence is that **TTS engine** joins OIDC provider, metadata provider and Forward-auth as a thing that must never be called just "provider".

### 2. Three engines at launch, one per axis of divergence

OpenAI-compatible, ElevenLabs, Azure Speech. Chosen so each represents a distinct capability axis rather than a distinct vendor: no-SSML-no-timings with a free local escape hatch, alignment-with-premium-quality, and full-SSML-with-timings at commodity price. Per-request caps span 3000 to effectively unbounded, so the chunker is stressed properly by the initial set rather than by whatever gets added later.

Polly and Google were considered and deferred. Polly's speech marks are the best timing data of the lot and Google Studio voices are the closest non-ElevenLabs quality, but capability-wise both are near-duplicates of Azure — the fourth and fifth adapters would teach the interface nothing new. They become entries four and five in the catalog literal, which is the extension ADR-0008 was designed for.

### 3. No vendor SDKs

All three speak plain REST with a header key, so `internal/provider`'s hand-rolled-HTTP precedent holds and `go.mod` does not grow a vendor tree. This is also why Polly lost to Azure in §2 beyond capability: AWS means either `aws-sdk-go-v2` or hand-rolled SigV4, and ADR-0024 already rejected vendor SDKs in a single-binary application.

### 4. Selection, not fan-out

Metadata providers fan out to every enabled provider and merge by confidence (ADR-0013), ranked by `provider_settings.priority`. Narration must pick exactly one engine: fanning out would generate and bill the same book three times. So this catalog has a *selected* engine, not a ranked list, and ADR-0011's priority-chain semantics do not carry over. Nothing here degrades gracefully in the ADR-0013 sense either — a failed engine is a failed generation, not a smaller result set.

The adapter interface therefore has two methods, not one: synthesis, and `ListVoices` so the generate dialog can populate. Voice lists are fetched when the dialog opens and not cached; OpenAI's are static, ElevenLabs and Azure each have a list endpoint.

### 5. Config is one typed settings row, not a `provider_settings` table

The catalog itself — which engines exist, their endpoints, their per-request character caps, a default price per million characters — is a Go literal, hard-coded and rebuilt to change, per ADR-0008. The configuration is a single typed `app_settings.AUDIOBOOK` row via `repo.Setting[T]`, with a sub-struct per engine and a `Secrets` accessor returning the three API keys.

`provider_settings` was rejected. Its runtime `ConfigSchema()` machinery exists because metadata providers have genuinely divergent config — a Hardcover token, an Amazon cookie, a region, a language — discovered at runtime and encrypted by walking the schema. TTS config is uniform across all three engines (`enabled`, `apiKey`, `baseURL`, `model`, `defaultVoice`, `pricePerMillionChars`) and the set is closed at compile time, so runtime schema discovery buys nothing.

It also costs something. Per CONTEXT, `AppSettingsRepo` holds the Cipher rather than taking it per call, which "is what stops a new accessor silently storing a secret in plaintext (the gap that left OIDC client secrets unencrypted until the mechanism was unified)". Declaring `Secrets` is the whole opt-in to at-rest encryption under ADR-0010. Using the newer, safer mechanism for three new API keys is the obvious call.

Reusing the existing `provider_settings` table with a kind discriminator was rejected outright: the enrichment fan-out queries that table, and conflating two catalogs in it invites a metadata search to enumerate speech engines.

### 6. Voice is chosen per book, with an instance default

The settings row holds the selected engine and a default voice; the generate dialog is prefilled from it and both are editable per book. `book_audiobooks.engine` and `.voice` record what was actually used.

Instance-wide-only was rejected because voice choice is most of the product. A different narrator for a different novel is the reason to pay ElevenLabs at all, and since generation is already a deliberate admin act (ADR-0028), a dialog costs nothing.

## Considered options

### Rejected: one OpenAI-compatible adapter, mirroring ADR-0024

Cheapest to build, one seam, local engines free. Rejected per §1 — the interface would fix `text in, audio out` and neither timing data nor SSML would fit later without breaking it.

### Rejected: two engines instead of three

A cheap floor plus a quality ceiling, or a cheap floor plus a capable middle. Two engines that barely differ prove nothing and the abstraction gets designed blind; two engines at opposite extremes leave SSML and timings unrepresented, so the first attempt to use them is a redesign.

### Rejected: a local subprocess only (Piper)

No key, no cost, no network, nothing leaves the instance — the strongest privacy story available, and a real fit for self-hosted software. Rejected because it ships a binary plus voice models, which breaks the single-binary property, and CPU-bound synthesis on the instance makes an eight-hour book an overnight job. Local engines remain reachable through the OpenAI-compatible adapter (Kokoro-FastAPI, openedai-speech, LocalAI), which gets the privacy story without the packaging cost.

### Rejected: Polly or Google in the launch set

Covered in §2 and §3.

## Open questions

- Whether ElevenLabs' alignment API and Azure's word-boundary events can be expressed in the common interface at all, or whether timing data stays an engine-specific extra the alignment map ignores.
- Azure word boundaries arrive over its streaming path, not plain REST — whether that is worth a second transport inside one adapter.
- Whether a per-engine health surface, like `provider_settings.last_success_at`, is worth having when only one engine is selected at a time.

## Companion artifacts

- `CONTEXT.md` — TTS engine, TTS catalog, Audiobook settings row.
- ADR-0008 — the catalog-in-the-binary pattern this follows.
- ADR-0010 — the encrypted-secret storage the three API keys reuse.
- ADR-0013 — the fan-out policy this deliberately does not adopt.
- ADR-0020, ADR-0024 — the two prior refusals of a catalog, and why they do not bind here.
- ADR-0025 — what the generated bytes become.
