-- provider_settings stores the per-provider enabled flag so admins can
-- toggle enrichment sources at runtime without restarting. On first boot
-- the service seeds this table from the provider catalog defaults; after
-- that, the DB is authoritative.
CREATE TABLE IF NOT EXISTS provider_settings (
    id         text PRIMARY KEY,
    enabled    boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
