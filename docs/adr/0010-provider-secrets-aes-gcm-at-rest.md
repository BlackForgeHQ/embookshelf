# Provider secrets encrypted per-field with AES-256-GCM at rest

`provider_settings.config` is JSON. Fields the provider declares as `password`-kind in its `ConfigSchema()` (API keys, Hardcover tokens, Amazon cookies) are encrypted in-place by `ProviderSettingsRepo` before the row is written; non-secret fields (region, language, enabled flags) pass through plaintext so the row stays readable in `psql`. The KEK comes from `EMBOOKSHELF_SECRET_KEY` (base64-encoded 32 bytes). Boot semantics are asymmetric: invalid key refuses startup; unset key falls back to `crypto.Noop` with a `slog.Warn`.

## Status

accepted (2026-05-03)

## Decisions bundled here

### 1. Per-field encryption, not whole-blob

`ProviderSettingsRepo.transformConfig(id, cfg, op)` unmarshals the stored JSON, walks the password-key set declared by `p.(SchemaProvider).ConfigSchema()` — resolved through the `SecretKeyFunc` handed in at construction — and applies `op` (Encrypt or Decrypt) only to those keys. Result: a stored row looks like

```json
{
  "enabled": true,
  "domain": "de",
  "cookie": "enc:v1:base64(nonce||ciphertext||tag)"
}
```

The `enc:v1:` prefix is what `crypto` actually writes. This document said `AESGCM:` from the day it was accepted; no code ever produced that. Corrected in favour of the shipping code, since rows in the wild carry the real prefix and `Decrypt` keys its plaintext-passthrough test on it.

instead of one opaque ciphertext blob. Operators can `select config from provider_settings where id='amazon'` and read the region without a key. Schema migrations and admin debugging don't require a decryption step.

### 2. KEK from `EMBOOKSHELF_SECRET_KEY` env var

Single env var, base64-encoded 32 bytes. `crypto.NewAESGCM` validates length on parse. No file path indirection, no KMS, no key rotation tooling. Fits the self-hosted single-binary deployment shape.

### 3. Asymmetric boot semantics

| `EMBOOKSHELF_SECRET_KEY` | Behavior |
|---|---|
| Unset / empty | `crypto.Noop` cipher; secrets stored plaintext; `slog.Warn` at boot. Dev convenience. |
| Set, valid (base64 32 bytes) | `crypto.AESGCM` cipher. |
| Set, invalid | `slog.Error` + refuse to boot. |

Refuse-on-invalid is deliberate: silently falling back to Noop when an admin meant to encrypt would let "secrets are protected" become a false belief. Allow-on-unset is also deliberate: dev-mode operators run `make up` daily and a hard requirement breaks the first-five-minutes experience. The `Warn` line in production logs is the single signal an operator must respect.

### 4. Providers always see plaintext, and the DB boundary is where encryption lives

`SetProviderConfig(ctx, id, cfg)` hands the **plaintext** blob to both `ProviderSettingsRepo.SetConfig` — which encrypts on the way to the row — and `provider.Configure(cfg)`. `LoadConfigs` (boot path) reads through `AllConfigs`, which decrypts. So provider adapters never carry a `Cipher` and never decrypt. A future provider author writing an adapter sees plain `string` API keys and that's correct.

"DB-boundary concern" is meant literally: the Cipher and the slot-discovery function sit on the repo, not on a service above it. Encryption is a property of the row, so putting it anywhere else makes it a property of one call path instead — which is what it was until #166. `ProviderSettingsRepo.SetConfig` used to store whatever blob it was handed, with the transform living in `ProviderSettingsService`; a second writer, or a second accessor on the repo, would have stored a secret in plaintext and nothing would have caught it. This is the same failure `AppSettingsRepo` was built to prevent, and both tables now hold the obligation at the same seam, sharing `crypto.TransformSlots` and differing only in how their slots are discovered — struct-field pointers for a `Setting[T]`, a runtime `ConfigSchema()` walk for a metadata provider.

One consequence worth stating: a read that cannot decrypt now returns an error rather than the raw blob. The old fallback kept the admin panel rendering, but it put ciphertext into a password input, and the next Save re-encrypted it — destroying the last recoverable copy of the secret.

## Considered options

### Rejected: whole-blob encryption

Simpler crypto seam (one Encrypt/Decrypt per row read/write), but every settings read decrypts even for boolean toggles, and DB rows become opaque to anyone debugging without the key. The win on simplicity isn't worth the loss in operability.

### Rejected: cloud KMS / envelope encryption

AWS KMS, GCP KMS, etc. would let us avoid storing the KEK on disk at all. Out of scope for self-hosted single-binary; adds a network dependency on the provider boot path. Reconsider if a managed-cloud deployment shape ever lands.

### Future: file-mounted KEK

`EMBOOKSHELF_SECRET_KEY_FILE` reading from `/run/secrets/embookshelf-key` would integrate cleanly with Docker/K8s secret mounts. Not built today; trivial follow-up when an operator asks.

## Companion artifacts

- `internal/crypto/cipher.go` — `Cipher` interface, `AESGCM`, `Noop`, `ErrBadKey`, the `enc:v1:` prefix.
- `internal/crypto/secrets.go` — `TransformSlots`, shared by both secret-bearing tables.
- `internal/repo/provider_settings.go` — `SecretKeyFunc`, `transformConfig`, and the encrypt/decrypt seam on `SetConfig` / `List` / `AllConfigs`.
- `internal/repo/setting.go` — the `Setting[T]` half of the same mechanism (`Secrets`).
- `internal/provider/provider.go` — `ConfigField{Kind: ConfigFieldPassword}`, `SecretConfigKeys`, `SecretKeyLookup`.
- `internal/service/provider_settings.go` — `LoadConfigs`, `SetProviderConfig`; owns no crypto.
- `cmd/embookshelf/main.go` — boot-time KEK parse, refuse-on-invalid, warn-on-unset, and the `SecretKeyLookup` wiring.
