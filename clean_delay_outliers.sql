-- SQL Script to clean transient single-record delay spikes in historical_train_delays

DELETE FROM historical_train_delays
WHERE id IN (
    WITH ranked AS (
        SELECT id, train_number, delay_date, delay_minutes,
               LAG(delay_minutes) OVER (PARTITION BY train_number, delay_date ORDER BY id) as prev_delay,
               LEAD(delay_minutes) OVER (PARTITION BY train_number, delay_date ORDER BY id) as next_delay
        FROM historical_train_delays
    )
    SELECT id FROM ranked
    WHERE (prev_delay IS NOT NULL AND next_delay IS NOT NULL AND (delay_minutes - prev_delay > 45) AND (delay_minutes - next_delay > 45))
       OR (prev_delay IS NULL AND next_delay IS NOT NULL AND (delay_minutes - next_delay > 60))
       OR (next_delay IS NULL AND prev_delay IS NOT NULL AND (delay_minutes - prev_delay > 60))
);

-- Update train_delays to sync with the clean latest historical record for each active train
UPDATE train_delays
SET delay_minutes = (
    SELECT h.delay_minutes
    FROM historical_train_delays h
    WHERE h.train_number = train_delays.train_number
    ORDER BY h.id DESC LIMIT 1
),
position_status = (
    SELECT h.position_status
    FROM historical_train_delays h
    WHERE h.train_number = train_delays.train_number
    ORDER BY h.id DESC LIMIT 1
),
last_station = (
    SELECT h.last_station
    FROM historical_train_delays h
    WHERE h.train_number = train_delays.train_number
    ORDER BY h.id DESC LIMIT 1
),
next_station = (
    SELECT h.next_station
    FROM historical_train_delays h
    WHERE h.train_number = train_delays.train_number
    ORDER BY h.id DESC LIMIT 1
),
updated_at = (
    SELECT h.updated_at
    FROM historical_train_delays h
    WHERE h.train_number = train_delays.train_number
    ORDER BY h.id DESC LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM historical_train_delays h WHERE h.train_number = train_delays.train_number
);
