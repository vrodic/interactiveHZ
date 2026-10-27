let map;
let stationsData = [];
let activeTrainsData = [];
let segmentsData = [];
let stationMarkersMap = {};
let trainMarkersMap = {};
let segmentLinesMap = {};
let segmentPathsMap = {};
let segmentPathLengthsMap = {};
let segmentsLayerGroup = L.layerGroup();
let segmentBadgesLayerGroup = L.layerGroup();
// Canvas keeps ~1200 segment polylines in one element instead of one SVG path each.
let segmentsCanvasRenderer = L.canvas({ padding: 0.5, tolerance: 4 });
// All badge markers; only those inside the viewport are added to segmentBadgesLayerGroup.
let segmentBadgeMarkers = [];
let currentOpenedTrainNum = null;
let selectedTrainNum = null;
let selectedTrainRouteLayer = null;
let selectedStationHighlightLayer = null;
let selectedStationId = null;
let noticesData = [];
let stationNoticesMap = {};
let segmentNoticesMap = {};

let selectedTrainPollTimer = null;
let selectedTrainLastStation = null;
let selectedTrainLastNextStation = null;
let selectedTrainLastDelay = null;
let trainDelayDistributions = {};
let trainRecentDelays = {};

function showToast(title, body, isStationChange = false) {
    const container = document.getElementById('toast-container');
    if (container) {
        const toast = document.createElement('div');
        toast.className = `toast-notification ${isStationChange ? 'station-change' : ''}`;
        toast.innerHTML = `
            <div class="toast-title">${escapeHTML(title)}</div>
            <div class="toast-body">${escapeHTML(body)}</div>
        `;

        container.appendChild(toast);

        setTimeout(() => {
            toast.style.opacity = '0';
            toast.style.transition = 'opacity 0.3s ease';
            setTimeout(() => toast.remove(), 300);
        }, 5000);
    }

    if ("Notification" in window && Notification.permission === "granted") {
        showBrowserNotification(title, body);
    }
}

// Overlays (details panel, modals, trip planner) stack in the order they were opened,
// staying below the header and mobile menu (z-index 10000+).
let overlayTopZ = 6000;

function bringOverlayToFront(el) {
    if (el) el.style.zIndex = String(++overlayTopZ);
}

let swRegistrationPromise = null;

function registerServiceWorker() {
    if (!('serviceWorker' in navigator)) return null;
    if (!swRegistrationPromise) {
        swRegistrationPromise = navigator.serviceWorker.register('/sw.js')
            .then(() => navigator.serviceWorker.ready)
            .catch(e => {
                console.error('Service worker registration failed:', e);
                swRegistrationPromise = null;
                return null;
            });
    }
    return swRegistrationPromise;
}

async function showBrowserNotification(title, body) {
    const options = { body: body, tag: `hz-${title}`, renotify: true };
    // Mobile browsers (e.g. Firefox for Android) throw on `new Notification()`;
    // only the service worker registration can display notifications there.
    const reg = await registerServiceWorker();
    if (reg) {
        try {
            await reg.showNotification(title, options);
            return;
        } catch (e) {
            console.error('Service worker notification error:', e);
        }
    }
    try {
        new Notification(title, options);
    } catch (e) {
        console.error('Browser Notification error:', e);
    }
}

function updateNotificationButtonUI() {
    const btn = document.getElementById('enable-notifs-btn');
    const label = document.getElementById('enable-notifs-label');
    if (!btn || !label) return;

    if ("Notification" in window) {
        if (Notification.permission === "granted") {
            label.textContent = "Notifs Active";
            btn.style.borderColor = "#10b981";
            btn.style.color = "#10b981";
        } else if (Notification.permission === "denied") {
            label.textContent = "Notifs Blocked";
            btn.style.borderColor = "#ef4444";
            btn.style.color = "#ef4444";
        } else {
            label.textContent = "Enable Notifs";
            btn.style.borderColor = "";
            btn.style.color = "";
        }
    } else {
        label.textContent = "Notifs Unsupported";
    }
}

function updateDelayStatsUI(stats) {
    if (!stats) return;
    const totalEl = document.getElementById('ds-total-calls');
    const minEl = document.getElementById('ds-calls-min');
    const hourEl = document.getElementById('ds-calls-hour');
    const sfEl = document.getElementById('ds-success-fail');
    const lastEl = document.getElementById('ds-last-time');

    if (totalEl) totalEl.textContent = stats.total_calls || 0;
    if (minEl) minEl.textContent = stats.calls_last_minute || 0;
    if (hourEl) hourEl.textContent = stats.calls_last_hour || 0;
    if (sfEl) sfEl.textContent = `${stats.successful_calls || 0} / ${stats.failed_calls || 0}`;
    if (lastEl) lastEl.textContent = stats.last_call_time || 'Never';
}

function startAcceleratedPollingForSelectedTrain(trainNum) {
    stopAcceleratedPollingForSelectedTrain();

    const poll = async () => {
        if (selectedTrainNum !== trainNum) return;
        try {
            const resp = await fetch(`/api/train-delay?trainId=${encodeURIComponent(trainNum)}`);
            if (!resp.ok) return;
            const data = await resp.json();
            if (data.success && data.data) {
                checkStationChangeAndNotify(trainNum, data.data);
            }
        } catch (e) {
            console.error('Accelerated delay polling error:', e);
        }
    };

    poll();
    selectedTrainPollTimer = setInterval(poll, 30000);
}

function stopAcceleratedPollingForSelectedTrain() {
    if (selectedTrainPollTimer) {
        clearInterval(selectedTrainPollTimer);
        selectedTrainPollTimer = null;
    }
    selectedTrainLastStation = null;
    selectedTrainLastNextStation = null;
    selectedTrainLastDelay = null;
}

function checkStationChangeAndNotify(trainNum, delayData) {
    const lastStation = delayData.lastStation || delayData.last_station || '';
    const nextStation = delayData.nextStation || delayData.next_station || '';
    const posStatus = delayData.positionStatus || delayData.position_status || '';

    let delayVal = null;
    if (delayData.delayMinutes !== undefined && delayData.delayMinutes !== null) {
        delayVal = delayData.delayMinutes;
    } else if (delayData.delay_minutes !== undefined && delayData.delay_minutes !== null) {
        delayVal = delayData.delay_minutes;
    }

    // 1. Check Station Transition
    if (lastStation && selectedTrainLastStation !== null && selectedTrainLastStation !== lastStation) {
        const title = `🚆 Train ${trainNum} Station Update`;
        const body = `Train ${trainNum} reached station: ${lastStation} (Status: ${posStatus || 'In Transit'}${nextStation ? ', Next: ' + nextStation : ''})`;
        showToast(title, body, true);
    }

    // 2. Check Delay Changes
    if (delayVal !== null && selectedTrainLastDelay !== null && selectedTrainLastDelay !== delayVal) {
        const diff = delayVal - selectedTrainLastDelay;
        const diffStr = diff > 0 ? `+${diff}m increase` : `${diff}m decrease`;
        const delayStr = delayVal > 0 ? `+${delayVal} min delay` : 'On Time';
        const title = `⚠️ Train ${trainNum} Delay Update`;
        const body = `Train ${trainNum} delay status updated: ${delayStr} (${diffStr}). Current location: ${lastStation || 'In Transit'}.`;
        showToast(title, body, false);
    }

    if (lastStation) selectedTrainLastStation = lastStation;
    if (nextStation) selectedTrainLastNextStation = nextStation;
    if (delayVal !== null) selectedTrainLastDelay = delayVal;
}

let currentTimeSec = getCurrentSecondsOfDay();
let isRealtime = true;
let isPlaying = false;
let playbackSpeed = 1;
let lastFetchTime = 0;
let lastAnimTime = performance.now();
let lastMarkerUpdateTime = 0;
let activeTrainsFetchSeq = 0;

function escapeHTML(str) {
    if (!str) return '';
    return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function removeDiacritics(str) {
    if (!str) return '';
    return str.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
}

function getCurrentSecondsOfDay() {
    const d = new Date();
    return d.getHours() * 3600 + d.getMinutes() * 60 + d.getSeconds() + d.getMilliseconds() / 1000;
}

function formatSecondsToTime(totalSec) {
    totalSec = Math.floor(((totalSec % 86400) + 86400) % 86400);
    const h = Math.floor(totalSec / 3600);
    const m = Math.floor((totalSec % 3600) / 60);
    const s = Math.floor(totalSec % 60);
    return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

function stripSeconds(timeStr) {
    if (!timeStr) return '';
    const parts = timeStr.split(':');
    if (parts.length >= 2) {
        const h = parts[0].padStart(2, '0');
        const m = parts[1].padStart(2, '0');
        return `${h}:${m}`;
    }
    return timeStr;
}

document.addEventListener('DOMContentLoaded', () => {
    initMap();
    initControls();
    initNavHandlers();
    loadNotices();
    loadStations();
    loadSegments();
    startUpdateLoop();
});

let startupRouteHandled = false;

async function checkStartupRouteSelection() {
    if (startupRouteHandled) return;
    try {
        const pairs = JSON.parse(localStorage.getItem('hz_recent_pairs') || '[]');
        if (!pairs || pairs.length === 0) return;

        const latest = pairs[0];
        if (!latest.fromId || !latest.toId) return;

        startupRouteHandled = true;

        // Fill in Trip Planner inputs
        const fromInput = document.getElementById('route-from-input');
        const fromIdInput = document.getElementById('route-from-id');
        const toInput = document.getElementById('route-to-input');
        const toIdInput = document.getElementById('route-to-id');

        if (fromInput) fromInput.value = latest.fromName || '';
        if (fromIdInput) fromIdInput.value = latest.fromId || '';
        if (toInput) toInput.value = latest.toName || '';
        if (toIdInput) toIdInput.value = latest.toId || '';

        // Fetch route plan
        const resp = await fetch(`/api/routes/plan?from=${encodeURIComponent(latest.fromId)}&to=${encodeURIComponent(latest.toId)}&time=${Math.floor(currentTimeSec)}`);
        if (!resp.ok) return;
        const routes = await resp.json();
        if (!routes || routes.length === 0) return;

        // Find the train that will first come to the starting station
        let targetRoute = routes.find(r => r.is_nearest_future) || routes[0];
        const trNum = targetRoute.train_number ? targetRoute.train_number.split(' ')[0] : null;

        if (!trNum) return;

        // Check if train is currently active
        const activeTr = activeTrainsData.find(t => t.train_number === trNum || t.trip_id === trNum);

        if (activeTr && activeTr.lat && activeTr.lon) {
            map.setView([activeTr.lat, activeTr.lon], 12);
            openTrainDetails(activeTr);
        } else {
            // Find origin station location
            const originSt = stationsData.find(st => st.stop_id === latest.fromId || removeDiacritics(st.stop_name) === removeDiacritics(latest.fromName));
            if (originSt && originSt.lat && originSt.lon) {
                map.setView([originSt.lat, originSt.lon], 12);
            }
            openTrainDetails({ train_number: trNum });
        }
    } catch (e) {
        console.error('Failed to handle startup route selection:', e);
    }
}

let hideSpeedMarkers = false;

function initMap() {
    map = L.map('map').fitBounds([
        [45.8117, 15.9525],
        [45.8078, 16.3262]
    ], { padding: [40, 40] });

    const osmStandard = L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
        maxZoom: 19,
        attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
    });

    const openTopo = L.tileLayer('https://{s}.tile.opentopomap.org/{z}/{x}/{y}.png', {
        maxZoom: 17,
        attribution: 'Map data: &copy; OpenStreetMap contributors | Style: OpenTopoMap'
    });

    const esriWorldImagery = L.tileLayer('https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}', {
        maxZoom: 19,
        attribution: 'Tiles &copy; Esri'
    });

    osmStandard.addTo(map);

    const baseMaps = {
        "OSM Standard": osmStandard,
        "OpenTopoMap": openTopo,
        "Esri Satellite": esriWorldImagery
    };

    const overlayMaps = {
        "Track Segments": segmentsLayerGroup,
        "Segment Speed Badges": segmentBadgesLayerGroup
    };

    L.control.layers(baseMaps, overlayMaps, { position: 'topright' }).addTo(map);

    segmentsLayerGroup.addTo(map);
    segmentBadgesLayerGroup.addTo(map);

    map.on('zoomend', updateSegmentVisibility);
    map.on('moveend overlayadd', updateVisibleSegmentBadges);
    map.on('popupclose', () => {
        clearSelectedStationHighlight();
    });
}

function updateSegmentVisibility() {
    const zoom = map.getZoom();
    if (zoom < 8) {
        if (map.hasLayer(segmentsLayerGroup)) map.removeLayer(segmentsLayerGroup);
        if (map.hasLayer(segmentBadgesLayerGroup)) map.removeLayer(segmentBadgesLayerGroup);
    } else if (zoom < 10 || hideSpeedMarkers) {
        if (!map.hasLayer(segmentsLayerGroup)) map.addLayer(segmentsLayerGroup);
        if (map.hasLayer(segmentBadgesLayerGroup)) map.removeLayer(segmentBadgesLayerGroup);
    } else {
        if (!map.hasLayer(segmentsLayerGroup)) map.addLayer(segmentsLayerGroup);
        if (!map.hasLayer(segmentBadgesLayerGroup)) map.addLayer(segmentBadgesLayerGroup);
        updateVisibleSegmentBadges();
    }
}

