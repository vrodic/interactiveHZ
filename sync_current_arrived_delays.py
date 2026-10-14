#!/usr/bin/env python3
import sqlite3
import sys

def sync_current_arrived_delays(db_path):
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()

    # Get the latest historical record for each train where position_status is 'arrived'
    cursor.execute("""
        SELECT h.train_number, h.delay_minutes, h.position_status, h.last_station, h.next_station, h.updated_at
        FROM historical_train_delays h
        JOIN (
            SELECT train_number, MAX(id) as max_id
            FROM historical_train_delays
            WHERE LOWER(position_status) = 'arrived'
            GROUP BY train_number
        ) latest ON h.id = latest.max_id
    """)
    latest_arrived_records = cursor.fetchall()

    updated_count = 0

    for rec in latest_arrived_records:
        train_num, delay_mins, pos_status, last_st, next_st, updated_at = rec

        cursor.execute("""
            UPDATE train_delays
            SET delay_minutes = ?,
                position_status = ?,
                last_station = ?,
                next_station = ?,
                updated_at = ?
            WHERE train_number = ?
        """, (delay_mins, pos_status, last_st, next_st, updated_at, train_num))

        if cursor.rowcount > 0:
            updated_count += cursor.rowcount

    conn.commit()
    conn.close()
    print(f"Successfully synchronized {updated_count} current train_delays records with historical arrived status.")

if __name__ == '__main__':
    db_file = sys.argv[1] if len(sys.argv) > 1 else './data/hz_trains.db'
    sync_current_arrived_delays(db_file)
