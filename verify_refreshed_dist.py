import asyncio
from playwright.async_api import async_playwright

async def verify():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()
        await page.goto('http://localhost:8080')
        await page.wait_for_timeout(3000)

        # Open train details modal/panel by calling openTrainDetails in page context
        await page.evaluate('''() => {
            openTrainDetails({
                train_number: '2011',
                trip_id: 'trip_2011',
                first_station_name: 'Zagreb Gl. kol.',
                last_station_name: 'Vinkovci',
                headsign: 'Zagreb - Vinkovci',
                prev_station_name: 'Zagreb Gl. kol.',
                next_station_name: 'Maksimir',
                progress: 0.2,
                delay_minutes: 0,
                delay_distribution: {
                    total_records: 10,
                    ontime_percent: 60.0,
                    late_1_to_5_percent: 20.0,
                    late_5_to_10_percent: 10.0,
                    late_10_to_30_percent: 10.0,
                    late_over_30_percent: 0.0
                }
            });
        }''')
        await page.wait_for_timeout(1000)

        dist = await page.inner_html('#delay-distribution-section')
        print('BEFORE REFRESH DIST HTML CONTAINS GRAPH:', 'Historical Punctuality Breakdown' in dist)

        # Click Refresh Live Delay Status button
        await page.click('#check-delay-btn')
        await page.wait_for_timeout(2000)

        dist_after = await page.inner_html('#delay-distribution-section')
        print('AFTER REFRESH DIST HTML CONTAINS GRAPH:', 'Historical Punctuality Breakdown' in dist_after)

        await page.screenshot(path='/home/jules/verification/screenshots/delay_dist_refreshed.png', full_page=True)
        await browser.close()

asyncio.run(verify())