// Leaflet keeps every marker in the DOM even when off-screen, so only badges near the viewport are added.
function updateVisibleSegmentBadges() {
    if (!map.hasLayer(segmentBadgesLayerGroup)) return;
    const bounds = map.getBounds().pad(0.25);
    segmentBadgeMarkers.forEach(marker => {
        const visible = bounds.contains(marker.getLatLng());
        const shown = segmentBadgesLayerGroup.hasLayer(marker);
        if (visible && !shown) segmentBadgesLayerGroup.addLayer(marker);
        else if (!visible && shown) segmentBadgesLayerGroup.removeLayer(marker);
    });
}

function initControls() {
    const timeSlider = document.getElementById('time-slider');
    const timeDisplay = document.getElementById('time-display');
    const playBtn = document.getElementById('play-btn');
    const realtimeBtn = document.getElementById('realtime-btn');
    const speedSelect = document.getElementById('speed-select');
    const searchInput = document.getElementById('search-input');
    const searchResults = document.getElementById('search-results');

    timeSlider.value = currentTimeSec;
    timeDisplay.textContent = formatSecondsToTime(currentTimeSec);

    let sliderDebounceTimer = null;

    timeSlider.addEventListener('input', (e) => {
        isRealtime = false;
        realtimeBtn.classList.remove('active');
        currentTimeSec = parseInt(e.target.value, 10);
        timeDisplay.textContent = formatSecondsToTime(currentTimeSec);

        if (sliderDebounceTimer) {
            clearTimeout(sliderDebounceTimer);
        }
        sliderDebounceTimer = setTimeout(() => {
            fetchActiveTrains();
        }, 100);
    });

    playBtn.addEventListener('click', () => {
        isPlaying = !isPlaying;
        if (isPlaying) {
            playBtn.textContent = '⏸ Pause';
            isRealtime = false;
            realtimeBtn.classList.remove('active');
        } else {
            playBtn.textContent = '▶ Play';
        }
    });

    if (speedSelect) {
        speedSelect.addEventListener('change', (e) => {
            playbackSpeed = parseFloat(e.target.value) || 1;
        });
    }

    realtimeBtn.addEventListener('click', () => {
        isRealtime = true;
        isPlaying = false;
        playBtn.textContent = '▶ Play';
        realtimeBtn.classList.add('active');
        currentTimeSec = getCurrentSecondsOfDay();
        timeSlider.value = currentTimeSec;
        timeDisplay.textContent = formatSecondsToTime(currentTimeSec);
        fetchActiveTrains();
    });

    let activeSearchIndex = -1;

    let searchDebounceTimer = null;
    searchInput.addEventListener('input', (e) => {
        const query = e.target.value.trim();
        activeSearchIndex = -1;

        if (query.length < 2) {
            searchResults.style.display = 'none';
            return;
        }

        if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
        searchDebounceTimer = setTimeout(async () => {
            try {
                const resp = await fetch(`/api/search?q=${encodeURIComponent(query)}`);
                const items = await resp.json();

                const normalizedQuery = removeDiacritics(query);

                items.sort((a, b) => {
                    const titleA = removeDiacritics(a.title);
                    const titleB = removeDiacritics(b.title);
                    const prefixA = titleA.startsWith(normalizedQuery);
                    const prefixB = titleB.startsWith(normalizedQuery);
                    if (prefixA && !prefixB) return -1;
                    if (!prefixA && prefixB) return 1;
                    return titleA.localeCompare(titleB);
                });

                searchResults.innerHTML = '';
                if (!items || items.length === 0) {
                    searchResults.innerHTML = '<div class="search-item" style="color: #a0aec0; cursor: default;">No stations or trains found</div>';
                    searchResults.style.display = 'block';
                    return;
                }

                items.forEach((item, index) => {
                    const div = document.createElement('div');
                    div.className = 'search-item';
                    div.dataset.index = index;
                    const icon = item.type === 'station' ? '🚉' : '🚆';
                    div.innerHTML = `<strong>${icon} ${escapeHTML(item.title)}</strong> <span style="font-size: 11px; color: #a0aec0;">(${escapeHTML(item.subtitle)})</span>`;
                    div.onclick = () => {
                        selectSearchResult(item);
                    };
                    searchResults.appendChild(div);
                });

                searchResults.style.display = 'block';
            } catch (err) {
                console.error('Search request failed:', err);
            }
        }, 150);
    });

    function selectSearchResult(item) {
        if (item.type === 'station') {
            map.setView([item.lat, item.lon], 12);
            openStationTimetable({ stop_id: item.id, stop_name: item.title, lat: item.lat, lon: item.lon });
        } else if (item.type === 'train') {
            openTrainDetails({ train_number: item.train_number || item.id, headsign: item.subtitle });
        }
        searchResults.style.display = 'none';
        searchInput.value = '';
    }

    searchInput.addEventListener('keydown', (e) => {
        const itemEls = searchResults.querySelectorAll('.search-item');
        if (!itemEls.length || searchResults.style.display === 'none') return;

        if (e.key === 'ArrowDown') {
            e.preventDefault();
            activeSearchIndex = (activeSearchIndex + 1) % itemEls.length;
            highlightSearchItem(itemEls);
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            activeSearchIndex = (activeSearchIndex - 1 + itemEls.length) % itemEls.length;
            highlightSearchItem(itemEls);
        } else if (e.key === 'Enter') {
            e.preventDefault();
            if (activeSearchIndex >= 0 && activeSearchIndex < itemEls.length) {
                itemEls[activeSearchIndex].click();
            }
        } else if (e.key === 'Escape') {
            searchResults.style.display = 'none';
        }
    });

    function highlightSearchItem(itemEls) {
        itemEls.forEach((el, idx) => {
            if (idx === activeSearchIndex) {
                el.style.background = '#2a3240';
                el.scrollIntoView({ block: 'nearest' });
            } else {
                el.style.background = '';
            }
        });
    }

    document.addEventListener('click', (e) => {
        if (!searchInput.contains(e.target) && !searchResults.contains(e.target)) {
            searchResults.style.display = 'none';
        }
    });

    document.getElementById('locate-train-btn').addEventListener('click', () => {
        locateTrainOnMap(currentOpenedTrainNum);
    });

    document.getElementById('close-panel').addEventListener('click', () => {
        document.getElementById('side-panel').style.display = 'none';
        currentOpenedTrainNum = null;
        selectedTrainNum = null;
        stopAcceleratedPollingForSelectedTrain();
        clearSelectedTrainRoute();
        updateTrainMarkersClientSide();
    });
}

function clearSelectedStationHighlight() {
    if (selectedStationHighlightLayer) {
        map.removeLayer(selectedStationHighlightLayer);
        selectedStationHighlightLayer = null;
    }
    selectedStationId = null;
}

function highlightSelectedStation(st) {
    clearSelectedStationHighlight();
    if (!st || !st.lat || !st.lon) return;

    selectedStationId = st.stop_id;
    const highlightIcon = L.divIcon({
        className: 'selected-station-marker',
        html: '<div class="selected-station-inner"></div>',
        iconSize: [32, 32],
        iconAnchor: [16, 16]
    });

    selectedStationHighlightLayer = L.marker([st.lat, st.lon], {
        icon: highlightIcon,
        zIndexOffset: 1000,
        interactive: false
    }).addTo(map);
}

function clearSelectedTrainRoute() {
    if (selectedTrainRouteLayer) {
        map.removeLayer(selectedTrainRouteLayer);
        selectedTrainRouteLayer = null;
    }
}

async function drawSelectedTrainRoute(trainNum) {
    clearSelectedTrainRoute();
    if (!trainNum) return;

    try {
        const resp = await fetch(`/api/trains/${encodeURIComponent(trainNum)}`);
        if (!resp.ok) return;
        const details = await resp.json();

        let coords = [];
        const activeTr = activeTrainsData.find(t => t.train_number === trainNum || t.trip_id === trainNum);
        if (activeTr && activeTr.path && activeTr.path.length > 0) {
            coords = activeTr.path.map(p => [p.lat, p.lon]);
        }

        if (coords.length < 2 && details.lat && details.lon) {
            coords = [[details.lat, details.lon]];
        }

        if (coords.length < 2 && activeTr && activeTr.prev_station_id && activeTr.next_station_id) {
            const segKey = `${activeTr.prev_station_id}->${activeTr.next_station_id}`;
            if (segmentPathsMap[segKey]) {
                coords = segmentPathsMap[segKey].map(p => [p.lat, p.lon]);
            }
        }

        if (coords.length >= 2) {
            const glowLine = L.polyline(coords, {
                color: '#ffea00',
                weight: 10,
                opacity: 0.8,
                lineCap: 'round',
                lineJoin: 'round'
            });

            const coreLine = L.polyline(coords, {
                color: '#ffffff',
                weight: 4,
                opacity: 1,
                lineCap: 'round',
                lineJoin: 'round'
            });

            selectedTrainRouteLayer = L.layerGroup([glowLine, coreLine]).addTo(map);
        }
    } catch (e) {
        console.error('Failed to draw train route highlight:', e);
    }
}

function haversineKm(lat1, lon1, lat2, lon2) {
    const radAvgLat = ((lat1 + lat2) / 2.0) * (Math.PI / 180.0);
    const cosLat = Math.cos(radAvgLat);
    const dx = (lon2 - lon1) * 40000.0 * cosLat / 360.0;
    const dy = (lat2 - lat1) * 40000.0 / 360.0;
    return Math.sqrt(dx * dx + dy * dy);
}

function calculateBearing(lat1, lon1, lat2, lon2) {
    const y = Math.sin((lon2 - lon1) * Math.PI / 180) * Math.cos(lat2 * Math.PI / 180);
    const x = Math.cos(lat1 * Math.PI / 180) * Math.sin(lat2 * Math.PI / 180) -
              Math.sin(lat1 * Math.PI / 180) * Math.cos(lat2 * Math.PI / 180) * Math.cos((lon2 - lon1) * Math.PI / 180);
    const brng = Math.atan2(y, x) * 180 / Math.PI;
    return (brng + 360) % 360;
}

function computePathSegmentLength(path) {
    if (!path || path.length < 2) return { totalDist: 0, segs: [] };
    let totalDist = 0;
    const segs = [];
    for (let i = 0; i < path.length - 1; i++) {
        const d = haversineKm(path[i].lat, path[i].lon, path[i + 1].lat, path[i + 1].lon) || 0.0001;
        segs.push({ p1: path[i], p2: path[i + 1], len: d });
        totalDist += d;
    }
    return { totalDist, segs };
}

function computeRealisticProgress(effTime, depSec, arrSec) {
    const totalDuration = arrSec - depSec;
    if (totalDuration <= 0) return 0;

    const elapsed = effTime - depSec;
    if (elapsed <= 0) return 0;
    if (elapsed >= totalDuration) return 1;

    // At least 20 seconds dwell time at station stop (or up to 20% for shorter segment durations)
    const minDwellSec = Math.min(20, totalDuration * 0.2);
    const motionDuration = totalDuration - minDwellSec;

    if (motionDuration <= 0) {
        return elapsed < totalDuration ? 0 : 1;
    }

    if (elapsed >= motionDuration) {
        // Dwell at the station stop
        return 1.0;
    }

    // Normalized progress along motion duration
    const x = elapsed / motionDuration;

    // Smooth cosine velocity profile (smooth acceleration from stop, smooth deceleration into stop)
    return (1 - Math.cos(Math.PI * x)) / 2;
}

function getPointAndBearingOnPath(path, progress) {
    if (!path || path.length === 0) return { pos: null, bearing: 0 };
    if (path.length === 1) return { pos: [path[0].lat, path[0].lon], bearing: 0 };

    const { totalDist, segs } = computePathSegmentLength(path);

    if (totalDist === 0 || segs.length === 0) return { pos: [path[0].lat, path[0].lon], bearing: 0 };

    if (progress <= 0) {
        const bearing = calculateBearing(segs[0].p1.lat, segs[0].p1.lon, segs[0].p2.lat, segs[0].p2.lon);
        return { pos: [path[0].lat, path[0].lon], bearing };
    }

    if (progress >= 1) {
        const lastSeg = segs[segs.length - 1];
        const bearing = calculateBearing(lastSeg.p1.lat, lastSeg.p1.lon, lastSeg.p2.lat, lastSeg.p2.lon);
        return { pos: [path[path.length - 1].lat, path[path.length - 1].lon], bearing };
    }

    const targetDist = progress * totalDist;
    let accum = 0;

    for (const seg of segs) {
        if (accum + seg.len >= targetDist) {
            const segProgress = (targetDist - accum) / seg.len;
            const lat = seg.p1.lat + (seg.p2.lat - seg.p1.lat) * segProgress;
            const lon = seg.p1.lon + (seg.p2.lon - seg.p1.lon) * segProgress;
            const bearing = calculateBearing(seg.p1.lat, seg.p1.lon, seg.p2.lat, seg.p2.lon);
            return { pos: [lat, lon], bearing };
        }
        accum += seg.len;
    }

    const lastSeg = segs[segs.length - 1];
    const bearing = calculateBearing(lastSeg.p1.lat, lastSeg.p1.lon, lastSeg.p2.lat, lastSeg.p2.lon);
    return { pos: [path[path.length - 1].lat, path[path.length - 1].lon], bearing };
}

