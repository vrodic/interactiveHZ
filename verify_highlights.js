const { chromium } = require('playwright');

(async () => {
    const browser = await chromium.launch({ headless: true });
    const context = await browser.newContext({
        recordVideo: { dir: '/home/jules/verification/videos' },
        viewport: { width: 1280, height: 800 }
    });
    const page = await context.newPage();

    console.log('Navigating to http://localhost:8080...');
    await page.goto('http://localhost:8080');
    await page.waitForTimeout(2000);

    // Search and select station "Zagreb Glavni"
    await page.fill('#search-input', 'Zagreb Glavni');
    await page.waitForTimeout(500);
    const stationItem = page.locator('.search-item', { hasText: 'Zagreb Gl. kol.' }).first();
    if (await stationItem.isVisible()) {
        await stationItem.click();
    } else {
        const firstItem = page.locator('.search-item').first();
        if (await firstItem.isVisible()) await firstItem.click();
    }
    await page.waitForTimeout(1000);

    // Search and select train "2011" or "2010"
    await page.fill('#search-input', '201');
    await page.waitForTimeout(500);
    const trainItem = page.locator('.search-item', { hasText: 'Train' }).first();
    if (await trainItem.isVisible()) {
        await trainItem.click();
    }
    await page.waitForTimeout(1500);

    await page.screenshot({ path: '/home/jules/verification/screenshots/highlight_verification.png', fullPage: true });
    console.log('Screenshot saved to /home/jules/verification/screenshots/highlight_verification.png');

    await context.close();
    await browser.close();
})();
