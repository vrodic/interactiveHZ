let map;
let stationsData = [];
let activeTrainsData = [];
let segmentsData = [];
let stationMarkersMap = {};
let trainMarkersMap = {};
let segmentLinesMap = {};
let segmentsLayerGroup = L.layerGroup();
let segmentBadgesLayerGroup = L.layerGroup();
let currentOpenedTripId = null;

let currentTimeSec = getCurrentSecondsOfDay();
let isRealtime = true;
let isPlaying = false;
let playbackSpeed = 1;
let lastFetchTime = 0;
let lastAnimTime = performance.now();

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

document.addEventListener('DOMContentLoaded', () => {
    initMap();
    initControls();
    initNavHandlers();
    loadStations();
    loadSegments();
    startUpdateLoop();
});

function initMap() {
    map = L.map('map').setView([45.8, 16.5], 8);

    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
        maxZoom: 19,
        attribution: '&copy; OpenStreetMap contributors'
    }).addTo(map);

    segmentsLayerGroup.addTo(map);
    segmentBadgesLayerGroup.addTo(map);

    map.on('zoomend', updateSegmentVisibility);
}

function updateSegmentVisibility() {
    const zoom = map.getZoom();
    if (zoom < 8) {
        if (map.hasLayer(segmentsLayerGroup)) map.removeLayer(segmentsLayerGroup);
        if (map.hasLayer(segmentBadgesLayerGroup)) map.removeLayer(segmentBadgesLayerGroup);
    } else if (zoom < 10) {
        if (!map.hasLayer(segmentsLayerGroup)) map.addLayer(segmentsLayerGroup);
        if (map.hasLayer(segmentBadgesLayerGroup)) map.removeLayer(segmentBadgesLayerGroup);
    } else {
        if (!map.hasLayer(segmentsLayerGroup)) map.addLayer(segmentsLayerGroup);
        if (!map.hasLayer(segmentBadgesLayerGroup)) map.addLayer(segmentBadgesLayerGroup);
    }
}

function initControls() {
    const timeSlider = document.getElementById('time-slider');
    const timeDisplay = document.getElementById('time-display');
    const playBtn = document.getElementById('play-btn');
    const realtimeBtn = document.getElementById('realtime-btn');
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
        }, 150);
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

    let searchDebounceTimer = null;
    searchInput.addEventListener('input', (e) => {
        const query = e.target.value.trim();
        if (query.length < 2) {
            searchResults.style.display = 'none';
            return;
        }

        if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
        searchDebounceTimer = setTimeout(async () => {
            try {
                const resp = await fetch(`/api/search?q=${encodeURIComponent(query)}`);
                const items = await resp.json();

                searchResults.innerHTML = '';
                if (!items || items.length === 0) {
                    searchResults.innerHTML = '<div class="search-item" style="color: #a0aec0; cursor: default;">No stations or trains found</div>';
                    searchResults.style.display = 'block';
                    return;
                }

                items.forEach(item => {
                    const div = document.createElement('div');
                    div.className = 'search-item';
                    const icon = item.type === 'station' ? '🚉' : '🚆';
                    div.innerHTML = `<strong>${icon} ${item.title}</strong> <span style="font-size: 11px; color: #a0aec0;">(${item.subtitle})</span>`;
                    div.onclick = () => {
                        map.setView([item.lat, item.lon], 12);
                        if (item.type === 'station') {
                            openStationTimetable({ stop_id: item.id, stop_name: item.title, lat: item.lat, lon: item.lon });
                        } else if (item.type === 'train') {
                            const activeTr = activeTrainsData.find(t => t.train_number === item.train_number);
                            if (activeTr) {
                                openTrainDetails(activeTr);
                            } else {
                                updateTrainDetailsPanel({
                                    train_number: item.train_number,
                                    headsign: item.subtitle,
                                    first_station_name: 'Scheduled Route',
                                    last_station_name: 'Scheduled Route',
                                    prev_station_name: 'Scheduled',
                                    next_station_name: 'Scheduled',
                                    progress: 0,
                                    delay_minutes: 0
                                }, true);
                            }
                        }
                        searchResults.style.display = 'none';
                        searchInput.value = '';
                    };
                    searchResults.appendChild(div);
                });

                searchResults.style.display = 'block';
            } catch (err) {
                console.error('Search request failed:', err);
            }
        }, 200);
    });

    document.addEventListener('click', (e) => {
        if (!searchInput.contains(e.target) && !searchResults.contains(e.target)) {
            searchResults.style.display = 'none';
        }
    });

    document.getElementById('close-panel').addEventListener('click', () => {
        document.getElementById('side-panel').style.display = 'none';
        currentOpenedTripId = null;
    });
}