function startUpdateLoop() {
    function animate(now) {
        const dt = (now - lastAnimTime) / 1000;
        lastAnimTime = now;

        if (isRealtime) {
            currentTimeSec = getCurrentSecondsOfDay();
            document.getElementById('time-slider').value = Math.floor(currentTimeSec);
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);

            if (now - lastFetchTime > 10000) {
                lastFetchTime = now;
                fetchActiveTrains();
            }
        } else if (isPlaying) {
            currentTimeSec = (currentTimeSec + dt * playbackSpeed) % 86400;
            document.getElementById('time-slider').value = Math.floor(currentTimeSec);
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);

            if (now - lastFetchTime > 5000) {
                lastFetchTime = now;
                fetchActiveTrains();
            }
        }

        if (now - lastMarkerUpdateTime > 100) {
            lastMarkerUpdateTime = now;
            updateTrainMarkersClientSide();
        }

        requestAnimationFrame(animate);
    }

    lastAnimTime = performance.now();
    requestAnimationFrame(animate);
}

function getTrainSVGHTML(trainNumber, isDelayed, bearing) {
    const color = isDelayed ? '#dc2626' : '#2563eb';
    const darkColor = isDelayed ? '#991b1b' : '#1e3a8a';
    const safeNum = escapeHTML(trainNumber);
    return `
        <div class="train-marker-inner">
            <div class="train-svg-wrapper">
                <svg class="train-svg" style="transform: rotate(${Math.round(bearing)}deg);" width="36" height="36" viewBox="0 0 40 40" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect x="7" y="3" width="26" height="34" rx="6" fill="${color}" fill-opacity="0.2" />
                    <rect x="10" y="5" width="20" height="30" rx="5" fill="${color}" stroke="#ffffff" stroke-width="2"/>
                    <path d="M11 9 C11 6.5, 14 5, 20 5 C26 5, 29 6.5, 29 9 L28 14 H12 Z" fill="${darkColor}" opacity="0.4"/>
                    <rect x="12" y="10" width="7" height="4.5" rx="1" fill="#bae6fd" stroke="${darkColor}" stroke-width="0.7"/>
                    <rect x="21" y="10" width="7" height="4.5" rx="1" fill="#bae6fd" stroke="${darkColor}" stroke-width="0.7"/>
                    <rect x="11.5" y="17" width="2" height="4" rx="0.5" fill="#e0f2fe"/>
                    <rect x="11.5" y="23" width="2" height="4" rx="0.5" fill="#e0f2fe"/>
                    <rect x="26.5" y="17" width="2" height="4" rx="0.5" fill="#e0f2fe"/>
                    <rect x="26.5" y="23" width="2" height="4" rx="0.5" fill="#e0f2fe"/>
                    <rect x="15" y="18" width="10" height="11" rx="2" fill="#ffffff" opacity="0.3"/>
                    <line x1="17" y1="20" x2="23" y2="20" stroke="#ffffff" stroke-width="1" opacity="0.8"/>
                    <line x1="17" y1="23" x2="23" y2="23" stroke="#ffffff" stroke-width="1" opacity="0.8"/>
                    <line x1="17" y1="26" x2="23" y2="26" stroke="#ffffff" stroke-width="1" opacity="0.8"/>
                    <circle cx="13" cy="7" r="1.5" fill="#fef08a" stroke="#ffffff" stroke-width="0.5"/>
                    <circle cx="27" cy="7" r="1.5" fill="#fef08a" stroke="#ffffff" stroke-width="0.5"/>
                    <circle cx="20" cy="6" r="1.2" fill="#ffffff"/>
                </svg>
            </div>
            <div class="train-number-badge">${safeNum}</div>
        </div>
    `;
}

function updateTrainMarkersClientSide() {
    const currentTrainIds = new Set();

    activeTrainsData.forEach(train => {
        currentTrainIds.add(train.trip_id);

        const effTime = currentTimeSec - (train.delay_minutes || 0) * 60;
        const depSec = train.scheduled_dep_sec;
        const arrSec = train.scheduled_arr_sec;
        const duration = arrSec - depSec;

        let progress = train.progress;
        if (duration > 0) {
            progress = computeRealisticProgress(effTime, depSec, arrSec);
        }

        let pos = [train.lat, train.lon];
        let bearing = 0;

        let trainPath = train.path;
        if ((!trainPath || trainPath.length === 0) && train.segment_id && segmentPathsMap[train.segment_id]) {
            trainPath = segmentPathsMap[train.segment_id];
        } else if ((!trainPath || trainPath.length === 0) && train.prev_station_id && train.next_station_id) {
            const segKey = `${train.prev_station_id}->${train.next_station_id}`;
            if (segmentPathsMap[segKey]) {
                trainPath = segmentPathsMap[segKey];
            }
        }

        if (trainPath && trainPath.length > 0) {
            const pb = getPointAndBearingOnPath(trainPath, progress);
            if (pb.pos) {
                pos = pb.pos;
                bearing = pb.bearing;
            }
        }

        const isDelayed = train.delay_minutes > 0;
        const isSelected = selectedTrainNum && (train.train_number === selectedTrainNum || train.trip_id === selectedTrainNum);

        if (trainMarkersMap[train.trip_id]) {
            const marker = trainMarkersMap[train.trip_id];
            marker.setLatLng(pos);
            const el = marker.getElement();
            if (el) {
                const svgEl = el.querySelector('.train-svg');
                if (svgEl) {
                    svgEl.style.transform = `rotate(${Math.round(bearing)}deg)`;
                }
                if (isDelayed) {
                    el.classList.add('delayed');
                } else {
                    el.classList.remove('delayed');
                }
                if (isSelected) {
                    el.classList.add('selected-train-marker');
                } else {
                    el.classList.remove('selected-train-marker');
                }
            }
        } else {
            const icon = L.divIcon({
                className: `train-marker-container ${isDelayed ? 'delayed' : ''} ${isSelected ? 'selected-train-marker' : ''}`,
                html: getTrainSVGHTML(train.train_number, isDelayed, bearing),
                iconSize: [50, 62],
                iconAnchor: [25, 18]
            });

            const marker = L.marker(pos, { icon: icon }).addTo(map);

            marker.on('click', () => {
                openTrainDetails(train);
            });

            trainMarkersMap[train.trip_id] = marker;
        }
    });

    Object.keys(trainMarkersMap).forEach(tripId => {
        if (!currentTrainIds.has(tripId)) {
            map.removeLayer(trainMarkersMap[tripId]);
            delete trainMarkersMap[tripId];
        }
    });
}

let evtSource = null;

function initNavHandlers() {
    const activeTrainsModal = document.getElementById('active-trains-modal');
    const dashboardModal = document.getElementById('dashboard-modal');
    const delaysStreamModal = document.getElementById('delays-stream-modal');
    const tripPlannerDrawer = document.getElementById('trip-planner-drawer');
    const leftSidebar = document.getElementById('left-sidebar');
    const timeControls = document.getElementById('time-controls');
    const mobileMenuBtn = document.getElementById('mobile-menu-btn');
    const toggleTimeControlsBtn = document.getElementById('toggle-time-controls-btn');

    if (mobileMenuBtn && leftSidebar) {
        mobileMenuBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            leftSidebar.classList.toggle('mobile-open');
        });

        // Tapping anywhere outside the open menu dismisses it.
        document.addEventListener('click', (e) => {
            if (leftSidebar.classList.contains('mobile-open') && !leftSidebar.contains(e.target)) {
                leftSidebar.classList.remove('mobile-open');
            }
        });
    }

    if (toggleTimeControlsBtn && timeControls) {
        toggleTimeControlsBtn.addEventListener('click', () => {
            const isHidden = timeControls.style.display === 'none' || !timeControls.style.display;
            timeControls.style.display = isHidden ? 'flex' : 'none';
            toggleTimeControlsBtn.classList.toggle('active', isHidden);
            closeMobileMenu();
        });
    }

    function closeMobileMenu() {
        if (leftSidebar && window.innerWidth <= 768) {
            leftSidebar.classList.remove('mobile-open');
        }
    }

    const noticesModal = document.getElementById('notices-modal');

    document.getElementById('nav-notices').addEventListener('click', () => {
        closeMobileMenu();
        openNoticesModal();
    });

    document.getElementById('close-notices-modal').addEventListener('click', () => {
        noticesModal.style.display = 'none';
    });

    document.getElementById('nav-active-trains').addEventListener('click', () => {
        closeMobileMenu();
        openActiveTrainsModal();
    });

    document.getElementById('close-active-trains-modal').addEventListener('click', () => {
        activeTrainsModal.style.display = 'none';
    });

    document.getElementById('nav-dashboard').addEventListener('click', () => {
        closeMobileMenu();
        openDashboardModal();
    });

    document.getElementById('close-dashboard-modal').addEventListener('click', () => {
        dashboardModal.style.display = 'none';
    });

    document.getElementById('nav-delays-stream').addEventListener('click', () => {
        closeMobileMenu();
        openDelaysStreamModal();
    });

    document.getElementById('close-delays-stream-modal').addEventListener('click', () => {
        delaysStreamModal.style.display = 'none';
    });

    const enableNotifsBtn = document.getElementById('enable-notifs-btn');
    if ("Notification" in window && Notification.permission === "granted") {
        registerServiceWorker();
    }
    if (enableNotifsBtn) {
        enableNotifsBtn.addEventListener('click', async () => {
            if ("Notification" in window) {
                try {
                    const perm = await Notification.requestPermission();
                    updateNotificationButtonUI();
                    closeMobileMenu();
                    if (perm === "granted") {
                        showToast("🔔 Notifications Enabled", "You will now receive desktop notifications for train delay updates and station changes.");
                    } else if (perm === "denied") {
                        alert("Notifications are blocked in your browser settings. Please enable notifications for this site to receive updates.");
                    }
                } catch (e) {
                    console.error("Failed to request notification permission:", e);
                }
            } else {
                alert("Desktop notifications are not supported by your browser.");
            }
        });
        updateNotificationButtonUI();
    }

    const toggleSpeedsBtn = document.getElementById('toggle-speeds-btn');
    const toggleSpeedsLabel = document.getElementById('toggle-speeds-label');
    if (toggleSpeedsBtn) {
        toggleSpeedsBtn.addEventListener('click', () => {
            hideSpeedMarkers = !hideSpeedMarkers;
            if (hideSpeedMarkers) {
                toggleSpeedsLabel.textContent = 'Show Speeds';
                toggleSpeedsBtn.classList.add('active');
            } else {
                toggleSpeedsLabel.textContent = 'Hide Speeds';
                toggleSpeedsBtn.classList.remove('active');
            }
            updateSegmentVisibility();
        });
    }

    document.getElementById('nav-routes').addEventListener('click', () => {
        closeMobileMenu();
        tripPlannerDrawer.style.display = 'flex';
        bringOverlayToFront(tripPlannerDrawer);
    });

    document.getElementById('close-trip-planner-drawer').addEventListener('click', () => {
        tripPlannerDrawer.style.display = 'none';
    });

    document.getElementById('find-routes-btn').addEventListener('click', () => {
        findRoutePlans();
    });

    const swapBtn = document.getElementById('swap-stations-btn');
    if (swapBtn) {
        swapBtn.addEventListener('click', () => {
            const fromInput = document.getElementById('route-from-input');
            const fromIdInput = document.getElementById('route-from-id');
            const toInput = document.getElementById('route-to-input');
            const toIdInput = document.getElementById('route-to-id');

            const tempVal = fromInput.value;
            const tempId = fromIdInput.value;

            fromInput.value = toInput.value;
            fromIdInput.value = toIdInput.value;

            toInput.value = tempVal;
            toIdInput.value = tempId;

            if (fromIdInput.value && toIdInput.value) {
                findRoutePlans();
            }
        });
    }

    const fromLocateBtn = document.getElementById('route-from-locate-btn');
    if (fromLocateBtn) {
        fromLocateBtn.addEventListener('click', fillOriginFromGeolocation);
    }

    setupStationAutocomplete('route-from-input', 'route-from-id', 'route-from-results');
    setupStationAutocomplete('route-to-input', 'route-to-id', 'route-to-results');

    renderRecentPairs();

    [noticesModal, activeTrainsModal, dashboardModal, delaysStreamModal].forEach(modal => {
        modal.addEventListener('click', (e) => {
            if (e.target === modal) {
                modal.style.display = 'none';
            }
        });
    });

    document.addEventListener('click', (evt) => {
        const badge = evt.target.closest('.clickable-notice-badge');
        if (badge) {
            evt.stopPropagation();
            evt.preventDefault();
            const noticeId = badge.getAttribute('data-notice-id');
            openNoticesModal(noticeId);
        }
    });

    initDelaysSSEStream();
}

