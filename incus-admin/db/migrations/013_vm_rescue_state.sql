-- +goose Up
-- PLAN-021 Phase D: rescue mode = safe-stop-with-snapshot
--
-- We don't swap the root disk (too risky with a shared production VM); we
-- take an automatic snapshot, stop the instance, and flip a rescue_state
-- flag. The admin can then:
--   - inspect / clone / download the disk out-of-band, or
--   - exit rescue with restore=true to roll back to the snapshot, or
--   - exit rescue with restore=false to resume where it stopped.
--
-- rescue_state transitions:
--   normal → rescue   (EnterRescue): take snapshot, stop VM, store snap name
--   rescue → normal   (ExitRescue):  optionally restore, start VM, clear fields

ALTER TABLE vms ADD COLUMN IF NOT EXISTS rescue_state          TEXT NOT NULL DEFAULT 'normal';
ALTER TABLE vms ADD COLUMN IF NOT EXISTS rescue_started_at     TIMESTAMPTZ;
ALTER TABLE vms ADD COLUMN IF NOT EXISTS rescue_snapshot_name  TEXT;

-- Guard against typos: only the two documented values make sense.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'vms_rescue_state_chk'
    ) THEN
        ALTER TABLE vms ADD CONSTRAINT vms_rescue_state_chk
            CHECK (rescue_state IN ('normal', 'rescue'));
    END IF;
END$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS idx_vms_rescue_state ON vms(rescue_state) WHERE rescue_state = 'rescue';

-- +goose Down
DROP INDEX IF EXISTS idx_vms_rescue_state;
ALTER TABLE vms DROP CONSTRAINT IF EXISTS vms_rescue_state_chk;
ALTER TABLE vms DROP COLUMN IF EXISTS rescue_snapshot_name;
ALTER TABLE vms DROP COLUMN IF EXISTS rescue_started_at;
ALTER TABLE vms DROP COLUMN IF EXISTS rescue_state;