function haversineKm(lat1, lon1, lat2, lon2) {
    const radAvgLat = ((lat1 + lat2) / 2.0) * (Math.PI / 180.0);
    const cosLat = Math.cos(radAvgLat);
    const dx = (lon2 - lon1) * 40000.0 * cosLat / 360.0;
    const dy = (lat2 - lat1) * 40000.0 / 360.0;
    return Math.sqrt(dx * dx + dy * dy);
}

function getPointOnPath(path, progress) {
    if (!path || path.length === 0) return null;
    if (path.length === 1 || progress <= 0) return [path[0].lat, path[0].lon];
    if (progress >= 1) return [path[path.length - 1].lat, path[path.length - 1].lon];

    let totalDist = 0;
    const segs = [];
    for (let i = 0; i < path.length - 1; i++) {
        const d = haversineKm(path[i].lat, path[i].lon, path[i + 1].lat, path[i + 1].lon) || 0.0001;
        segs.push({ p1: path[i], p2: path[i + 1], len: d });
        totalDist += d;
    }

    if (totalDist === 0) return [path[0].lat, path[0].lon];

    const targetDist = progress * totalDist;
    let accum = 0;

    for (const seg of segs) {
        if (accum + seg.len >= targetDist) {
            const segProgress = (targetDist - accum) / seg.len;
            const lat = seg.p1.lat + (seg.p2.lat - seg.p1.lat) * segProgress;
            const lon = seg.p1.lon + (seg.p2.lon - seg.p1.lon) * segProgress;
            return [lat, lon];
        }
        accum += seg.len;
    }

    return [path[path.length - 1].lat, path[path.length - 1].lon];
}