function initDelaysSSEStream() {
    if (evtSource) return;

    evtSource = new EventSource('/api/delays/stream');

    evtSource.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data);
            appendStreamLog(data);

            if (data.delay_service_stats) {
                updateDelayStatsUI(data.delay_service_stats);
            }

            if (data.type === 'delay_update') {
                const tr = activeTrainsData.find(t => t.train_number === data.train_number);
                if (tr) {
                    tr.delay_minutes = data.delay_minutes;
                    updateTrainMarkersClientSide();
                }

                if (selectedTrainNum && data.train_number === selectedTrainNum) {
                    checkStationChangeAndNotify(selectedTrainNum, data);
                }
            }
        } catch (e) {
            console.error('Failed to parse SSE event:', e);
        }
    };

    evtSource.onerror = () => {
        const badge = document.getElementById('stream-status-badge');
        if (badge) {
            badge.style.background = '#ef4444';
            badge.textContent = 'RECONNECTING';
        }
    };

    evtSource.onopen = () => {
        const badge = document.getElementById('stream-status-badge');
        if (badge) {
            badge.style.background = '#10b981';
            badge.textContent = 'LIVE SSE';
        }
    };
}

function openDelaysStreamModal() {
    const modal = document.getElementById('delays-stream-modal');
    modal.style.display = 'flex';
    bringOverlayToFront(modal);
}

function appendStreamLog(data) {
    const logContainer = document.getElementById('delays-stream-log');
    if (!logContainer) return;

    const div = document.createElement('div');
    div.style.marginBottom = '6px';
    div.style.borderBottom = '1px solid #161b22';
    div.style.paddingBottom = '4px';

    const time = escapeHTML(data.timestamp || new Date().toLocaleTimeString());

    if (data.type === 'connected') {
        div.innerHTML = `<span style="color: #10b981;">[${time}]</span> <strong style="color: #58a6ff;">SYSTEM:</strong> ${escapeHTML(data.message)}`;
    } else if (data.type === 'delay_update') {
        const delayMins = data.delay_minutes || 0;
        const delayColor = delayMins > 0 ? '#f85149' : '#3fb950';
        div.innerHTML = `<span style="color: #8b949e;">[${time}]</span> <strong style="color: #e6edf3;">Train ${escapeHTML(data.train_number)}:</strong> Delay: <span style="color: ${delayColor}; font-weight: bold;">+${delayMins} min</span> | Status: <span style="color: #d2a8ff;">${escapeHTML(data.position_status || 'N/A')}</span> | Last: ${escapeHTML(data.last_station || 'N/A')}`;
    } else {
        div.innerHTML = `<span style="color: #8b949e;">[${time}]</span> ${escapeHTML(JSON.stringify(data))}`;
    }

    logContainer.prepend(div);

    while (logContainer.children.length > 100) {
        logContainer.removeChild(logContainer.lastChild);
    }
}

function renderNoticesModalBody() {
    const body = document.getElementById('notices-modal-body');
    if (!body) return;

    if (!noticesData || noticesData.length === 0) {
        body.innerHTML = '<p style="text-align: center; color: #a0aec0; padding: 20px;">No active traffic notices or track works reported.</p>';
        return;
    }

    let cardsHTML = '';
    noticesData.forEach(item => {
        let badgesHTML = '';
        if (item.is_bus_replacement) {
            badgesHTML += `<span class="notice-badge notice-badge-bus">🚌 Bus replacement</span> `;
        }
        if (item.is_track_works) {
            badgesHTML += `<span class="notice-badge notice-badge-works">🚧 Track works</span> `;
        }

        let datesHTML = '';
        if (item.dates_json && item.dates_json.length > 0) {
            datesHTML = `<div style="font-size: 11px; color: #00e5ff; margin-bottom: 6px;">📅 Dates: ${item.dates_json.map(d => escapeHTML(d)).join(', ')}</div>`;
        } else if (item.date_from || item.date_to) {
            datesHTML = `<div style="font-size: 11px; color: #00e5ff; margin-bottom: 6px;">📅 Date: ${escapeHTML(item.date_from)} ${item.date_to ? '➔ ' + escapeHTML(item.date_to) : ''}</div>`;
        }

        let pdfHTML = '';
        if (item.pdf_urls && item.pdf_urls.length > 0) {
            pdfHTML += `<div style="margin-top: 8px; font-size: 11px;">📄 Linked documents: `;
            item.pdf_urls.forEach((url, i) => {
                pdfHTML += `<a href="${escapeHTML(url)}" target="_blank" rel="noopener" style="color: #00e5ff; margin-right: 8px; text-decoration: underline;">Schedule PDF ${i + 1}</a>`;
            });
            pdfHTML += `</div>`;
        }

        cardsHTML += `
            <div class="notice-card" id="notice-card-${escapeHTML(item.id)}" data-notice-id="${escapeHTML(item.id)}">
                <div class="notice-card-header">
                    <div class="notice-card-title">${escapeHTML(item.title)}</div>
                    <div>${badgesHTML}</div>
                </div>
                ${datesHTML}
                <div class="notice-card-content">${escapeHTML(item.content)}</div>
                ${pdfHTML}
            </div>
        `;
    });

    body.innerHTML = cardsHTML;
}

function openNoticesModal(highlightNoticeId = null) {
    const modal = document.getElementById('notices-modal');
    if (modal) {
        modal.style.display = 'flex';
        bringOverlayToFront(modal);
        renderNoticesModalBody();

        if (highlightNoticeId) {
            setTimeout(() => {
                const targetCard = document.getElementById(`notice-card-${highlightNoticeId}`);
                if (targetCard) {
                    targetCard.scrollIntoView({ behavior: 'smooth', block: 'center' });
                    targetCard.classList.add('notice-card-highlight');
                    setTimeout(() => {
                        targetCard.classList.remove('notice-card-highlight');
                    }, 4000);
                }
            }, 100);
        }
    }
}

function openActiveTrainsModal() {
    const modal = document.getElementById('active-trains-modal');
    const body = document.getElementById('active-trains-modal-body');

    let rowsHTML = '';
    const seenTrainNums = new Set();
    const uniqueTrains = activeTrainsData.filter(tr => {
        if (seenTrainNums.has(tr.train_number)) return false;
        seenTrainNums.add(tr.train_number);
        return true;
    });

    if (uniqueTrains.length === 0) {
        rowsHTML = `<tr><td colspan="5" style="padding: 16px; text-align: center; color: #a0aec0;">No active trains at current time.</td></tr>`;
    } else {
        uniqueTrains.forEach(tr => {
            const delayStr = tr.delay_minutes > 0
                ? `<span style="color: #ef4444; font-weight: bold;">+${tr.delay_minutes} min delay</span>`
                : `<span style="color: #10b981; font-weight: bold;">On Time</span>`;

            rowsHTML += `
                <tr style="border-bottom: 1px solid #2a313d; cursor: pointer;" class="active-train-row" data-train="${escapeHTML(tr.train_number)}">
                    <td style="padding: 10px;"><strong>${escapeHTML(tr.train_number)}</strong></td>
                    <td style="padding: 10px;">🚩 ${escapeHTML(tr.first_station_name || 'N/A')}</td>
                    <td style="padding: 10px;">🏁 ${escapeHTML(tr.last_station_name || 'N/A')}</td>
                    <td style="padding: 10px;">${escapeHTML(tr.prev_station_name)} ➔ ${escapeHTML(tr.next_station_name)} (${Math.round(tr.progress * 100)}%)</td>
                    <td style="padding: 10px;">${delayStr}</td>
                </tr>
            `;
        });
    }

    body.innerHTML = `
        <table style="width: 100%; border-collapse: collapse; font-size: 14px;">
            <thead>
                <tr style="border-bottom: 2px solid #3a4250; text-align: left; color: #a0aec0;">
                    <th style="padding: 8px;">Train Number</th>
                    <th style="padding: 8px;">Origin</th>
                    <th style="padding: 8px;">Destination</th>
                    <th style="padding: 8px;">Current Segment</th>
                    <th style="padding: 8px;">Delay Status</th>
                </tr>
            </thead>
            <tbody>
                ${rowsHTML}
            </tbody>
        </table>
    `;

    modal.style.display = 'flex';
    bringOverlayToFront(modal);

    body.querySelectorAll('.active-train-row').forEach(row => {
        row.addEventListener('click', () => {
            const trNum = row.getAttribute('data-train');
            const tr = activeTrainsData.find(t => t.train_number === trNum);
            if (tr) {
                map.setView([tr.lat, tr.lon], 11);
                openTrainDetails(tr);
                modal.style.display = 'none';
            }
        });
    });
}

async function openDashboardModal() {
    const modal = document.getElementById('dashboard-modal');
    const body = document.getElementById('dashboard-modal-body');

    body.innerHTML = '<p style="text-align: center; color: #a0aec0; padding: 20px;">Loading live dashboard metrics...</p>';
    modal.style.display = 'flex';
    bringOverlayToFront(modal);

    try {
        const resp = await fetch(`/api/dashboard?time=${formatSecondsToTime(currentTimeSec)}`);
        const stats = await resp.json();

        let delayedRows = '';
        if (stats.recent_delayed_trains && stats.recent_delayed_trains.length > 0) {
            const seenDelayedNums = new Set();
            const uniqueDelayed = stats.recent_delayed_trains.filter(tr => {
                if (seenDelayedNums.has(tr.train_number)) return false;
                seenDelayedNums.add(tr.train_number);
                return true;
            });

            uniqueDelayed.forEach(tr => {
                delayedRows += `
                    <tr style="border-bottom: 1px solid #2a313d;">
                        <td style="padding: 8px;"><strong>${escapeHTML(tr.train_number)}</strong></td>
                        <td style="padding: 8px;">${escapeHTML(tr.first_station_name)} ➔ ${escapeHTML(tr.last_station_name)}</td>
                        <td style="padding: 8px; color: #ef4444; font-weight: bold;">+${tr.delay_minutes} min</td>
                    </tr>
                `;
            });
        } else {
            delayedRows = `<tr><td colspan="3" style="padding: 12px; text-align: center; color: #10b981;">No delayed trains currently active! 🎉</td></tr>`;
        }

        const tripsCount = stats.trips_running_today || stats.total_trips_today || 0;

        const dsStats = stats.delay_service_stats || {};
        const dsCalls = dsStats.total_calls || 0;
        const dsMin = dsStats.calls_last_minute || 0;

        body.innerHTML = `
            <div class="dashboard-grid">
                <div class="dash-card">
                    <div class="dash-label">Active Trains</div>
                    <div class="dash-value">${stats.active_train_count}</div>
                </div>
                <div class="dash-card">
                    <div class="dash-label">Delayed Trains</div>
                    <div class="dash-value" style="color: ${stats.delayed_train_count > 0 ? '#ef4444' : '#10b981'};">${stats.delayed_train_count}</div>
                </div>
                <div class="dash-card">
                    <div class="dash-label">Average Delay</div>
                    <div class="dash-value" style="color: #f59e0b;">${stats.avg_delay_minutes.toFixed(1)} min</div>
                </div>
                <div class="dash-card">
                    <div class="dash-label">Trips Running Today</div>
                    <div class="dash-value">${tripsCount}</div>
                </div>
                <div class="dash-card">
                    <div class="dash-label">Delay API Calls (Total/Min)</div>
                    <div class="dash-value" style="color: #00e5ff;">${dsCalls} <span style="font-size: 14px; color: #a0aec0;">(${dsMin}/min)</span></div>
                </div>
            </div>

            <h3 style="margin-bottom: 10px; color: #fff; font-size: 16px;">⚠️ Currently Delayed Trains</h3>
            <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
                <thead>
                    <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                        <th style="padding: 6px;">Train</th>
                        <th style="padding: 6px;">Route</th>
                        <th style="padding: 6px;">Delay</th>
                    </tr>
                </thead>
                <tbody>
                    ${delayedRows}
                </tbody>
            </table>
        `;
    } catch (e) {
        body.innerHTML = `<p style="color: #ef4444; padding: 20px;">Failed to load dashboard statistics: ${escapeHTML(e.message)}</p>`;
    }
}

function saveRecentPair(fromId, fromName, toId, toName) {
    if (!fromId || !toId || fromId === toId) return;
    try {
        let pairs = JSON.parse(localStorage.getItem('hz_recent_pairs') || '[]');
        pairs = pairs.filter(p => !(p.fromId === fromId && p.toId === toId));
        pairs.unshift({ fromId, fromName, toId, toName });
        if (pairs.length > 5) pairs = pairs.slice(0, 5);
        localStorage.setItem('hz_recent_pairs', JSON.stringify(pairs));
        renderRecentPairs();
    } catch (e) {
        console.error('Failed to save recent pair:', e);
    }
}

