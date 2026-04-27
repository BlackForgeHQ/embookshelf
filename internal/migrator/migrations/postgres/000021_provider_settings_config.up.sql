-- Per-provider configuration + priority.
--
-- `config` is a free-form JSONB bag of provider-specific knobs (API
-- keys, regions, languages, cookies, …). The shape is defined per
-- provider in Go; unknown fields are ignored on decode so forward
-- migrations don't break rollbacks.
--
-- `priority` orders providers in chain-walking flows (ISBN lookup, and
-- eventually bookdrop auto-enrich). Lower = earlier. NULL means "fall
-- back to catalog order". Stored separately from enabled so disabling
-- and priority are independent axes.
ALTER TABLE provider_settings
    ADD COLUMN IF NOT EXISTS config   JSONB   NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS priority INTEGER;
