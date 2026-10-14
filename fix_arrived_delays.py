#!/usr/bin/env python3
import sqlite3
import sys
from datetime import datetime, timedelta

def format_sec_to_datetime(date_str, sec):
    base = datetime.strptime(date_str, '%Y-%m-%d')
    res = base + timedelta(seconds=sec)
    return res.strftime('%Y-%m-%d %H:%M:%S')

def fix_arrived_delays(db_path):
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()

    # Pre-cache destination arrival_seconds for all trains
    cursor.execute("""
        SELECT COALESCE(t.trip_short_name, t.trip_id) as train_num, MAX(st.arrival_seconds)
        FROM stop_times st
        JOIN trips t ON st.trip_id = t.trip_id
        GROUP BY train_num
    """)
    sched_cache = {row[0]: row[1] for row in cursor.fetchall()}

    # Get all arrived historical records
    cursor.execute("""
        SELECT id, train_number, delay_date, delay_minutes, COALESCE(position_status, ''), COALESCE(last_station, ''), updated_at
        FROM historical_train_delays
        WHERE LOWER(position_status) = 'arrived'
        ORDER BY id ASC
    """)
    arrived_rows = cursor.fetchall()

    updates = []
    current_updates = []

    for row in arrived_rows:
        row_id, train_num, delay_date, delay_mins, pos_status, last_st, orig_updated_at = row

        cursor.execute("""
            SELECT delay_minutes
            FROM historical_train_delays
            WHERE train_number = ? AND delay_date = ? AND id < ?
            ORDER BY id DESC
            LIMIT 1
        """, (train_num, delay_date, row_id))
        prev = cursor.fetchone()

        target_delay = delay_mins
        if prev and prev[0] > 0 and delay_mins == 0:
            target_delay = prev[0]

        arr_sec = sched_cache.get(train_num)
        new_updated_at = orig_updated_at
        if arr_sec is not None:
            effective_sec = arr_sec + (target_delay * 60)
            new_updated_at = format_sec_to_datetime(delay_date, effective_sec)

        if target_delay != delay_mins or new_updated_at != orig_updated_at:
            updates.append((target_delay, new_updated_at, row_id))
            current_updates.append((target_delay, new_updated_at, train_num))

    cursor.executemany("""
        UPDATE historical_train_delays
        SET delay_minutes = ?, updated_at = ?
        WHERE id = ?
    """, updates)

    cursor.executemany("""
        UPDATE train_delays
        SET delay_minutes = ?, updated_at = ?
        WHERE train_number = ? AND LOWER(position_status) = 'arrived'
    """, current_updates)

    conn.commit()
    conn.close()
    print(f"Successfully processed arrived records. Updated {len(updates)} entries.")

if __name__ == '__main__':
    db_file = sys.argv[1] if len(sys.argv) > 1 else './data/hz_trains.db'
    fix_arrived_delays(db_file)
