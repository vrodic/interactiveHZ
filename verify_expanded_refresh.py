import time
from playwright.sync_api import sync_playwright

def run():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(record_video_dir="/home/jules/verification/videos")
        page = context.new_page()

        page.goto("http://localhost:8080")
        page.wait_for_timeout(2000)

        # Open train details
        page.get_by_placeholder("Search station or train...").fill("700")
        page.wait_for_timeout(500)
        page.keyboard.press("Enter")
        page.wait_for_timeout(1000)

        # Expand day delay section
        toggle_el = page.locator("[id^='toggle-date-']").first
        if toggle_el.is_visible():
            toggle_el.click()
            page.wait_for_timeout(500)

        # Take screenshot of expanded section
        page.screenshot(path="/home/jules/verification/screenshots/expanded_before_refresh.png")

        # Click refresh delay button
        refresh_btn = page.get_by_role("button", name="📡 Refresh Live Delay Status")
        if refresh_btn.is_visible():
            refresh_btn.click()
            page.wait_for_timeout(1500)

        # Take screenshot after refresh
        page.screenshot(path="/home/jules/verification/screenshots/expanded_after_refresh.png")

        # Check that toggle arrow is still expanded (▴)
        if toggle_el.is_visible():
            assert toggle_el.inner_text() == "▴", f"Expected '▴', got '{toggle_el.inner_text()}'"

        context.close()
        browser.close()
        print("Verification successful!")

if __name__ == "__main__":
    run()
