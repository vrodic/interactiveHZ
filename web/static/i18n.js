// UI translations. English text is the key, so untranslated strings fall back to English.
const I18N_HR = {
    // Header, menu and static page text
    'Interactive Train Map': 'Interaktivna karta vlakova',
    'Toggle Menu': 'Izbornik',
    'Search station or train...': 'Traži kolodvor ili vlak...',
    'Trip Planner': 'Planer putovanja',
    'Notices & Works': 'Obavijesti i radovi',
    'Active Trains': 'Aktivni vlakovi',
    'Dashboard': 'Nadzorna ploča',
    'Live Delay Stream': 'Kašnjenja uživo',
    'Live Delays': 'Kašnjenja uživo',
    'Toggle Time Controls': 'Prikaži/sakrij upravljanje vremenom',
    'Time Controls': 'Upravljanje vremenom',
    'Enable Desktop Notifications': 'Uključi obavijesti preglednika',
    'Toggle Speed Markers': 'Prikaži/sakrij oznake brzine',
    'Realtime': 'Stvarno vrijeme',
    'Playback Speed': 'Brzina reprodukcije',
    'Details': 'Detalji',
    'Show train on map': 'Prikaži vlak na karti',
    'Close details': 'Zatvori detalje',
    'Traffic Notices & Track Works (Stanje u prometu)': 'Stanje u prometu i radovi na pruzi',
    'Currently Active Trains': 'Trenutno aktivni vlakovi',
    'Real-Time Delay Process Feed': 'Praćenje kašnjenja u stvarnom vremenu',
    'Total API Calls:': 'Ukupno upita:',
    'Calls/Min:': 'Upita/min:',
    'Calls/Hour:': 'Upita/sat:',
    'Success/Fail:': 'Uspjeh/greška:',
    'Last Call:': 'Zadnji upit:',
    'Live feed streaming train delay updates, background polling activity, and status changes in real time.':
        'Prikaz promjena kašnjenja vlakova, pozadinskih upita i promjena statusa u stvarnom vremenu.',
    '[System] Connecting to real-time delay event stream...': '[Sustav] Spajanje na prijenos kašnjenja uživo...',
    'Live Dashboard & Statistics': 'Nadzorna ploča i statistika',
    'Train Trip Planner': 'Planer putovanja vlakom',
    'FROM (ORIGIN STATION)': 'OD (POLAZNI KOLODVOR)',
    'TO (DESTINATION STATION)': 'DO (ODREDIŠNI KOLODVOR)',
    'Type origin station...': 'Upišite polazni kolodvor...',
    'Type destination station...': 'Upišite odredišni kolodvor...',
    'Use my location (nearest station)': 'Koristi moju lokaciju (najbliži kolodvor)',
    'Use my location': 'Koristi moju lokaciju',
    'Switch Stations': 'Zamijeni kolodvore',
    'Find Trains': 'Pronađi vlakove',
    'Recent searches:': 'Nedavne pretrage:',
    'Select origin and destination stations above to view all scheduled trains for the day.':
        'Odaberite polazni i odredišni kolodvor za prikaz svih vlakova tog dana.',

    // Menu states
    'Enable Notifs': 'Uključi obavijesti',
    'Notifs Active': 'Obavijesti uključene',
    'Notifs Blocked': 'Obavijesti blokirane',
    'Notifs Unsupported': 'Obavijesti nisu podržane',
    'Show Speeds': 'Prikaži brzine',
    'Hide Speeds': 'Sakrij brzine',
    '▶ Play': '▶ Pokreni',
    '⏸ Pause': '⏸ Pauza',
    'Never': 'Nikad',
    'RECONNECTING': 'PONOVNO SPAJANJE',

    // Notifications and toasts
    '🔔 Notifications Enabled': '🔔 Obavijesti uključene',
    'You will now receive desktop notifications for train delay updates and station changes.':
        'Primat ćete obavijesti o promjenama kašnjenja i dolascima vlaka na kolodvore.',
    'Notifications are blocked in your browser settings. Please enable notifications for this site to receive updates.':
        'Obavijesti su blokirane u postavkama preglednika. Dopustite obavijesti za ovu stranicu.',
    'Desktop notifications are not supported by your browser.': 'Vaš preglednik ne podržava obavijesti.',
    '🚆 Train {n} Station Update': '🚆 Vlak {n} – novi kolodvor',
    'Train {n} reached station: {station}': 'Vlak {n} stigao je na kolodvor: {station}',
    '⚠️ Train {n} Delay Update': '⚠️ Vlak {n} – promjena kašnjenja',
    'Train {n} delay status updated: {delay} ({diff}). Current location: {loc}.':
        'Kašnjenje vlaka {n}: {delay} ({diff}). Trenutna lokacija: {loc}.',
    '+{m}m increase': 'više za {m} min',
    '{m}m decrease': 'manje za {m} min',
    '📍 Location unavailable': '📍 Lokacija nije dostupna',
    'Your browser does not support geolocation.': 'Vaš preglednik ne podržava određivanje lokacije.',
    '📍 Stations not loaded': '📍 Kolodvori nisu učitani',
    'Station list is still loading, please try again in a moment.': 'Popis kolodvora se još učitava, pokušajte ponovno za trenutak.',
    '📍 Location error': '📍 Greška lokacije',
    'Location access was denied. Allow location for this site to use this feature.':
        'Pristup lokaciji je odbijen. Dopustite lokaciju za ovu stranicu.',
    'Could not determine your location.': 'Nije moguće odrediti vašu lokaciju.',
    '📍 Nearest station': '📍 Najbliži kolodvor',
    '{name} ({km} km away)': '{name} (udaljeno {km} km)',
    'Show my location and nearest station': 'Prikaži moju lokaciju i najbliži kolodvor',
    'This train is not running on the map right now.': 'Ovaj vlak trenutno nije na karti.',

    // Shared train / delay wording
    'Train': 'Vlak',
    'Train Number': 'Broj vlaka',
    'Status': 'Status',
    'Delay': 'Kašnjenje',
    'Last': 'Zadnji',
    'Next': 'Sljedeći',
    'In Transit': 'U vožnji',
    'Unknown': 'Nepoznato',
    'On Time': 'Na vrijeme',
    'On Time / No Delay': 'Na vrijeme / bez kašnjenja',
    '+{m} min delay': 'kasni +{m} min',
    '+{m} min late': 'kasni +{m} min',
    'Bus': 'Bus',
    'Works': 'Radovi',
    'Bus replacement': 'Zamjenski autobusi',
    'Track works': 'Radovi na pruzi',
    'Origin': 'Polazište',
    'Destination': 'Odredište',
    'Origin Station': 'Polazni kolodvor',
    'Destination Station': 'Odredišni kolodvor',
    'Current Segment': 'Trenutna dionica',
    'Delay Status': 'Kašnjenje',
    'Route': 'Relacija',

    // Notices
    'No active traffic notices or track works reported.': 'Nema aktualnih obavijesti ni radova na pruzi.',
    'Dates': 'Datumi',
    'Date': 'Datum',
    'now': 'sada',
    'Linked documents': 'Povezani dokumenti',
    'Schedule PDF': 'Vozni red PDF',

    // Active trains and dashboard
    'No active trains at current time.': 'Trenutno nema aktivnih vlakova.',
    'Loading live dashboard metrics...': 'Učitavanje statistike...',
    'No delayed trains currently active! 🎉': 'Trenutno nema vlakova u kašnjenju! 🎉',
    'Delayed Trains': 'Vlakovi u kašnjenju',
    'Average Delay': 'Prosječno kašnjenje',
    'Trips Running Today': 'Vlakova danas',
    'Delay API Calls (Total/Min)': 'Upiti o kašnjenju (ukupno/min)',
    'Currently Delayed Trains': 'Vlakovi koji trenutno kasne',
    'Failed to load dashboard statistics': 'Učitavanje statistike nije uspjelo',

    // Search and trip planner
    'No stations or trains found': 'Nema pronađenih kolodvora ni vlakova',
    'No stations found': 'Nema pronađenih kolodvora',
    'Please select valid origin and destination stations.': 'Odaberite ispravan polazni i odredišni kolodvor.',
    'Searching routes...': 'Traženje veza...',
    'No direct or 1-transfer scheduled trains found for this route.': 'Za ovu relaciju nema izravnih vlakova ni veza s jednim presjedanjem.',
    'NEXT DEPARTURE': 'SLJEDEĆI POLAZAK',
    '1 TRANSFER': '1 PRESJEDANJE',
    'Disruption Notice': 'Obavijest o poremećaju',
    'Track works or bus replacement on route': 'Radovi na pruzi ili zamjenski autobusi na relaciji',
    'Departure ➔ Arrival': 'Polazak ➔ Dolazak',
    'Failed to fetch route plans': 'Dohvat veza nije uspio',

    // Map pop-ups
    'Loading scheduled train details...': 'Učitavanje voznog reda...',
    'No train data for segment.': 'Nema podataka o vlakovima za ovu dionicu.',
    'Distance': 'Udaljenost',
    'Avg Speed': 'Prosječna brzina',
    '{n} trains': '{n} vlakova',
    'Dep - Arr': 'Pol. - Dol.',
    'Speed': 'Brzina',
    'Failed to load segment details': 'Učitavanje dionice nije uspjelo',
    'Departure': 'Polazak',
    'No scheduled trains found.': 'Nema vlakova u voznom redu.',

    // Train details panel
    'Loading details for train {n}...': 'Učitavanje podataka za vlak {n}...',
    'Train {n} not found.': 'Vlak {n} nije pronađen.',
    'Scheduled Train': 'Vlak po voznom redu',
    'Currently Active on Route': 'Trenutno u vožnji',
    'Completed Journey': 'Vožnja završena',
    'Scheduled (Departs at {time})': 'Po voznom redu (polazak u {time})',
    'Completed (Arrived at {time})': 'Završeno (dolazak u {time})',
    'Not running at selected time': 'Ne vozi u odabrano vrijeme',
    'Active on Route': 'U vožnji',
    'First Station (Origin)': 'Polazni kolodvor',
    'Last Station (Destination)': 'Odredišni kolodvor',
    'Scheduled Journey Time': 'Vrijeme vožnje po voznom redu',
    'Current Status': 'Trenutni status',
    'Progress': 'Napredak',
    'Running with +{m} min delay': 'Vozi s kašnjenjem od +{m} min',
    'Running with +{m} min delay (position auto-adjusted on map)': 'Vozi s kašnjenjem od +{m} min (položaj na karti je prilagođen)',
    'Refresh Live Delay Status': 'Osvježi kašnjenje uživo',
    'Fetching live status from hzpp.app...': 'Dohvaćanje statusa s hzpp.app...',
    'Live Delay': 'Kašnjenje uživo',
    'Last Station': 'Zadnji kolodvor',
    'Next Station': 'Sljedeći kolodvor',
    'No live status available for this train.': 'Za ovaj vlak nema statusa uživo.',
    'Failed to fetch live delay': 'Dohvat kašnjenja nije uspio',
    'Failed to load train details': 'Učitavanje podataka o vlaku nije uspjelo',
    'Delay Logs (Last 3 Days)': 'Zapisi kašnjenja (zadnja 3 dana)',
    'Loading delay logs...': 'Učitavanje zapisa kašnjenja...',
    'No delay logs recorded in the last 3 days': 'Nema zapisa kašnjenja u zadnja 3 dana',
    '{n} days recorded': 'zapisa za {n} dana',
    'Date / Time': 'Datum / vrijeme',
    'Location': 'Lokacija',
    'Historical Punctuality Breakdown': 'Točnost kroz povijest',
    'No historical delay data available yet': 'Još nema povijesnih podataka o kašnjenju',
    'On time (0 min delay)': 'Na vrijeme (0 min)',
    '1–5 min late': 'Kasni 1–5 min',
    '5–10 min late': 'Kasni 5–10 min',
    '10–30 min late': 'Kasni 10–30 min',
    '>30 min late': 'Kasni >30 min',
    '{n} logs': '{n} zapisa',
};