function setupStationAutocomplete(inputId, idInputId, resultsId) {
    const inputEl = document.getElementById(inputId);
    const hiddenEl = document.getElementById(idInputId);
    const resultsEl = document.getElementById(resultsId);

    if (!inputEl || !hiddenEl || !resultsEl) return;

    let activeIndex = -1;

    inputEl.addEventListener('input', (e) => {
        const query = e.target.value.trim();
        hiddenEl.value = '';
        activeIndex = -1;

        if (query.length < 1) {
            resultsEl.style.display = 'none';
            return;
        }

        const normalizedQuery = removeDiacritics(query);
        const matches = stationsData.filter(st => {
            const normName = removeDiacritics(st.stop_name);
            return normName.includes(normalizedQuery);
        });

        matches.sort((a, b) => {
            const normA = removeDiacritics(a.stop_name);
            const normB = removeDiacritics(b.stop_name);
            const prefixA = normA.startsWith(normalizedQuery);
            const prefixB = normB.startsWith(normalizedQuery);
            if (prefixA && !prefixB) return -1;
            if (!prefixA && prefixB) return 1;
            return normA.localeCompare(normB);
        });

        resultsEl.innerHTML = '';
        if (matches.length === 0) {
            resultsEl.innerHTML = '<div class="search-item" style="color: #a0aec0; cursor: default;">No stations found</div>';
            resultsEl.style.display = 'block';
            return;
        }

        matches.slice(0, 10).forEach((st, idx) => {
            const div = document.createElement('div');
            div.className = 'search-item';
            div.dataset.index = idx;
            div.innerHTML = `<strong>🚉 ${escapeHTML(st.stop_name)}</strong>`;
            div.onclick = () => {
                inputEl.value = st.stop_name;
                hiddenEl.value = st.stop_id;
                resultsEl.style.display = 'none';
            };
            resultsEl.appendChild(div);
        });

        resultsEl.style.display = 'block';
    });

    inputEl.addEventListener('keydown', (e) => {
        const itemEls = resultsEl.querySelectorAll('.search-item');
        if (!itemEls.length || resultsEl.style.display === 'none') return;

        if (e.key === 'ArrowDown') {
            e.preventDefault();
            activeIndex = (activeIndex + 1) % itemEls.length;
            highlightItem(itemEls);
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            activeIndex = (activeIndex - 1 + itemEls.length) % itemEls.length;
            highlightItem(itemEls);
        } else if (e.key === 'Enter') {
            e.preventDefault();
            if (activeIndex >= 0 && activeIndex < itemEls.length) {
                itemEls[activeIndex].click();
            }
        } else if (e.key === 'Escape') {
            resultsEl.style.display = 'none';
        }
    });

    function highlightItem(itemEls) {
        itemEls.forEach((el, idx) => {
            if (idx === activeIndex) {
                el.style.background = '#2a3240';
                el.scrollIntoView({ block: 'nearest' });
            } else {
                el.style.background = '';
            }
        });
    }

    document.addEventListener('click', (e) => {
        if (!inputEl.contains(e.target) && !resultsEl.contains(e.target)) {
            resultsEl.style.display = 'none';
        }
    });
}

function fillOriginFromGeolocation() {
    const btn = document.getElementById('route-from-locate-btn');
    if (!('geolocation' in navigator)) {
        showToast('📍 Location unavailable', 'Your browser does not support geolocation.');
        return;
    }
    if (stationsData.length === 0) {
        showToast('📍 Stations not loaded', 'Station list is still loading, please try again in a moment.');
        return;
    }

    btn.disabled = true;
    btn.classList.add('loading');
    navigator.geolocation.getCurrentPosition((pos) => {
        btn.disabled = false;
        btn.classList.remove('loading');
        const { latitude, longitude } = pos.coords;

        let nearest = null;
        let nearestDist = Infinity;
        stationsData.forEach(st => {
            const d = haversineKm(latitude, longitude, st.lat, st.lon);
            if (d < nearestDist) {
                nearestDist = d;
                nearest = st;
            }
        });
        if (!nearest) return;

        document.getElementById('route-from-input').value = nearest.stop_name;
        document.getElementById('route-from-id').value = nearest.stop_id;
        showToast('📍 Nearest station', `${nearest.stop_name} (${nearestDist.toFixed(1)} km away)`);

        if (document.getElementById('route-to-id').value) {
            findRoutePlans();
        }
    }, (err) => {
        btn.disabled = false;
        btn.classList.remove('loading');
        const msg = err.code === err.PERMISSION_DENIED
            ? 'Location access was denied. Allow location for this site to use this feature.'
            : 'Could not determine your location.';
        showToast('📍 Location error', msg);
    }, { enableHighAccuracy: true, timeout: 15000, maximumAge: 60000 });
}

function renderRecentPairs() {
    const container = document.getElementById('recent-pairs-container');
    const chips = document.getElementById('recent-pairs-chips');
    if (!container || !chips) return;

    try {
        const pairs = JSON.parse(localStorage.getItem('hz_recent_pairs') || '[]');
        if (pairs.length === 0) {
            container.style.display = 'none';
            return;
        }

        chips.innerHTML = '';
        pairs.forEach(p => {
            const btn = document.createElement('button');
            btn.style.cssText = 'background: #2a313d; border: 1px solid #3a4250; color: #00e5ff; font-size: 11px; padding: 3px 8px; border-radius: 12px; cursor: pointer;';
            btn.textContent = `${p.fromName} ➔ ${p.toName}`;
            btn.onclick = () => {
                document.getElementById('route-from-input').value = p.fromName;
                document.getElementById('route-from-id').value = p.fromId;
                document.getElementById('route-to-input').value = p.toName;
                document.getElementById('route-to-id').value = p.toId;
                findRoutePlans();
            };
            chips.appendChild(btn);
        });
        container.style.display = 'block';
    } catch (e) {
        container.style.display = 'none';
    }
}

async function findRoutePlans() {
    const fromInput = document.getElementById('route-from-input');
    const fromIdInput = document.getElementById('route-from-id');
    const toInput = document.getElementById('route-to-input');
    const toIdInput = document.getElementById('route-to-id');

    let fromId = fromIdInput.value;
    let toId = toIdInput.value;
    const resultsContainer = document.getElementById('route-results-container');

    if (!fromId && fromInput.value.trim()) {
        const query = removeDiacritics(fromInput.value.trim());
        const match = stationsData.find(st => removeDiacritics(st.stop_name) === query) || stationsData.find(st => removeDiacritics(st.stop_name).includes(query));
        if (match) {
            fromId = match.stop_id;
            fromIdInput.value = match.stop_id;
            fromInput.value = match.stop_name;
        }
    }

    if (!toId && toInput.value.trim()) {
        const query = removeDiacritics(toInput.value.trim());
        const match = stationsData.find(st => removeDiacritics(st.stop_name) === query) || stationsData.find(st => removeDiacritics(st.stop_name).includes(query));
        if (match) {
            toId = match.stop_id;
            toIdInput.value = match.stop_id;
            toInput.value = match.stop_name;
        }
    }

    if (!fromId || !toId) {
        resultsContainer.innerHTML = '<p style="text-align: center; color: #ef4444; padding: 10px;">Please select valid origin and destination stations.</p>';
        return;
    }

    const fromName = fromInput.value.trim();
    const toName = toInput.value.trim();
    saveRecentPair(fromId, fromName, toId, toName);

    resultsContainer.innerHTML = '<p style="text-align: center; color: #a0aec0; padding: 10px;">Searching routes...</p>';

    try {
        const resp = await fetch(`/api/routes/plan?from=${fromId}&to=${toId}&time=${Math.floor(currentTimeSec)}`);
        const routes = await resp.json();

        if (routes.length === 0) {
            resultsContainer.innerHTML = '<p style="text-align: center; color: #a0aec0; padding: 10px;">No direct or 1-transfer scheduled trains found for this route.</p>';
            return;
        }

        let rowsHTML = '';
        routes.forEach(r => {
            const delayStr = r.delay_minutes > 0
                ? `<span style="color: #ef4444; font-weight: bold;">+${r.delay_minutes} min delay</span>`
                : `<span style="color: #10b981;">On Time</span>`;

            const isNext = r.is_nearest_future;
            const rowClass = isNext ? 'route-plan-row next-departure-row' : 'route-plan-row';
            const rowStyle = isNext
                ? 'background: rgba(0, 102, 204, 0.25); border-left: 4px solid #00e5ff; border-bottom: 1px solid #2a313d;'
                : 'border-bottom: 1px solid #2a313d;';

            const badgeStr = isNext ? '<span style="background: #00e5ff; color: #0f1217; font-size: 10px; font-weight: bold; padding: 2px 6px; border-radius: 4px; margin-left: 6px;">NEXT DEPARTURE</span>' : '';
            const transferBadge = r.is_transfer ? `<span style="background: #f59e0b; color: #0f1217; font-size: 10px; font-weight: bold; padding: 2px 6px; border-radius: 4px; margin-left: 4px;">1 TRANSFER (${escapeHTML(r.transfer_station_name)})</span>` : '';

            let disruptionBadgesHTML = '';
            if (r.is_bus_replacement) {
                disruptionBadgesHTML += `<span class="notice-badge notice-badge-bus clickable-notice-badge" data-notice-id="${escapeHTML(r.notice_id || '')}" style="font-size: 9px; padding: 1px 4px; margin-left: 4px;">🚌 Bus</span> `;
            }
            if (r.is_track_works) {
                disruptionBadgesHTML += `<span class="notice-badge notice-badge-works clickable-notice-badge" data-notice-id="${escapeHTML(r.notice_id || '')}" style="font-size: 9px; padding: 1px 4px; margin-left: 4px;">🚧 Works</span>`;
            }

            let disruptionNoticeHTML = '';
            if (r.affected_by_disruption) {
                disruptionNoticeHTML = `<div style="font-size: 11px; color: #f59e0b; margin-top: 4px;">⚠️ <strong>Disruption Notice:</strong> ${escapeHTML(r.disruption_notice || 'Track works or bus replacement on route')}</div>`;
            }

            const depTimeHM = stripSeconds(r.departure_time);
            const arrTimeHM = stripSeconds(r.arrival_time);

            rowsHTML += `
                <tr style="${rowStyle}" class="${rowClass}">
                    <td style="padding: 10px;">🕒 ${depTimeHM} ➔ ${arrTimeHM} <span style="color: #a0aec0; font-size: 11px;">(${r.duration_minutes} min)</span></td>
                    <td style="padding: 10px;"><a href="#" class="train-link" data-train="${escapeHTML(r.train_number)}" style="color: #00e5ff; font-weight: bold; text-decoration: underline;">${escapeHTML(r.train_number)}</a> ${disruptionBadgesHTML}${badgeStr}${transferBadge}${disruptionNoticeHTML}</td>
                    <td style="padding: 10px;">${delayStr}</td>
                </tr>
            `;
        });

        const headerRouteTitle = `🚩 ${escapeHTML(fromName)} ➔ 🏁 ${escapeHTML(toName)}`;

        resultsContainer.innerHTML = `
            <div style="background: #1e242e; border: 1px solid #3a4250; border-radius: 6px; padding: 10px 14px; margin-bottom: 10px; font-weight: bold; font-size: 14px; color: #00e5ff;">
                ${headerRouteTitle}
            </div>
            <div style="max-height: 280px; overflow-y: auto;">
                <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
                    <thead>
                        <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                            <th style="padding: 8px;">Departure ➔ Arrival</th>
                            <th style="padding: 8px;">Train</th>
                            <th style="padding: 8px;">Status</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${rowsHTML}
                    </tbody>
                </table>
            </div>
        `;

        const nextRow = resultsContainer.querySelector('.next-departure-row');
        if (nextRow) {
            nextRow.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
        }

        resultsContainer.querySelectorAll('.train-link').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                const trainNum = link.getAttribute('data-train');
                openTrainDetails({ train_number: trainNum });
            });
        });

    } catch (e) {
        resultsContainer.innerHTML = `<p style="color: #ef4444; padding: 10px;">Failed to fetch route plans: ${escapeHTML(e.message)}</p>`;
    }
}

async function loadNotices() {
    try {
        const resp = await fetch('/api/notices');
        noticesData = await resp.json();
        processNoticesMapData();
        renderNoticesModalBody();
    } catch (e) {
        console.error('Failed to load notices:', e);
    }
}

