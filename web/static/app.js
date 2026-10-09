let map;
let stationsData = [];
let activeTrainsData = [];
let segmentsData = [];
let stationMarkersMap = {};
let trainMarkersMap = {};
let segmentLinesMap = {};

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

    timeSlider.addEventListener('input', (e) => {
        isRealtime = false;
        realtimeBtn.classList.remove('active');
        currentTimeSec = parseInt(e.target.value, 10);
        timeDisplay.textContent = formatSecondsToTime(currentTimeSec);
        fetchActiveTrains();
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

    searchInput.addEventListener('input', (e) => {
        const query = e.target.value.trim().toLowerCase();
        if (query.length < 2) {
            searchResults.style.display = 'none';
            return;
        }

        const filteredStations = stationsData.filter(s => s.stop_name.toLowerCase().includes(query)).slice(0, 5);
        const filteredTrains = activeTrainsData.filter(t => t.train_number.toLowerCase().includes(query)).slice(0, 5);

        searchResults.innerHTML = '';
        if (filteredStations.length === 0 && filteredTrains.length === 0) {
            searchResults.style.display = 'none';
            return;
        }

        filteredStations.forEach(st => {
            const div = document.createElement('div');
            div.className = 'search-item';
            div.innerHTML = `<strong>🚉 ${st.stop_name}</strong> <span style="font-size: 11px; color: #a0aec0;">(Station)</span>`;
            div.onclick = () => {
                map.setView([st.lat, st.lon], 12);
                openStationTimetable(st);
                searchResults.style.display = 'none';
                searchInput.value = '';
            };
            searchResults.appendChild(div);
        });

        filteredTrains.forEach(tr => {
            const div = document.createElement('div');
            div.className = 'search-item';
            div.innerHTML = `<strong>🚆 Train ${tr.train_number}</strong> <span style="font-size: 11px; color: #a0aec0;">(${tr.headsign || 'Active'})</span>`;
            div.onclick = () => {
                map.setView([tr.lat, tr.lon], 11);
                openTrainDetails(tr);
                searchResults.style.display = 'none';
                searchInput.value = '';
            };
            searchResults.appendChild(div);
        });

        searchResults.style.display = 'block';
    });

    document.addEventListener('click', (e) => {
        if (!searchInput.contains(e.target) && !searchResults.contains(e.target)) {
            searchResults.style.display = 'none';
        }
    });

    document.getElementById('close-panel').addEventListener('click', () => {
        document.getElementById('side-panel').style.display = 'none';
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
    stationsData.forEach(st => {
        const icon = L.divIcon({
            className: 'station-marker',
            iconSize: [10, 10]
        });

        const marker = L.marker([st.lat, st.lon], { icon: icon }).addTo(map);
        marker.bindTooltip(st.stop_name);

        marker.on('click', () => {
            openStationTimetable(st);
        });

        stationMarkersMap[st.stop_id] = marker;
    });
}

function renderSegments() {
    segmentsData.forEach(seg => {
        const key = `${seg.from_stop_id}-${seg.to_stop_id}`;
        const line = L.polyline([[seg.from_lat, seg.from_lon], [seg.to_lat, seg.to_lon]], {
            color: '#3b82f6',
            weight: 3,
            opacity: 0.6
        }).addTo(map);

        const midLat = (seg.from_lat + seg.to_lat) / 2;
        const midLon = (seg.from_lon + seg.to_lon) / 2;

        const speedLabelIcon = L.divIcon({
            className: 'segment-speed-badge',
            html: `<span>⚡ ${Math.round(seg.avg_speed_kmh)} km/h</span>`,
            iconSize: [60, 20],
            iconAnchor: [30, 10]
        });

        const labelMarker = L.marker([midLat, midLon], { icon: speedLabelIcon }).addTo(map);

        const clickHandler = () => {
            openSegmentDetails(seg);
        };

        line.on('click', clickHandler);
        labelMarker.on('click', clickHandler);

        segmentLinesMap[key] = { line, labelMarker };
    });
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

        let timetableHTML = `<div style="max-height: 250px; overflow-y: auto;">
            <h3 style="margin-bottom: 8px; color: #00e5ff;">🚉 ${station.stop_name}</h3>
            <table style="width: 100%; border-collapse: collapse; font-size: 13px;">
                <thead>
                    <tr style="border-bottom: 1px solid #3a4250; text-align: left; color: #a0aec0;">
                        <th style="padding: 4px;">Train</th>
                        <th style="padding: 4px;">Arr / Dep</th>
                        <th style="padding: 4px;">Status</th>
                    </tr>
                </thead>
                <tbody>`;

        if (entries.length === 0) {
            timetableHTML += `<tr><td colspan="3" style="padding: 8px; text-align: center; color: #a0aec0;">No scheduled trains found.</td></tr>`;
        } else {
            entries.forEach(e => {
                const delayVal = typeof e.delay_minutes === 'number' ? e.delay_minutes : 0;
                const delayStr = delayVal > 0 ? `<span style="color: #ef4444;">+${delayVal}m</span>` : `<span style="color: #10b981;">On Time</span>`;
                timetableHTML += `<tr style="border-bottom: 1px solid #2a313d;">
                    <td style="padding: 6px 4px;"><strong>${e.train_number}</strong></td>
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
    } catch (e) {
        console.error('Failed to fetch active trains:', e);
    }
}

function updateTrainMarkers() {
    updateTrainMarkersClientSide();
}

function openTrainDetails(train) {
    const panel = document.getElementById('side-panel');
    const content = document.getElementById('panel-content');

    const delayBadge = train.delay_minutes > 0
        ? `<span class="badge badge-delay">+${train.delay_minutes} min delay</span>`
        : `<span class="badge badge-on-time">On Time</span>`;

    const originStation = train.first_station_name || 'Origin Station';
    const destStation = train.last_station_name || 'Destination Station';

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
        <div id="live-delay-result" style="margin-top: 10px; font-size: 13px;"></div>
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

                resultDiv.innerHTML = `
                    <div style="background: #1e242e; padding: 10px; border-radius: 6px; border: 1px solid #3a4250;">
                        <div><strong>Status:</strong> ${live.positionStatus || 'Unknown'}</div>
                        <div><strong>Live Delay:</strong> <span style="color:${liveDelayMins > 0 ? '#ef4444' : '#10b981'}">${liveDelayStr}</span></div>
                        <div><strong>Last Station:</strong> ${live.lastStation || 'N/A'}</div>
                        <div><strong>Next Station:</strong> ${live.nextStation || 'N/A'}</div>
                    </div>
                `;

                // Immediately trigger active trains fetch to adjust map positions with new delay data
                fetchActiveTrains();
            } else {
                resultDiv.innerHTML = '<span style="color: #ef4444;">No live status available for this train.</span>';
            }
        } catch (e) {
            resultDiv.innerHTML = `<span style="color: #ef4444;">Failed to fetch live delay: ${e.message}</span>`;
        }
    });
}
