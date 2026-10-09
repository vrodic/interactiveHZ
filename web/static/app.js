let map;
let stationsData = [];
let activeTrainsData = [];
let stationMarkersMap = {};
let trainMarkersMap = {};

let currentTimeSec = getCurrentSecondsOfDay();
let isRealtime = true;
let isPlaying = false;
let playbackSpeed = 1;

function getCurrentSecondsOfDay() {
    const d = new Date();
    return d.getHours() * 3600 + d.getMinutes() * 60 + d.getSeconds();
}

function formatSecondsToTime(totalSec) {
    totalSec = ((totalSec % 86400) + 86400) % 86400;
    const h = Math.floor(totalSec / 3600);
    const m = Math.floor((totalSec % 3600) / 60);
    const s = Math.floor(totalSec % 60);
    return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

document.addEventListener('DOMContentLoaded', () => {
    initMap();
    initControls();
    loadStations();
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

function startUpdateLoop() {
    setInterval(() => {
        if (isRealtime) {
            currentTimeSec = getCurrentSecondsOfDay();
            document.getElementById('time-slider').value = currentTimeSec;
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);
            fetchActiveTrains();
        } else if (isPlaying) {
            currentTimeSec = (currentTimeSec + playbackSpeed * 2) % 86400;
            document.getElementById('time-slider').value = currentTimeSec;
            document.getElementById('time-display').textContent = formatSecondsToTime(currentTimeSec);
            fetchActiveTrains();
        }
    }, 1000);
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
                const delayStr = (e.delay_minutes && e.delay_minutes > 0) ? `<span style="color: #ef4444;">+${e.delay_minutes}m</span>` : `<span style="color: #10b981;">On Time</span>`;
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
    const currentTrainIds = new Set();

    activeTrainsData.forEach(train => {
        currentTrainIds.add(train.trip_id);

        if (trainMarkersMap[train.trip_id]) {
            trainMarkersMap[train.trip_id].setLatLng([train.lat, train.lon]);
        } else {
            const icon = L.divIcon({
                className: `train-marker ${train.delay_minutes > 0 ? 'delayed' : ''}`,
                html: '🚆',
                iconSize: [32, 32],
                iconAnchor: [16, 16]
            });

            const marker = L.marker([train.lat, train.lon], { icon: icon }).addTo(map);

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

function openTrainDetails(train) {
    const panel = document.getElementById('side-panel');
    const content = document.getElementById('panel-content');

    const delayBadge = train.delay_minutes > 0
        ? `<span class="badge badge-delay">+${train.delay_minutes} min delay</span>`
        : `<span class="badge badge-on-time">On Time</span>`;

    content.innerHTML = `
        <div class="panel-header">
            <div class="panel-title">🚆 Train ${train.train_number}</div>
            ${delayBadge}
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
            <div class="detail-label">Coordinates</div>
            <div class="detail-value">${train.lat.toFixed(4)}, ${train.lon.toFixed(4)}</div>
        </div>
        <button class="refresh-delay-btn" id="check-delay-btn">📡 Check Live Delay Status</button>
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
                const liveDelay = live.delayMinutes ? `+${live.delayMinutes} min` : 'On Time / No Delay';
                resultDiv.innerHTML = `
                    <div style="background: #1e242e; padding: 10px; border-radius: 6px; border: 1px solid #3a4250;">
                        <div><strong>Status:</strong> ${live.positionStatus || 'Unknown'}</div>
                        <div><strong>Delay:</strong> ${liveDelay}</div>
                        <div><strong>Last Station:</strong> ${live.lastStation || 'N/A'}</div>
                        <div><strong>Next Station:</strong> ${live.nextStation || 'N/A'}</div>
                    </div>
                `;
            } else {
                resultDiv.innerHTML = '<span style="color: #ef4444;">No live status available for this train.</span>';
            }
        } catch (e) {
            resultDiv.innerHTML = `<span style="color: #ef4444;">Failed to fetch live delay: ${e.message}</span>`;
        }
    });
}