function processNoticesMapData() {
    stationNoticesMap = {};
    segmentNoticesMap = {};

    noticesData.forEach(notice => {
        if (notice.parsed_stations) {
            notice.parsed_stations.forEach(stName => {
                const cleanSt = removeDiacritics(stName);
                if (!stationNoticesMap[cleanSt]) stationNoticesMap[cleanSt] = [];
                stationNoticesMap[cleanSt].push(notice);
            });
        }
    });

    if (segmentsData && segmentsData.length > 0) {
        segmentsData.forEach(seg => {
            const fromNorm = removeDiacritics(seg.from_stop_name);
            const toNorm = removeDiacritics(seg.to_stop_name);

            noticesData.forEach(notice => {
                let match = false;
                if (notice.parsed_stations) {
                    const stNorms = notice.parsed_stations.map(removeDiacritics);
                    if (stNorms.includes(fromNorm) && stNorms.includes(toNorm)) {
                        match = true;
                    }
                }
                if (match) {
                    const segKey = seg.id || `${seg.from_stop_id}->${seg.to_stop_id}`;
                    if (!segmentNoticesMap[segKey]) segmentNoticesMap[segKey] = [];
                    segmentNoticesMap[segKey].push(notice);
                }
            });
        });
    }

    if (stationsData.length > 0) renderStations();
    if (segmentsData.length > 0) renderSegments();
}

async function loadStations() {
    try {
        const resp = await fetch('/api/stations');
        stationsData = await resp.json();
        renderStations();
        fetchActiveTrains();
    } catch (e) {
        console.error('Failed to load stations:', e);
    }
}

async function loadSegments() {
    try {
        const resp = await fetch('/api/segments');
        segmentsData = await resp.json();
        renderSegments();
    } catch (e) {
        console.error('Failed to load segments:', e);
    }
}

function renderStations() {
    Object.keys(stationMarkersMap).forEach(id => {
        map.removeLayer(stationMarkersMap[id]);
    });
    stationMarkersMap = {};

    stationsData.forEach(st => {
        const normName = removeDiacritics(st.stop_name);
        const hasNotices = stationNoticesMap[normName] && stationNoticesMap[normName].length > 0;

        let iconHTML = '';
        let markerClassName = 'station-marker';
        let iconSize = [16, 16];
        let iconAnchor = [8, 8];

        if (hasNotices) {
            markerClassName = 'station-marker station-warning-marker';
            iconHTML = `<div style="font-size: 14px; line-height: 1; text-align: center;">⚠️</div>`;
            iconSize = [22, 22];
            iconAnchor = [11, 11];
        }

        const icon = L.divIcon({
            className: markerClassName,
            html: iconHTML,
            iconSize: iconSize,
            iconAnchor: iconAnchor
        });

        const marker = L.marker([st.lat, st.lon], { icon: icon }).addTo(map);
        const tooltipText = hasNotices ? `⚠️ ${escapeHTML(st.stop_name)} (Track Works / Notice)` : escapeHTML(st.stop_name);
        marker.bindTooltip(tooltipText);

        marker.on('click', () => {
            openStationTimetable(st);
        });

        stationMarkersMap[st.stop_id] = marker;
    });
}

function renderSegments() {
    segmentsLayerGroup.clearLayers();
    segmentBadgesLayerGroup.clearLayers();
    segmentBadgeMarkers = [];

    segmentsData.forEach(seg => {
        const key = seg.id || `${seg.from_stop_id}->${seg.to_stop_id}`;
        let lineCoords = [[seg.from_lat, seg.from_lon], [seg.to_lat, seg.to_lon]];
        if (seg.path && seg.path.length > 0) {
            lineCoords = seg.path.map(pt => [pt.lat, pt.lon]);
            segmentPathsMap[key] = seg.path;
            segmentPathsMap[`${seg.from_stop_id}->${seg.to_stop_id}`] = seg.path;
        } else {
            const fallbackPath = [{ lat: seg.from_lat, lon: seg.from_lon }, { lat: seg.to_lat, lon: seg.to_lon }];
            segmentPathsMap[key] = fallbackPath;
            segmentPathsMap[`${seg.from_stop_id}->${seg.to_stop_id}`] = fallbackPath;
        }

        const hasSegmentDisruption = segmentNoticesMap[key] && segmentNoticesMap[key].length > 0;
        const lineColor = hasSegmentDisruption ? '#f59e0b' : '#3b82f6';
        const lineWeight = hasSegmentDisruption ? 5 : 3;
        const lineOpacity = hasSegmentDisruption ? 0.95 : 0.6;

        const line = L.polyline(lineCoords, {
            color: lineColor,
            weight: lineWeight,
            opacity: lineOpacity,
            dashArray: hasSegmentDisruption ? '8, 6' : null,
            renderer: segmentsCanvasRenderer
        });
        segmentsLayerGroup.addLayer(line);

        const midLat = (seg.from_lat + seg.to_lat) / 2;
        const midLon = (seg.from_lon + seg.to_lon) / 2;

        const speedLabelIcon = L.divIcon({
            className: 'segment-speed-badge',
            html: `<span>⚡ ${Math.round(seg.avg_speed_kmh)} km/h</span>`,
            iconSize: [80, 22],
            iconAnchor: [40, 11]
        });

        const labelMarker = L.marker([midLat, midLon], { icon: speedLabelIcon });
        segmentBadgeMarkers.push(labelMarker);

        const clickHandler = () => {
            openSegmentDetails(seg);
        };

        line.on('click', clickHandler);
        labelMarker.on('click', clickHandler);

        segmentLinesMap[key] = { line, labelMarker };
    });

    updateSegmentVisibility();
}

function mapPopupOptions() {
    const isMobile = window.innerWidth <= 768;
    return {
        maxWidth: Math.min(360, window.innerWidth - 40),
        autoPanPaddingTopLeft: L.point(isMobile ? 10 : 180, 20),
        autoPanPaddingBottomRight: L.point(20, isMobile ? 20 : 90),
    };
}

async function openSegmentDetails(seg) {
    const midLat = (seg.from_lat + seg.to_lat) / 2;
    const midLon = (seg.from_lon + seg.to_lon) / 2;

    const popup = L.popup(mapPopupOptions())
        .setLatLng([midLat, midLon])
        .setContent(`
            <div style="width: min(300px, calc(100vw - 80px)); padding: 10px;">
                <h3 style="margin-bottom: 4px; color: #3b82f6;">📍 ${escapeHTML(seg.from_stop_name)} ➔ ${escapeHTML(seg.to_stop_name)}</h3>
                <p style="color: #a0aec0; font-size: 12px;"><em>Loading scheduled train details...</em></p>
            </div>
        `)
        .openOn(map);

    try {
        const resp = await fetch(`/api/segments/details?from=${encodeURIComponent(seg.from_stop_id)}&to=${encodeURIComponent(seg.to_stop_id)}`);
        const details = await resp.json();

        let trainRowsHTML = '';
        if (details.trains && details.trains.length > 0) {
            details.trains.forEach(tr => {
                const depTime = formatSecondsToTime(tr.scheduled_dep_sec);
                const arrTime = formatSecondsToTime(tr.scheduled_arr_sec);
                trainRowsHTML += `
                    <tr style="border-bottom: 1px solid #2a313d;">
                        <td style="padding: 6px 4px;"><strong>${escapeHTML(tr.train_number)}</strong></td>
                        <td style="padding: 6px 4px;">${depTime} - ${arrTime}</td>
                        <td style="padding: 6px 4px; color: #10b981; font-weight: bold;">${Math.round(tr.speed_kmh)} km/h</td>
                    </tr>
                `;
            });
        } else {
            trainRowsHTML = `<tr><td colspan="3" style="padding: 8px; text-align: center; color: #a0aec0;">No train data for segment.</td></tr>`;
        }

        const popupHTML = `
            <div style="max-height: 280px; overflow-y: auto; width: min(300px, calc(100vw - 80px));">
                <h3 style="margin-bottom: 4px; color: #3b82f6;">📍 ${escapeHTML(seg.from_stop_name)} ➔ ${escapeHTML(seg.to_stop_name)}</h3>
                <div style="font-size: 12px; color: #a0aec0; margin-bottom: 10px;">
                    Distance: <strong>${seg.distance_km.toFixed(1)} km</strong> | Avg Speed: <strong style="color: #10b981;">${Math.round(seg.avg_speed_kmh)} km/h</strong> (${seg.train_count} trains)
                </div>
                <table style="width: 100%; border-collapse: collapse; font-size: 12px;">
                    <thead>
                        <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                            <th style="padding: 4px;">Train</th>
                            <th style="padding: 4px;">Dep - Arr</th>
                            <th style="padding: 4px;">Speed</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${trainRowsHTML}
                    </tbody>
                </table>
            </div>
        `;

        popup.setContent(popupHTML);
    } catch (e) {
        popup.setContent(`
            <div style="width: min(300px, calc(100vw - 80px)); padding: 10px; color: #ef4444;">
                Failed to load segment details: ${escapeHTML(e.message)}
            </div>
        `);
    }
}

function gtfsTimeToSeconds(timeStr) {
    if (!timeStr) return null;
    const parts = timeStr.split(':').map(Number);
    if (parts.length < 2 || parts.some(isNaN)) return null;
    return parts[0] * 3600 + parts[1] * 60 + (parts[2] || 0);
}

// Index of the next train due at the station (expected departure incl. delay, allowing
// trains that left up to 2 minutes ago); falls back to the most recent one.
function findNearestTimetableEntryIndex(entries, nowSec) {
    let bestIdx = -1;
    let bestDiff = Infinity;
    let latestIdx = -1;
    let latestDiff = -Infinity;
    entries.forEach((e, idx) => {
        const depSec = gtfsTimeToSeconds(e.departure_time || e.arrival_time);
        if (depSec === null) return;
        const delaySec = (typeof e.delay_minutes === 'number' ? e.delay_minutes : 0) * 60;
        // Signed difference wrapped into [-12h, 12h) so times past midnight (e.g. 24:15) compare correctly.
        const diff = ((depSec + delaySec - nowSec) % 86400 + 86400 + 43200) % 86400 - 43200;
        if (diff >= -120 && diff < bestDiff) {
            bestDiff = diff;
            bestIdx = idx;
        }
        if (diff > latestDiff) {
            latestDiff = diff;
            latestIdx = idx;
        }
    });
    return bestIdx >= 0 ? bestIdx : latestIdx;
}

function scrollTimetableToNearestRow(popupNode) {
    const container = popupNode.querySelector('.timetable-popup-container');
    const row = popupNode.querySelector('.timetable-nearest-row');
    if (!container || !row) return;
    const header = container.querySelector('thead');
    const headerHeight = header ? header.offsetHeight : 0;
    const rowTop = row.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop;
    // Keep the previous train visible above the highlighted one.
    const prevRow = row.previousElementSibling;
    const prevHeight = prevRow ? prevRow.offsetHeight : 0;
    container.scrollTop = Math.max(0, rowTop - headerHeight - prevHeight);
}

async function openStationTimetable(station) {
    highlightSelectedStation(station);
    try {
        const resp = await fetch(`/api/stations/${encodeURIComponent(station.stop_id)}/timetable`);
        const entries = await resp.json();

        let timetableHTML = `<div style="max-height: 280px; overflow-y: auto; width: min(340px, calc(100vw - 80px));" class="timetable-popup-container">
            <h3 style="margin-bottom: 8px; color: #00e5ff;">🚉 ${escapeHTML(station.stop_name)}</h3>
            <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
                <thead>
                    <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                        <th style="padding: 4px;">Departure</th>
                        <th style="padding: 4px;">Train</th>
                        <th style="padding: 4px;">Destination</th>
                        <th style="padding: 4px;">Status</th>
                    </tr>
                </thead>
                <tbody>`;

        const nearestIdx = findNearestTimetableEntryIndex(entries, currentTimeSec);

        if (entries.length === 0) {
            timetableHTML += `<tr><td colspan="4" style="padding: 8px; text-align: center; color: #a0aec0;">No scheduled trains found.</td></tr>`;
        } else {
            entries.forEach((e, idx) => {
                const delayVal = typeof e.delay_minutes === 'number' ? e.delay_minutes : 0;
                const delayStr = delayVal > 0 ? `<span style="color: #ef4444;">+${delayVal}m</span>` : `<span style="color: #10b981;">On Time</span>`;
                const dest = e.destination_station_name || e.headsign || 'N/A';
                const depHM = stripSeconds(e.departure_time);

                let badgesHTML = '';
                if (e.is_bus_replacement) {
                    badgesHTML += `<span class="notice-badge notice-badge-bus clickable-notice-badge" data-notice-id="${escapeHTML(e.notice_id || '')}" style="font-size: 9px; padding: 1px 4px;">🚌 Bus</span> `;
                }
                if (e.is_track_works) {
                    badgesHTML += `<span class="notice-badge notice-badge-works clickable-notice-badge" data-notice-id="${escapeHTML(e.notice_id || '')}" style="font-size: 9px; padding: 1px 4px;">🚧 Works</span>`;
                }

                const rowClass = idx === nearestIdx ? 'timetable-row timetable-nearest-row' : 'timetable-row';
                timetableHTML += `<tr style="border-bottom: 1px solid #2a313d; cursor: pointer;" class="${rowClass}" data-train="${escapeHTML(e.train_number)}">
                    <td style="padding: 6px 4px;">🕒 ${depHM}</td>
                    <td style="padding: 6px 4px;"><strong>${escapeHTML(e.train_number)}</strong> ${badgesHTML}</td>
                    <td style="padding: 6px 4px; color: #e2e8f0; font-weight: 500;">🏁 ${escapeHTML(dest)}</td>
                    <td style="padding: 6px 4px;">${delayStr}</td>
                </tr>`;
            });
        }

        timetableHTML += `</tbody></table></div>`;

        const popup = L.popup(mapPopupOptions())
            .setLatLng([station.lat, station.lon])
            .setContent(timetableHTML)
            .openOn(map);

        // openOn() adds the popup synchronously, so its DOM is ready here.
        const popupNode = popup.getElement();
        if (popupNode) {
            popupNode.querySelectorAll('.clickable-notice-badge').forEach(badge => {
                badge.addEventListener('click', (evt) => {
                    evt.stopPropagation();
                    const noticeId = badge.getAttribute('data-notice-id');
                    openNoticesModal(noticeId);
                });
            });

            popupNode.querySelectorAll('.timetable-row').forEach(row => {
                row.addEventListener('click', (evt) => {
                    if (evt.target.classList.contains('clickable-notice-badge')) return;
                    const trNum = row.getAttribute('data-train');
                    openTrainDetails({ train_number: trNum });
                });
            });

            scrollTimetableToNearestRow(popupNode);
        }

    } catch (err) {
        console.error('Failed to load station timetable:', err);
    }
}

