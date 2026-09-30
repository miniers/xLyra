-- +goose Up
ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS gateway_config JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE canonical_models
  ADD COLUMN IF NOT EXISTS routing_preference TEXT NOT NULL DEFAULT 'default',
  ADD COLUMN IF NOT EXISTS routing_expiry_rescue_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE health_snapshots
  ADD COLUMN IF NOT EXISTS first_byte_latency_ms INTEGER;

CREATE TABLE IF NOT EXISTS route_locks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  canonical_model_id UUID NOT NULL REFERENCES canonical_models(id) ON DELETE CASCADE,
  site_model_id UUID NOT NULL REFERENCES site_models(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ,
  policy TEXT NOT NULL DEFAULT 'strict' CHECK (policy IN ('strict', 'fallback')),
  reason TEXT NOT NULL DEFAULT '',
  created_by_admin_id UUID REFERENCES admins(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(canonical_model_id)
);

CREATE INDEX IF NOT EXISTS route_locks_canonical_model_idx ON route_locks (canonical_model_id);
CREATE INDEX IF NOT EXISTS route_locks_site_model_idx ON route_locks (site_model_id);
CREATE INDEX IF NOT EXISTS route_locks_expires_at_idx ON route_locks (expires_at DESC);

CREATE TABLE IF NOT EXISTS route_anomaly_states (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  canonical_model_id UUID NOT NULL REFERENCES canonical_models(id) ON DELETE CASCADE,
  site_model_id UUID NOT NULL REFERENCES site_models(id) ON DELETE CASCADE,
  active BOOLEAN NOT NULL DEFAULT FALSE,
  penalty NUMERIC(6,2) NOT NULL DEFAULT 0,
  metrics JSONB NOT NULL DEFAULT '[]'::jsonb,
  bad_since TIMESTAMPTZ,
  good_since TIMESTAMPTZ,
  active_until TIMESTAMPTZ,
  last_evaluated_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(canonical_model_id, site_model_id)
);

CREATE INDEX IF NOT EXISTS route_anomaly_states_model_idx ON route_anomaly_states (canonical_model_id);
CREATE INDEX IF NOT EXISTS route_anomaly_states_bad_since_idx ON route_anomaly_states (bad_since);
CREATE INDEX IF NOT EXISTS route_anomaly_states_active_idx ON route_anomaly_states (active_until DESC);

CREATE TABLE IF NOT EXISTS route_score_adjustments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  canonical_model_id UUID NOT NULL REFERENCES canonical_models(id) ON DELETE CASCADE,
  site_model_id UUID NOT NULL REFERENCES site_models(id) ON DELETE CASCADE,
  delta NUMERIC(8,2) NOT NULL DEFAULT 0,
  active_until TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(canonical_model_id, site_model_id)
);

CREATE INDEX IF NOT EXISTS route_score_adjustments_model_idx ON route_score_adjustments (canonical_model_id);
CREATE INDEX IF NOT EXISTS route_score_adjustments_active_idx ON route_score_adjustments (active_until DESC);

CREATE INDEX IF NOT EXISTS request_logs_site_model_created_idx ON request_logs (site_model_id, created_at DESC);

CREATE TRIGGER route_locks_set_updated_at
BEFORE UPDATE ON route_locks
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER route_anomaly_states_set_updated_at
BEFORE UPDATE ON route_anomaly_states
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER route_score_adjustments_set_updated_at
BEFORE UPDATE ON route_score_adjustments
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS route_score_adjustments_set_updated_at ON route_score_adjustments;
DROP TRIGGER IF EXISTS route_anomaly_states_set_updated_at ON route_anomaly_states;
DROP TRIGGER IF EXISTS route_locks_set_updated_at ON route_locks;
DROP INDEX IF EXISTS request_logs_site_model_created_idx;
DROP TABLE IF EXISTS route_score_adjustments;
DROP TABLE IF EXISTS route_anomaly_states;
DROP TABLE IF EXISTS route_locks;
ALTER TABLE health_snapshots DROP COLUMN IF EXISTS first_byte_latency_ms;
ALTER TABLE canonical_models DROP COLUMN IF EXISTS routing_expiry_rescue_enabled, DROP COLUMN IF EXISTS routing_preference;
ALTER TABLE api_keys DROP COLUMN IF EXISTS gateway_config;
