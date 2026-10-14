-- Fix delay timestamps in SQLite database to compensate UTC to Zagreb local time (+2 hours CEST)
-- Applies to records updated before 2026-10-14 17:36:00 local time.

BEGIN TRANSACTION;

-- Update historical train delay timestamps recorded before 2026-10-14 17:36:00
UPDATE historical_train_delays
SET updated_at = datetime(updated_at, '+2 hours')
WHERE updated_at < '2026-10-14 17:36:00';

-- Update current train delay timestamps recorded before 2026-10-14 17:36:00
UPDATE train_delays
SET updated_at = datetime(updated_at, '+2 hours')
WHERE updated_at < '2026-10-14 17:36:00';

COMMIT;