async function fetchActiveTrains() {
    const seq = ++activeTrainsFetchSeq;
    try {
        const resp = await fetch(`/api/active-trains?time=${formatSecondsToTime(currentTimeSec)}`);
        const data = await resp.json();
        if (seq !== activeTrainsFetchSeq) return;
        activeTrainsData = data;
        updateTrainMarkers();
        updateOpenTrainDetailsIfActive();
        if (!startupRouteHandled) {
            checkStartupRouteSelection();
        }
    } catch (e) {
        console.error('Failed to fetch active trains:', e);
    }
}

function updateOpenTrainDetailsIfActive() {
    if (!currentOpenedTrainNum) return;
    const train = activeTrainsData.find(t => t.train_number === currentOpenedTrainNum || t.trip_id === currentOpenedTrainNum);
    if (train) {
        updateTrainDetailsPanel(train, false);
    }
}

function showLocateTrainButton() {
    const btn = document.getElementById('locate-train-btn');
    if (btn) btn.style.display = 'inline-flex';
}

function locateTrainOnMap(trNum) {
    if (!trNum) return;
    const train = activeTrainsData.find(t => t.train_number === trNum || t.trip_id === trNum);
    const marker = train && trainMarkersMap[train.trip_id];
    if (!marker) {
        showToast(`🚆 Train ${trNum}`, 'This train is not running on the map right now.');
        return;
    }

    selectedTrainNum = trNum;
    updateTrainMarkersClientSide();
    map.setView(marker.getLatLng(), Math.max(map.getZoom(), 12));

    const el = marker.getElement();
    if (el) {
        el.classList.remove('locate-flash');
        void el.offsetWidth; // restart the animation on repeated clicks
        el.classList.add('locate-flash');
        setTimeout(() => el.classList.remove('locate-flash'), 2000);
    }
}

function updateTrainMarkers() {
    updateTrainMarkersClientSide();
}

function openTrainDetails(train) {
    const trNum = train.train_number || train.trip_id;
    currentOpenedTrainNum = trNum;
    selectedTrainNum = trNum;

    clearSelectedStationHighlight();

    drawSelectedTrainRoute(trNum);
    startAcceleratedPollingForSelectedTrain(trNum);

    const activeTr = activeTrainsData.find(t => t.train_number === trNum || t.trip_id === trNum);
    if (activeTr) {
        if (activeTr.lat && activeTr.lon) {
            map.panTo([activeTr.lat, activeTr.lon]);
        }
        updateTrainDetailsPanel(activeTr, true);
    } else {
        fetchAndDisplayTrainDetails(trNum);
    }

    updateTrainMarkersClientSide();
}

async function fetchAndDisplayTrainDetails(trainNum) {
    const panel = document.getElementById('side-panel');
    const content = document.getElementById('panel-content');
    const titleEl = document.getElementById('side-panel-title');
    const badgeEl = document.getElementById('side-panel-badge');

    if (titleEl) titleEl.textContent = `🚆 Train ${trainNum}`;
    if (badgeEl) badgeEl.innerHTML = '';
    showLocateTrainButton();

    panel.style.display = 'block';
    bringOverlayToFront(panel);
    content.innerHTML = `<p style="color: #a0aec0; text-align: center; padding: 20px;">Loading details for Train ${escapeHTML(trainNum)}...</p>`;

    try {
        const resp = await fetch(`/api/trains/${encodeURIComponent(trainNum)}`);
        if (!resp.ok) {
            content.innerHTML = `<p style="color: #ef4444; text-align: center; padding: 20px;">Train ${escapeHTML(trainNum)} not found.</p>`;
            return;
        }

        const data = await resp.json();
        if (data.delay_distribution) {
            trainDelayDistributions[data.train_number] = data.delay_distribution;
        }
        if (data.recent_delays) {
            trainRecentDelays[data.train_number] = data.recent_delays;
        }

        let statusStr = "Scheduled Train";
        let statusBadgeClass = "badge-on-time";

        let disruptionBadgeHTML = '';
        if (data.is_bus_replacement) {
            disruptionBadgeHTML += `<span class="notice-badge notice-badge-bus clickable-notice-badge" data-notice-id="${escapeHTML(data.notice_id || '')}" style="margin-left: 6px;">🚌 Bus replacement</span>`;
        }
        if (data.is_track_works) {
            disruptionBadgeHTML += `<span class="notice-badge notice-badge-works clickable-notice-badge" data-notice-id="${escapeHTML(data.notice_id || '')}" style="margin-left: 6px;">🚧 Track works</span>`;
        }

        if (data.is_active) {
            statusStr = "Currently Active on Route";
            statusBadgeClass = "badge-on-time";
        } else if (data.position_status && data.position_status.toLowerCase() === "arrived") {
            statusStr = "Completed Journey";
            statusBadgeClass = "badge-on-time";
        } else {
            const depSec = data.first_departure_seconds;
            const arrSec = data.last_arrival_seconds;
            if (currentTimeSec < depSec) {
                const depHM = stripSeconds(data.first_departure_time);
                statusStr = `Scheduled (Departs at ${depHM})`;
            } else if (currentTimeSec > arrSec) {
                const arrHM = stripSeconds(data.last_arrival_time);
                statusStr = `Completed (Arrived at ${arrHM})`;
            } else {
                statusStr = "Not running at selected time";
            }
        }

        const delayBadge = data.delay_minutes > 0
            ? `<span class="badge badge-delay">+${data.delay_minutes} min delay</span>`
            : `<span class="badge ${statusBadgeClass}">${escapeHTML(statusStr)}</span>`;

        if (titleEl) titleEl.textContent = `🚆 Train ${data.train_number}`;
        if (badgeEl) badgeEl.innerHTML = delayBadge + disruptionBadgeHTML;

        const firstTimeStr = data.first_departure_time ? ` (${stripSeconds(data.first_departure_time)})` : '';
        const lastTimeStr = data.last_arrival_time ? ` (${stripSeconds(data.last_arrival_time)})` : '';

        content.innerHTML = `
            <div class="detail-row">
                <div class="detail-label">First Station (Origin)</div>
                <div class="detail-value">🚩 ${escapeHTML(data.first_station_name)}${firstTimeStr}</div>
            </div>
            <div class="detail-row">
                <div class="detail-label">Last Station (Destination)</div>
                <div class="detail-value">🏁 ${escapeHTML(data.last_station_name)}${lastTimeStr}</div>
            </div>
            <div class="detail-row">
                <div class="detail-label">Scheduled Journey Time</div>
                <div class="detail-value">🕒 ${stripSeconds(data.first_departure_time)} ➔ ${stripSeconds(data.last_arrival_time)}</div>
            </div>
            <div class="detail-row">
                <div class="detail-label">Current Status</div>
                <div class="detail-value" style="color: ${data.is_active ? '#10b981' : '#a0aec0'}; font-weight: bold;">
                    ${escapeHTML(statusStr)}
                </div>
            </div>
            <div class="detail-row">
                <div class="detail-label">Delay Status</div>
                <div class="detail-value">${data.delay_minutes > 0 ? `<span style="color:#ef4444; font-weight:bold;">Running with +${data.delay_minutes} min delay</span>` : '<span style="color:#10b981; font-weight:bold;">On Time / No Delay</span>'}</div>
            </div>
            <button class="refresh-delay-btn" id="check-delay-btn">📡 Refresh Live Delay Status</button>
            <div id="live-delay-result" style="margin-top: 10px; font-size: 13px;"></div>
            <div id="delay-distribution-section">${renderDelayDistributionHTML(data.delay_distribution)}</div>
            <div id="recent-delays-section">${renderRecentDelaysHTML(data.recent_delays)}</div>
        `;

        if (data.is_active && data.lat && data.lon) {
            map.panTo([data.lat, data.lon]);
        }

        document.getElementById('check-delay-btn').addEventListener('click', async () => {
            const resultDiv = document.getElementById('live-delay-result');
            resultDiv.innerHTML = '<em>Fetching live status from hzpp.app...</em>';
            try {
                const resp = await fetch(`/api/train-delay?trainId=${encodeURIComponent(data.train_number)}`);
                const liveData = await resp.json();
                if (liveData.success && liveData.data) {
                    const live = liveData.data;
                    const liveDelayMins = live.delayMinutes || 0;
                    const liveDelayStr = liveDelayMins > 0 ? `+${liveDelayMins} min` : 'On Time / No Delay';

                    resultDiv.innerHTML = `
                        <div style="background: #1e242e; padding: 10px; border-radius: 6px; border: 1px solid #3a4250;">
                            <div><strong>Status:</strong> ${escapeHTML(live.positionStatus || 'Unknown')}</div>
                            <div><strong>Live Delay:</strong> <span style="color:${liveDelayMins > 0 ? '#ef4444' : '#10b981'}">${escapeHTML(liveDelayStr)}</span></div>
                            <div><strong>Last Station:</strong> ${escapeHTML(live.lastStation || 'N/A')}</div>
                            <div><strong>Next Station:</strong> ${escapeHTML(live.nextStation || 'N/A')}</div>
                        </div>
                    `;
                } else {
                    resultDiv.innerHTML = '<span style="color: #ef4444;">No live status available for this train.</span>';
                }
            } catch (e) {
                resultDiv.innerHTML = `<span style="color: #ef4444;">Failed to fetch live delay: ${escapeHTML(e.message)}</span>`;
            }
        });

    } catch (err) {
        content.innerHTML = `<p style="color: #ef4444; text-align: center; padding: 20px;">Failed to load train details: ${escapeHTML(err.message)}</p>`;
    }
}

function formatCleanDate(dateStr) {
    if (!dateStr) return '';
    const clean = dateStr.split('T')[0];
    const parts = clean.split('-');
    if (parts.length === 3) {
        return `${parts[1]}-${parts[2]}`; // MM-DD omitting year
    }
    return clean;
}

function safeCSSId(str) {
    if (!str) return '';
    return str.replace(/[^a-zA-Z0-9_-]/g, '_');
}

let expandedDelayDateGroups = new Set();

window.toggleDelayDateGroup = function(dateKey) {
    const safeKey = safeCSSId(dateKey);
    const rows = document.querySelectorAll(`.delay-subrow-${safeKey}`);
    const toggleBtn = document.getElementById(`toggle-date-${safeKey}`);
    if (!rows.length) return;

    const isHidden = rows[0].style.display === 'none';
    if (isHidden) {
        expandedDelayDateGroups.add(dateKey);
    } else {
        expandedDelayDateGroups.delete(dateKey);
    }

    rows.forEach(r => {
        r.style.display = isHidden ? 'table-row' : 'none';
    });
    if (toggleBtn) {
        toggleBtn.textContent = isHidden ? '▴' : '▾';
    }
};

