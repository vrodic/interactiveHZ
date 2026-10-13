import asyncio
from playwright.async_api import async_playwright

async def verify():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()
        page.on("console", lambda msg: print("CONSOLE:", msg.type, msg.text))
        page.on("pageerror", lambda err: print("PAGE ERROR:", err))

        await page.goto('http://localhost:8080')
        await page.wait_for_timeout(2000)

        res = await page.evaluate('''() => {
            try {
                updateTrainDetailsPanel({
                    train_number: '2010',
                    trip_id: 'trip_2010',
                    first_station_name: 'Zagreb Gl. kol.',
                    last_station_name: 'Vinkovci',
                    headsign: 'Zagreb - Vinkovci',
                    prev_station_name: 'Zagreb Gl. kol.',
                    next_station_name: 'Maksimir',
                    progress: 0.2,
                    delay_minutes: 10,
                    delay_distribution: {
                        total_records: 10,
                        ontime_percent: 60.0,
                        late_1_to_5_percent: 20.0,
                        late_5_to_10_percent: 10.0,
                        late_10_to_30_percent: 10.0,
                        late_over_30_percent: 0.0
                    },
                    recent_delays: [
                        { delay_date: '2026-10-10', delay_minutes: 10, position_status: 'In Transit', last_station: 'Zagreb Gl. kol.', updated_at: '10:15' }
                    ]
                }, true);
                return "SUCCESS";
            } catch (e) {
                return "ERROR: " + e.stack;
            }
        }''')
        print("EVAL RESULT:", res)

        recent_html = await page.inner_html('#recent-delays-section')
        print('RECENT DELAYS SECTION RENDERED:', 'Delay Logs (Last 3 Days)' in recent_html)

        await page.screenshot(path='/home/jules/verification/screenshots/recent_delays_3days.png', full_page=True)
        await browser.close()

asyncio.run(verify())