const LANG_STORAGE_KEY = 'hz_lang';

let currentLang = (() => {
    try {
        const saved = localStorage.getItem(LANG_STORAGE_KEY);
        if (saved === 'hr' || saved === 'en') return saved;
    } catch (e) {
        // Storage can be unavailable (private mode); fall back to the browser language.
    }
    return (navigator.language || '').toLowerCase().startsWith('hr') ? 'hr' : 'en';
})();

// t('Train {n} not found.', { n: 2702 }) -> translated text with {placeholders} filled in.
function t(text, vars) {
    let out = (currentLang === 'hr' && I18N_HR[text]) || text;
    if (vars) {
        out = out.replace(/\{(\w+)\}/g, (m, key) => (vars[key] !== undefined ? vars[key] : m));
    }
    return out;
}

// Translates elements marked data-i18n (their English text is the key) and any
// title / placeholder / aria-label that has a translation.
function applyStaticTranslations(root = document) {
    document.documentElement.lang = currentLang;
    if (currentLang === 'en') return;
    root.querySelectorAll('[data-i18n]').forEach(el => {
        el.textContent = t(el.textContent.trim());
    });
    root.querySelectorAll('[title], [placeholder], [aria-label]').forEach(el => {
        ['title', 'placeholder', 'aria-label'].forEach(attr => {
            const val = el.getAttribute(attr);
            if (val && I18N_HR[val]) el.setAttribute(attr, I18N_HR[val]);
        });
    });
}

function initLanguageToggle() {
    const btn = document.getElementById('toggle-lang-btn');
    const label = document.getElementById('toggle-lang-label');
    if (!btn) return;
    // The button names the language it switches to.
    label.textContent = currentLang === 'hr' ? 'English' : 'Hrvatski';
    btn.addEventListener('click', () => {
        try {
            localStorage.setItem(LANG_STORAGE_KEY, currentLang === 'hr' ? 'en' : 'hr');
        } catch (e) {
            return;
        }
        // Reload so every panel is rebuilt in the new language; the URL hash keeps the open panel.
        location.reload();
    });
}