function renderRecentDelaysHTML(logs) {
    if (logs === undefined || logs === null) {
        return `
            <div class="delay-dist-container" style="margin-top: 12px;">
                <div class="delay-dist-title">📅 Delay Logs (Last 3 Days)</div>
                <div class="delay-dist-empty">Loading delay logs...</div>
            </div>
        `;
    }

    if (!Array.isArray(logs) || logs.length === 0) {
        return `
            <div class="delay-dist-container" style="margin-top: 12px;">
                <div class="delay-dist-title">📅 Delay Logs (Last 3 Days)</div>
                <div class="delay-dist-empty">No delay logs recorded in the last 3 days</div>
            </div>
        `;
    }

    // Group logs by raw date (YYYY-MM-DD)
    const dateGroups = {};
    const dateOrder = [];

    logs.forEach(l => {
        const rawDate = l.delay_date ? l.delay_date.split('T')[0] : '';
        if (!dateGroups[rawDate]) {
            dateGroups[rawDate] = [];
            dateOrder.push(rawDate);
        }
        dateGroups[rawDate].push(l);
    });

    let rowsHTML = '';
    dateOrder.forEach(rawDate => {
        const dayLogs = dateGroups[rawDate];
        const primary = dayLogs[0];
        const hasSub = dayLogs.length > 1;
        const isExpanded = expandedDelayDateGroups.has(rawDate);
        const cleanDate = formatCleanDate(rawDate);
        const safeKey = safeCSSId(rawDate);

        const delayMins = primary.delay_minutes || 0;
        const color = delayMins > 0 ? '#ef4444' : '#10b981';
        const delayText = delayMins > 0 ? `+${delayMins} min late` : 'On Time';
        let primaryTime = primary.updated_at ? String(primary.updated_at).trim() : '';
        if (primaryTime.includes('T')) primaryTime = primaryTime.split('T')[1];
        if (primaryTime.includes(' ')) primaryTime = primaryTime.split(' ')[1];
        const timePart = primaryTime ? ` ${escapeHTML(stripSeconds(primaryTime))}` : '';

        const arrowSymbol = isExpanded ? '▴' : '▾';

        const dateCellHTML = hasSub ? `
            <span onclick="toggleDelayDateGroup('${escapeHTML(rawDate)}')" style="cursor: pointer; text-decoration: underline; color: #00e5ff; font-weight: bold;" title="Click to view all ${dayLogs.length} logs for this date">
                ${escapeHTML(cleanDate)}${timePart} <span id="toggle-date-${escapeHTML(safeKey)}" style="font-size: 11px;">${arrowSymbol}</span>
            </span>
        ` : `${escapeHTML(cleanDate)}${timePart}`;

        rowsHTML += `
            <tr style="border-bottom: 1px solid #2a313d;">
                <td style="padding: 6px 4px; color: #8b949e;">${dateCellHTML}</td>
                <td style="padding: 6px 4px; color: ${color}; font-weight: bold;">${escapeHTML(delayText)}</td>
                <td style="padding: 6px 4px; color: #d2a8ff; font-size: 11px;">${escapeHTML(primary.last_station || primary.position_status || 'In Transit')}</td>
            </tr>
        `;

        if (hasSub) {
            const subDisplayStyle = isExpanded ? 'table-row' : 'none';
            for (let i = 1; i < dayLogs.length; i++) {
                const sub = dayLogs[i];
                const subDelayMins = sub.delay_minutes || 0;
                const subColor = subDelayMins > 0 ? '#ef4444' : '#10b981';
                const subDelayText = subDelayMins > 0 ? `+${subDelayMins} min late` : 'On Time';
                let subTime = sub.updated_at ? String(sub.updated_at).trim() : '';
                if (subTime.includes('T')) subTime = subTime.split('T')[1];
                if (subTime.includes(' ')) subTime = subTime.split(' ')[1];
                const subTimePart = subTime ? escapeHTML(stripSeconds(subTime)) : '';

                rowsHTML += `
                    <tr class="delay-subrow-${escapeHTML(safeKey)}" style="display: ${subDisplayStyle}; background: #161b22; border-bottom: 1px dashed #2a313d;">
                        <td style="padding: 4px 4px 4px 16px; color: #6e7681; font-size: 11px;">↳ ${subTimePart}</td>
                        <td style="padding: 4px; color: ${subColor}; font-size: 11px; font-weight: bold;">${escapeHTML(subDelayText)}</td>
                        <td style="padding: 4px; color: #a5d6ff; font-size: 11px;">${escapeHTML(sub.last_station || sub.position_status || 'In Transit')}</td>
                    </tr>
                `;
            }
        }
    });

    return `
        <div class="delay-dist-container" style="margin-top: 12px;">
            <div class="delay-dist-title">
                <span>📅 Delay Logs (Last 3 Days)</span>
                <span style="font-size: 10px; color: #8b949e;">(${dateOrder.length} days recorded)</span>
            </div>
            <div class="delay-logs-scroll" style="max-height: 180px; overflow-y: auto; margin-top: 8px;">
                <table style="width: 100%; border-collapse: collapse; font-size: 12px;">
                    <thead>
                        <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #8b949e;">
                            <th style="padding: 4px;">Date / Time</th>
                            <th style="padding: 4px;">Delay</th>
                            <th style="padding: 4px;">Location</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${rowsHTML}
                    </tbody>
                </table>
            </div>
        </div>
    `;
}

function renderDelayDistributionHTML(dist) {
    if (!dist || !dist.total_records || dist.total_records === 0) {
        return `
            <div class="delay-dist-container">
                <div class="delay-dist-title">📊 Historical Punctuality Breakdown</div>
                <div class="delay-dist-empty">No historical delay data available yet</div>
            </div>
        `;
    }

    const items = [
        { label: 'On time (0 min delay)', pct: dist.ontime_percent || 0, color: '#10b981' },
        { label: '1–5 min late', pct: dist.late_1_to_5_percent || 0, color: '#38bdf8' },
        { label: '5–10 min late', pct: dist.late_5_to_10_percent || 0, color: '#f59e0b' },
        { label: '10–30 min late', pct: dist.late_10_to_30_percent || 0, color: '#f97316' },
        { label: '>30 min late', pct: dist.late_over_30_percent || 0, color: '#ef4444' }
    ];

    let itemsHTML = '';
    items.forEach(it => {
        const pctFormatted = it.pct.toFixed(1);
        itemsHTML += `
            <div class="delay-dist-item">
                <div class="delay-dist-header">
                    <span>${escapeHTML(it.label)}</span>
                    <strong style="color: ${it.color};">${pctFormatted}%</strong>
                </div>
                <div class="delay-dist-track">
                    <div class="delay-dist-fill" style="width: ${pctFormatted}%; background: ${it.color};"></div>
                </div>
            </div>
        `;
    });

    return `
        <div class="delay-dist-container">
            <div class="delay-dist-title">
                <span>📊 Historical Punctuality Breakdown</span>
                <span style="font-size: 10px; color: #8b949e;">(${dist.total_records} logs)</span>
            </div>
            ${itemsHTML}
        </div>
    `;
}

function updateTrainDetailsPanel(train, isNewOpen = false) {
    const panel = document.getElementById('side-panel');
    const content = document.getElementById('panel-content');
    const titleEl = document.getElementById('side-panel-title');
    const badgeEl = document.getElementById('side-panel-badge');

    const trNum = train.train_number || train.trip_id;

    if (!train.delay_distribution && trainDelayDistributions[trNum]) {
        train.delay_distribution = trainDelayDistributions[trNum];
    } else if (train.delay_distribution) {
        trainDelayDistributions[trNum] = train.delay_distribution;
    }

    if (!train.recent_delays && trainRecentDelays[trNum]) {
        train.recent_delays = trainRecentDelays[trNum];
    } else if (train.recent_delays) {
        trainRecentDelays[trNum] = train.recent_delays;
    }

        let activeDisruptionsHTML = '';
        if (train.is_bus_replacement) {
            activeDisruptionsHTML += `<span class="notice-badge notice-badge-bus clickable-notice-badge" data-notice-id="${escapeHTML(train.notice_id || '')}" style="margin-left: 6px;">🚌 Bus replacement</span>`;
        }
        if (train.is_track_works) {
            activeDisruptionsHTML += `<span class="notice-badge notice-badge-works clickable-notice-badge" data-notice-id="${escapeHTML(train.notice_id || '')}" style="margin-left: 6px;">🚧 Track works</span>`;
        }

    const delayBadge = train.delay_minutes > 0
        ? `<span class="badge badge-delay">+${train.delay_minutes} min delay</span>`
        : `<span class="badge badge-on-time">Active on Route</span>`;

    if (titleEl) titleEl.textContent = `🚆 Train ${train.train_number}`;
    showLocateTrainButton();
        if (badgeEl) badgeEl.innerHTML = delayBadge + activeDisruptionsHTML;

    const originStation = escapeHTML(train.first_station_name || 'Origin Station');
    const destStation = escapeHTML(train.last_station_name || 'Destination Station');
    const firstTimeStr = train.first_departure_time ? ` (${stripSeconds(train.first_departure_time)})` : '';
    const lastTimeStr = train.last_arrival_time ? ` (${stripSeconds(train.last_arrival_time)})` : '';

    let liveResultHTML = '';
    const existingResultDiv = document.getElementById('live-delay-result');
    if (!isNewOpen && existingResultDiv) {
        liveResultHTML = existingResultDiv.innerHTML;
    }

    const prevScrollTop = content.scrollTop;
    const prevLogScroll = content.querySelector('.delay-logs-scroll');
    const prevLogScrollTop = prevLogScroll ? prevLogScroll.scrollTop : 0;

    content.innerHTML = `
        <div class="detail-row">
            <div class="detail-label">First Station (Origin)</div>
            <div class="detail-value">🚩 ${originStation}${firstTimeStr}</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Last Station (Destination)</div>
            <div class="detail-value">🏁 ${destStation}${lastTimeStr}</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Current Segment</div>
            <div class="detail-value">${escapeHTML(train.prev_station_name)} ➔ ${escapeHTML(train.next_station_name)}</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Progress</div>
            <div class="detail-value">${Math.round(train.progress * 100)}%</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Delay Status</div>
            <div class="detail-value">${train.delay_minutes > 0 ? `<span style="color:#ef4444; font-weight:bold;">Running with +${train.delay_minutes} min delay (position auto-adjusted on map)</span>` : '<span style="color:#10b981; font-weight:bold;">On Time</span>'}</div>
        </div>
        <button class="refresh-delay-btn" id="check-delay-btn">📡 Refresh Live Delay Status</button>
        <div id="live-delay-result" style="margin-top: 10px; font-size: 13px;">${liveResultHTML}</div>
        <div id="delay-distribution-section">${renderDelayDistributionHTML(train.delay_distribution)}</div>
        <div id="recent-delays-section">${renderRecentDelaysHTML(train.recent_delays)}</div>
    `;

    content.scrollTop = prevScrollTop;
    const newLogScroll = content.querySelector('.delay-logs-scroll');
    if (newLogScroll) {
        newLogScroll.scrollTop = prevLogScrollTop;
    }

    panel.style.display = 'block';
    if (isNewOpen) bringOverlayToFront(panel);

    if (!train.delay_distribution || !train.recent_delays) {
        fetch(`/api/trains/${encodeURIComponent(trNum)}`)
            .then(res => res.json())
            .then(data => {
                if (data) {
                    if (data.delay_distribution) {
                        train.delay_distribution = data.delay_distribution;
                        trainDelayDistributions[trNum] = data.delay_distribution;
                        const distSection = document.getElementById('delay-distribution-section');
                        if (distSection) {
                            distSection.innerHTML = renderDelayDistributionHTML(data.delay_distribution);
                        }
                    }
                    if (data.recent_delays) {
                        train.recent_delays = data.recent_delays;
                        trainRecentDelays[trNum] = data.recent_delays;
                        const recentSection = document.getElementById('recent-delays-section');
                        if (recentSection) {
                            recentSection.innerHTML = renderRecentDelaysHTML(data.recent_delays);
                        }
                    }
                }
            })
            .catch(e => console.error("Failed to load train details:", e));
    }

    document.getElementById('check-delay-btn').addEventListener('click', async () => {
        const resultDiv = document.getElementById('live-delay-result');
        resultDiv.innerHTML = '<em>Fetching live status from hzpp.app...</em>';
        try {
            const resp = await fetch(`/api/train-delay?trainId=${encodeURIComponent(trNum)}`);
            const data = await resp.json();
            if (data.success && data.data) {
                const live = data.data;
                const liveDelayMins = live.delayMinutes || 0;
                const liveDelayStr = liveDelayMins > 0 ? `+${liveDelayMins} min` : 'On Time / No Delay';

                train.delay_minutes = liveDelayMins;

                resultDiv.innerHTML = `
                    <div style="background: #1e242e; padding: 10px; border-radius: 6px; border: 1px solid #3a4250;">
                        <div><strong>Status:</strong> ${escapeHTML(live.positionStatus || 'Unknown')}</div>
                        <div><strong>Live Delay:</strong> <span style="color:${liveDelayMins > 0 ? '#ef4444' : '#10b981'}">${escapeHTML(liveDelayStr)}</span></div>
                        <div><strong>Last Station:</strong> ${escapeHTML(live.lastStation || 'N/A')}</div>
                        <div><strong>Next Station:</strong> ${escapeHTML(live.nextStation || 'N/A')}</div>
                    </div>
                `;

                const badgeEl = panel.querySelector('.panel-header .badge');
                if (badgeEl) {
                    if (liveDelayMins > 0) {
                        badgeEl.className = 'badge badge-delay';
                        badgeEl.textContent = `+${liveDelayMins} min delay`;
                    } else {
                        badgeEl.className = 'badge badge-on-time';
                        badgeEl.textContent = 'Active on Route';
                    }
                }

                fetchActiveTrains();
            } else {
                resultDiv.innerHTML = '<span style="color: #ef4444;">No live status available for this train.</span>';
            }
        } catch (e) {
            resultDiv.innerHTML = `<span style="color: #ef4444;">Failed to fetch live delay: ${escapeHTML(e.message)}</span>`;
        }
    });
}