function startUpdateLoop() {
    function animate(now) {
        const dt = (now - lastAnimTime) / 1000;
        lastAnimTime = now;

        if (isRealtime) {
            currentTimeSec = getCurrentSecondsOfDay();
            document.getElementById('time-slider').value = Math.floor(currentTimeSec);
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);

            if (now - lastFetchTime > 10000) { // Fetch active trains from server every 10s in realtime
                lastFetchTime = now;
                fetchActiveTrains();
            }
        } else if (isPlaying) {
            currentTimeSec = (currentTimeSec + dt * playbackSpeed * 2) % 86400;
            document.getElementById('time-slider').value = Math.floor(currentTimeSec);
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);

            if (now - lastFetchTime > 5000) { // Fetch every 5s when playing back
                lastFetchTime = now;
                fetchActiveTrains();
            }
        }

        updateTrainMarkersClientSide();
        requestAnimationFrame(animate);
    }

    lastAnimTime = performance.now();
    requestAnimationFrame(animate);
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
            progress = (effTime - depSec) / duration;
            if (progress < 0) progress = 0;
            if (progress > 1) progress = 1;
        }

        let pos = [train.lat, train.lon];
        if (train.path && train.path.length > 0) {
            const calculatedPos = getPointOnPath(train.path, progress);
            if (calculatedPos) pos = calculatedPos;
        }

        if (trainMarkersMap[train.trip_id]) {
            trainMarkersMap[train.trip_id].setLatLng(pos);
            if (train.delay_minutes > 0) {
                trainMarkersMap[train.trip_id].getElement()?.classList.add('delayed');
            } else {
                trainMarkersMap[train.trip_id].getElement()?.classList.remove('delayed');
            }
        } else {
            const icon = L.divIcon({
                className: `train-marker ${train.delay_minutes > 0 ? 'delayed' : ''}`,
                html: '🚆',
                iconSize: [32, 32],
                iconAnchor: [16, 16]
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

    document.getElementById('nav-active-trains').addEventListener('click', () => {
        openActiveTrainsModal();
    });

    document.getElementById('close-active-trains-modal').addEventListener('click', () => {
        activeTrainsModal.style.display = 'none';
    });

    document.getElementById('nav-dashboard').addEventListener('click', () => {
        openDashboardModal();
    });

    document.getElementById('close-dashboard-modal').addEventListener('click', () => {
        dashboardModal.style.display = 'none';
    });

    document.getElementById('nav-delays-stream').addEventListener('click', () => {
        openDelaysStreamModal();
    });

    document.getElementById('close-delays-stream-modal').addEventListener('click', () => {
        delaysStreamModal.style.display = 'none';
    });

    document.getElementById('nav-routes').addEventListener('click', () => {
        tripPlannerDrawer.style.display = 'flex';
    });

    document.getElementById('close-trip-planner-drawer').addEventListener('click', () => {
        tripPlannerDrawer.style.display = 'none';
    });

    document.getElementById('find-routes-btn').addEventListener('click', () => {
        findRoutePlans();
    });

    // Close modals on clicking overlay background
    [activeTrainsModal, dashboardModal, delaysStreamModal].forEach(modal => {
        modal.addEventListener('click', (e) => {
            if (e.target === modal) {
                modal.style.display = 'none';
            }
        });
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

            if (data.type === 'delay_update') {
                // If the train is currently loaded in activeTrainsData, update its delay
                const tr = activeTrainsData.find(t => t.train_number === data.train_number);
                if (tr) {
                    tr.delay_minutes = data.delay_minutes;
                    updateTrainMarkersClientSide();
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
}

function appendStreamLog(data) {
    const logContainer = document.getElementById('delays-stream-log');
    if (!logContainer) return;

    const div = document.createElement('div');
    div.style.marginBottom = '6px';
    div.style.borderBottom = '1px solid #161b22';
    div.style.paddingBottom = '4px';

    const time = data.timestamp || new Date().toLocaleTimeString();

    if (data.type === 'connected') {
        div.innerHTML = `<span style="color: #10b981;">[${time}]</span> <strong style="color: #58a6ff;">SYSTEM:</strong> ${data.message}`;
    } else if (data.type === 'delay_update') {
        const delayMins = data.delay_minutes || 0;
        const delayColor = delayMins > 0 ? '#f85149' : '#3fb950';
        div.innerHTML = `<span style="color: #8b949e;">[${time}]</span> <strong style="color: #e6edf3;">Train ${data.train_number}:</strong> Delay: <span style="color: ${delayColor}; font-weight: bold;">+${delayMins} min</span> | Status: <span style="color: #d2a8ff;">${data.position_status || 'N/A'}</span> | Last: ${data.last_station || 'N/A'}`;
    } else {
        div.innerHTML = `<span style="color: #8b949e;">[${time}]</span> ${JSON.stringify(data)}`;
    }

    logContainer.prepend(div);
}

function openActiveTrainsModal() {
    const modal = document.getElementById('active-trains-modal');
    const body = document.getElementById('active-trains-modal-body');

    let rowsHTML = '';
    // Deduplicate active trains by train_number in modal
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
                <tr style="border-bottom: 1px solid #2a313d; cursor: pointer;" class="active-train-row" data-trip="${tr.trip_id}">
                    <td style="padding: 10px;"><strong>Train ${tr.train_number}</strong></td>
                    <td style="padding: 10px;">🚩 ${tr.first_station_name || 'N/A'}</td>
                    <td style="padding: 10px;">🏁 ${tr.last_station_name || 'N/A'}</td>
                    <td style="padding: 10px;">${tr.prev_station_name} ➔ ${tr.next_station_name} (${Math.round(tr.progress * 100)}%)</td>
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

    body.querySelectorAll('.active-train-row').forEach(row => {
        row.addEventListener('click', () => {
            const tripId = row.getAttribute('data-trip');
            const tr = activeTrainsData.find(t => t.trip_id === tripId);
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
                        <td style="padding: 8px;"><strong>Train ${tr.train_number}</strong></td>
                        <td style="padding: 8px;">${tr.first_station_name} ➔ ${tr.last_station_name}</td>
                        <td style="padding: 8px; color: #ef4444; font-weight: bold;">+${tr.delay_minutes} min</td>
                    </tr>
                `;
            });
        } else {
            delayedRows = `<tr><td colspan="3" style="padding: 12px; text-align: center; color: #10b981;">No delayed trains currently active! 🎉</td></tr>`;
        }

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
                    <div class="dash-label">Total Stations</div>
                    <div class="dash-value">${stats.total_stations}</div>
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
        body.innerHTML = `<p style="color: #ef4444; padding: 20px;">Failed to load dashboard statistics: ${e.message}</p>`;
    }
}

async function findRoutePlans() {
    const fromId = document.getElementById('route-from-select').value;
    const toId = document.getElementById('route-to-select').value;
    const resultsContainer = document.getElementById('route-results-container');

    if (!fromId || !toId) {
        resultsContainer.innerHTML = '<p style="text-align: center; color: #ef4444; padding: 10px;">Please select both origin and destination stations.</p>';
        return;
    }

    resultsContainer.innerHTML = '<p style="text-align: center; color: #a0aec0; padding: 10px;">Searching routes...</p>';

    try {
        const resp = await fetch(`/api/routes/plan?from=${fromId}&to=${toId}&time=${Math.floor(currentTimeSec)}`);
        const routes = await resp.json();

        if (routes.length === 0) {
            resultsContainer.innerHTML = '<p style="text-align: center; color: #a0aec0; padding: 10px;">No direct scheduled trains found for this route.</p>';
            return;
        }

        let rowsHTML = '';
        routes.forEach(r => {
            const delayStr = r.delay_minutes > 0
                ? `<span style="color: #ef4444; font-weight: bold;">+${r.delay_minutes} min delay</span>`
                : `<span style="color: #10b981;">On Time</span>`;

            const isNext = r.is_nearest_future;
            const rowStyle = isNext
                ? 'background: rgba(0, 102, 204, 0.25); border-left: 4px solid #00e5ff; border-bottom: 1px solid #2a313d;'
                : 'border-bottom: 1px solid #2a313d;';

            const badgeStr = isNext ? '<span style="background: #00e5ff; color: #0f1217; font-size: 10px; font-weight: bold; padding: 2px 6px; border-radius: 4px; margin-left: 6px;">NEXT DEPARTURE</span>' : '';

            rowsHTML += `
                <tr style="${rowStyle} cursor: pointer;" class="route-plan-row" data-depsec="${r.departure_seconds}">
                    <td style="padding: 10px;"><strong>Train ${r.train_number}</strong>${badgeStr}</td>
                    <td style="padding: 10px;">🚩 ${r.origin_station_name}</td>
                    <td style="padding: 10px;">🏁 ${r.destination_station_name}</td>
                    <td style="padding: 10px;">🕒 ${r.departure_time} ➔ ${r.arrival_time} (${r.duration_minutes} min)</td>
                    <td style="padding: 10px;">${delayStr}</td>
                </tr>
            `;
        });

        resultsContainer.innerHTML = `
            <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
                <thead>
                    <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                        <th style="padding: 8px;">Train</th>
                        <th style="padding: 8px;">Origin</th>
                        <th style="padding: 8px;">Destination</th>
                        <th style="padding: 8px;">Departure ➔ Arrival</th>
                        <th style="padding: 8px;">Status</th>
                    </tr>
                </thead>
                <tbody>
                    ${rowsHTML}
                </tbody>
            </table>
        `;

        // Automatically focus time slider to the nearest future departure time if present
        const nearestRoute = routes.find(r => r.is_nearest_future);
        if (nearestRoute) {
            currentTimeSec = nearestRoute.departure_seconds;
            isRealtime = false;
            document.getElementById('realtime-btn').classList.remove('active');
            document.getElementById('time-slider').value = currentTimeSec;
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);
            fetchActiveTrains();
        }

        // Allow clicking any route row to scrub timeline to that train's departure time
        resultsContainer.querySelectorAll('.route-plan-row').forEach(row => {
            row.addEventListener('click', () => {
                const depSec = parseInt(row.getAttribute('data-depsec'), 10);
                if (!isNaN(depSec)) {
                    currentTimeSec = depSec;
                    isRealtime = false;
                    document.getElementById('realtime-btn').classList.remove('active');
                    document.getElementById('time-slider').value = currentTimeSec;
                    document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);
                    fetchActiveTrains();
                }
            });
        });

    } catch (e) {
        resultsContainer.innerHTML = `<p style="color: #ef4444; padding: 10px;">Failed to fetch route plans: ${e.message}</p>`;
    }
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
    const fromSelect = document.getElementById('route-from-select');
    const toSelect = document.getElementById('route-to-select');

    if (fromSelect && toSelect) {
        fromSelect.innerHTML = '<option value="">Select origin station...</option>';
        toSelect.innerHTML = '<option value="">Select destination station...</option>';
    }

    stationsData.forEach(st => {
        const icon = L.divIcon({
            className: 'station-marker',
            iconSize: [16, 16],
            iconAnchor: [8, 8]
        });

        const marker = L.marker([st.lat, st.lon], { icon: icon }).addTo(map);
        marker.bindTooltip(st.stop_name);

        marker.on('click', () => {
            openStationTimetable(st);
        });

        stationMarkersMap[st.stop_id] = marker;

        if (fromSelect && toSelect) {
            const opt1 = document.createElement('option');
            opt1.value = st.stop_id;
            opt1.textContent = st.stop_name;
            fromSelect.appendChild(opt1);

            const opt2 = document.createElement('option');
            opt2.value = st.stop_id;
            opt2.textContent = st.stop_name;
            toSelect.appendChild(opt2);
        }
    });
}

function renderSegments() {
    segmentsLayerGroup.clearLayers();
    segmentBadgesLayerGroup.clearLayers();

    segmentsData.forEach(seg => {
        const key = `${seg.from_stop_id}-${seg.to_stop_id}`;
        const line = L.polyline([[seg.from_lat, seg.from_lon], [seg.to_lat, seg.to_lon]], {
            color: '#3b82f6',
            weight: 3,
            opacity: 0.6
        });
        segmentsLayerGroup.addLayer(line);

        const midLat = (seg.from_lat + seg.to_lat) / 2;
        const midLon = (seg.from_lon + seg.to_lon) / 2;

        const speedLabelIcon = L.divIcon({
            className: 'segment-speed-badge',
            html: `<span>⚡ ${Math.round(seg.avg_speed_kmh)} km/h</span>`,
            iconSize: [60, 20],
            iconAnchor: [30, 10]
        });

        const labelMarker = L.marker([midLat, midLon], { icon: speedLabelIcon });
        segmentBadgesLayerGroup.addLayer(labelMarker);

        const clickHandler = () => {
            openSegmentDetails(seg);
        };

        line.on('click', clickHandler);
        labelMarker.on('click', clickHandler);

        segmentLinesMap[key] = { line, labelMarker };
    });

    updateSegmentVisibility();
}

function openSegmentDetails(seg) {
    let trainRowsHTML = '';
    if (seg.trains && seg.trains.length > 0) {
        seg.trains.forEach(tr => {
            const depTime = formatSecondsToTime(tr.scheduled_dep_sec);
            const arrTime = formatSecondsToTime(tr.scheduled_arr_sec);
            trainRowsHTML += `
                <tr style="border-bottom: 1px solid #2a313d;">
                    <td style="padding: 6px 4px;"><strong>Train ${tr.train_number}</strong></td>
                    <td style="padding: 6px 4px;">${depTime} - ${arrTime}</td>
                    <td style="padding: 6px 4px; color: #10b981; font-weight: bold;">${Math.round(tr.speed_kmh)} km/h</td>
                </tr>
            `;
        });
    } else {
        trainRowsHTML = `<tr><td colspan="3" style="padding: 8px; text-align: center; color: #a0aec0;">No train data for segment.</td></tr>`;
    }

    const popupHTML = `
        <div style="max-height: 280px; overflow-y: auto; width: 300px;">
            <h3 style="margin-bottom: 4px; color: #3b82f6;">📍 ${seg.from_stop_name} ➔ ${seg.to_stop_name}</h3>
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

    const midLat = (seg.from_lat + seg.to_lat) / 2;
    const midLon = (seg.from_lon + seg.to_lon) / 2;

    L.popup()
        .setLatLng([midLat, midLon])
        .setContent(popupHTML)
        .openOn(map);
}

async function openStationTimetable(station) {
    try {
        const resp = await fetch(`/api/stations/${station.stop_id}/timetable`);
        const entries = await resp.json();

        let timetableHTML = `<div style="max-height: 280px; overflow-y: auto; width: 340px;">
            <h3 style="margin-bottom: 8px; color: #00e5ff;">🚉 ${station.stop_name}</h3>
            <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
                <thead>
                    <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                        <th style="padding: 4px;">Train</th>
                        <th style="padding: 4px;">Destination</th>
                        <th style="padding: 4px;">Arr / Dep</th>
                        <th style="padding: 4px;">Status</th>
                    </tr>
                </thead>
                <tbody>`;

        if (entries.length === 0) {
            timetableHTML += `<tr><td colspan="4" style="padding: 8px; text-align: center; color: #a0aec0;">No scheduled trains found.</td></tr>`;
        } else {
            entries.forEach(e => {
                const delayVal = typeof e.delay_minutes === 'number' ? e.delay_minutes : 0;
                const delayStr = delayVal > 0 ? `<span style="color: #ef4444;">+${delayVal}m</span>` : `<span style="color: #10b981;">On Time</span>`;
                const dest = e.destination_station_name || e.headsign || 'N/A';
                timetableHTML += `<tr style="border-bottom: 1px solid #2a313d; cursor: pointer;" class="timetable-row" data-train="${e.train_number}">
                    <td style="padding: 6px 4px;"><strong>Train ${e.train_number}</strong></td>
                    <td style="padding: 6px 4px; color: #e2e8f0; font-weight: 500;">🏁 ${dest}</td>
                    <td style="padding: 6px 4px;">${e.arrival_time} / ${e.departure_time}</td>
                    <td style="padding: 6px 4px;">${delayStr}</td>
                </tr>`;
            });
        }

        timetableHTML += `</tbody></table></div>`;

        L.popup()
            .setLatLng([station.lat, station.lon])
            .setContent(timetableHTML)
            .openOn(map);

    } catch (err) {
        console.error('Failed to load station timetable:', err);
    }
}

async function fetchActiveTrains() {
    try {
        const resp = await fetch(`/api/active-trains?time=${formatSecondsToTime(currentTimeSec)}`);
        activeTrainsData = await resp.json();
        updateTrainMarkers();
        updateOpenTrainDetailsIfActive();
    } catch (e) {
        console.error('Failed to fetch active trains:', e);
    }
}

function updateOpenTrainDetailsIfActive() {
    if (!currentOpenedTripId) return;
    const train = activeTrainsData.find(t => t.trip_id === currentOpenedTripId || t.train_number === currentOpenedTripId);
    if (train) {
        updateTrainDetailsPanel(train, false);
    }
}

function updateTrainMarkers() {
    updateTrainMarkersClientSide();
}

function openTrainDetails(train) {
    currentOpenedTripId = train.trip_id || train.train_number;
    updateTrainDetailsPanel(train, true);
}

function updateTrainDetailsPanel(train, isNewOpen = false) {
    const panel = document.getElementById('side-panel');
    const content = document.getElementById('panel-content');

    const delayBadge = train.delay_minutes > 0
        ? `<span class="badge badge-delay">+${train.delay_minutes} min delay</span>`
        : `<span class="badge badge-on-time">On Time</span>`;

    const originStation = train.first_station_name || 'Origin Station';
    const destStation = train.last_station_name || 'Destination Station';

    // Preserve existing live-delay-result HTML if updating in place
    let liveResultHTML = '';
    const existingResultDiv = document.getElementById('live-delay-result');
    if (!isNewOpen && existingResultDiv) {
        liveResultHTML = existingResultDiv.innerHTML;
    }

    content.innerHTML = `
        <div class="panel-header">
            <div class="panel-title">🚆 Train ${train.train_number}</div>
            ${delayBadge}
        </div>
        <div class="detail-row">
            <div class="detail-label">First Station (Origin)</div>
            <div class="detail-value">🚩 ${originStation}</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Last Station (Destination)</div>
            <div class="detail-value">🏁 ${destStation}</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Route / Headsign</div>
            <div class="detail-value">${train.headsign || 'HŽ Passenger Transport'}</div>
        </div>
        <div class="detail-row">
            <div class="detail-label">Current Segment</div>
            <div class="detail-value">${train.prev_station_name} ➔ ${train.next_station_name}</div>
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
    `;

    panel.style.display = 'block';

    document.getElementById('check-delay-btn').addEventListener('click', async () => {
        const resultDiv = document.getElementById('live-delay-result');
        resultDiv.innerHTML = '<em>Fetching live status from hzpp.app...</em>';
        try {
            const resp = await fetch(`/api/train-delay?trainId=${train.train_number}`);
            const data = await resp.json();
            if (data.success && data.data) {
                const live = data.data;
                const liveDelayMins = live.delayMinutes || 0;
                const liveDelayStr = liveDelayMins > 0 ? `+${liveDelayMins} min` : 'On Time / No Delay';

                train.delay_minutes = liveDelayMins;

                resultDiv.innerHTML = `
                    <div style="background: #1e242e; padding: 10px; border-radius: 6px; border: 1px solid #3a4250;">
                        <div><strong>Status:</strong> ${live.positionStatus || 'Unknown'}</div>
                        <div><strong>Live Delay:</strong> <span style="color:${liveDelayMins > 0 ? '#ef4444' : '#10b981'}">${liveDelayStr}</span></div>
                        <div><strong>Last Station:</strong> ${live.lastStation || 'N/A'}</div>
                        <div><strong>Next Station:</strong> ${live.nextStation || 'N/A'}</div>
                    </div>
                `;

                // Re-render the panel content immediately so badge and delay status text update
                updateTrainDetailsPanel(train, false);

                // Trigger active trains fetch to adjust map positions with new delay data
                fetchActiveTrains();
            } else {
                resultDiv.innerHTML = '<span style="color: #ef4444;">No live status available for this train.</span>';
            }
        } catch (e) {
            resultDiv.innerHTML = `<span style="color: #ef4444;">Failed to fetch live delay: ${e.message}</span>`;
        }
    });
}
