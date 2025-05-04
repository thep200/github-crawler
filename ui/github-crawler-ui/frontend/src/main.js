// Import styles
import './style.css';

// Import Wails runtime
import {
    StartCrawling,
    StopCrawling,
    GetCrawlStats,
    GetDbStatus,
    GetInitStatus
} from '../wailsjs/go/main/App';

// DOM elements
let startCrawlingBtn;
let stopCrawlingBtn;
let crawlerVersionSelect;
let crawlerStatusSpan;
let statusMessageDiv;
let dbStatusDiv;
let logsDiv;

// Stats elements
let repoCountEl;
let releaseCountEl;
let commitCountEl;
let durationEl;

// State
let isCrawling = false;
let statsInterval = null;
let isInitialized = false;

// Add log message to the logs div
function addLogMessage(message) {
    const logEntry = document.createElement('p');
    logEntry.classList.add('log-entry');
    logEntry.textContent = `[${new Date().toLocaleTimeString()}] ${message}`;
    logsDiv.appendChild(logEntry);
    logsDiv.scrollTop = logsDiv.scrollHeight; // Auto-scroll to bottom
}

// Update the UI based on crawling state
function updateUI(isRunning) {
    isCrawling = isRunning;
    startCrawlingBtn.disabled = isRunning || !isInitialized;
    stopCrawlingBtn.disabled = !isRunning || !isInitialized;
    crawlerVersionSelect.disabled = isRunning || !isInitialized;

    if (isRunning) {
        crawlerStatusSpan.textContent = 'Running';
        crawlerStatusSpan.classList.remove('stopped');
        crawlerStatusSpan.classList.add('running');
    } else {
        crawlerStatusSpan.textContent = 'Stopped';
        crawlerStatusSpan.classList.remove('running');
        crawlerStatusSpan.classList.add('stopped');
    }

    // Thêm class 'not-initialized' nếu crawler chưa được khởi tạo
    if (!isInitialized) {
        crawlerStatusSpan.textContent = 'Not Initialized';
        crawlerStatusSpan.classList.remove('running');
        crawlerStatusSpan.classList.add('stopped');
    }
}

// Check crawler initialization status
async function checkInitStatus() {
    try {
        const status = await GetInitStatus();
        isInitialized = status.initialized;

        if (!isInitialized && status.error) {
            statusMessageDiv.textContent = `Initialization Error: ${status.error}`;
            addLogMessage(`Crawler initialization failed: ${status.error}`);
            updateUI(false);
        } else if (isInitialized) {
            addLogMessage('Crawler initialized successfully');
        } else {
            statusMessageDiv.textContent = 'Crawler is not initialized';
            addLogMessage('Crawler is not initialized');
            updateUI(false);
        }
    } catch (err) {
        statusMessageDiv.textContent = 'Failed to check initialization status';
        addLogMessage(`Error checking initialization status: ${err}`);
        console.error(err);
        isInitialized = false;
        updateUI(false);
    }
}

// Start the crawler
async function startCrawler() {
    if (!isInitialized) {
        statusMessageDiv.textContent = 'Cannot start: Crawler is not initialized';
        addLogMessage('Cannot start: Crawler is not initialized');
        return;
    }

    const version = crawlerVersionSelect.value;
    try {
        const result = await StartCrawling(version);
        statusMessageDiv.textContent = result;
        addLogMessage(`Started crawler ${version}: ${result}`);

        if (!result.includes('Error:')) {
            updateUI(true);

            // Start periodic stats updates
            if (statsInterval) {
                clearInterval(statsInterval);
            }
            statsInterval = setInterval(updateStats, 1000);
        }
    } catch (err) {
        addLogMessage(`Error starting crawler: ${err}`);
        console.error(err);
    }
}

// Stop the crawler
async function stopCrawler() {
    if (!isInitialized) {
        statusMessageDiv.textContent = 'Cannot stop: Crawler is not initialized';
        addLogMessage('Cannot stop: Crawler is not initialized');
        return;
    }

    try {
        const result = await StopCrawling();
        statusMessageDiv.textContent = result;
        addLogMessage(`Stopping crawler: ${result}`);
    } catch (err) {
        addLogMessage(`Error stopping crawler: ${err}`);
        console.error(err);
    }
}

// Update crawler statistics
async function updateStats() {
    try {
        const stats = await GetCrawlStats();

        // Check if there's an initialization error in stats
        if (stats.lastError && stats.lastError.includes('Crawler is not initialized')) {
            isInitialized = false;
            updateUI(false);

            if (statsInterval) {
                clearInterval(statsInterval);
                statsInterval = null;
            }

            statusMessageDiv.textContent = stats.lastError;
            addLogMessage(stats.lastError);
            return;
        }

        // Update stat counters
        repoCountEl.textContent = stats.reposCrawled;
        releaseCountEl.textContent = stats.releasesCrawled;
        commitCountEl.textContent = stats.commitsCrawled;
        durationEl.textContent = stats.duration || '0s';

        // Update crawler status
        if (isCrawling !== stats.isRunning) {
            updateUI(stats.isRunning);

            // If crawler stopped, display message
            if (!stats.isRunning && isCrawling) {
                addLogMessage('Crawler has stopped');
                if (stats.lastError) {
                    addLogMessage(`Error: ${stats.lastError}`);
                }

                // Clear the stats interval
                if (statsInterval) {
                    clearInterval(statsInterval);
                    statsInterval = null;
                }
            }
        }

        // Show error if any
        if (stats.lastError && !statusMessageDiv.textContent.includes(stats.lastError)) {
            statusMessageDiv.textContent = `Error: ${stats.lastError}`;
        }

    } catch (err) {
        console.error('Failed to update stats:', err);
    }
}

// Check database status
async function checkDbStatus() {
    try {
        const status = await GetDbStatus();
        dbStatusDiv.textContent = status;
        addLogMessage(`Database status: ${status}`);

        // If database status contains error about not being initialized
        if (status.includes('not initialized')) {
            isInitialized = false;
            updateUI(false);
        }
    } catch (err) {
        dbStatusDiv.textContent = 'Database connection error';
        addLogMessage(`Database connection error: ${err}`);
        console.error(err);
    }
}

// Initialize the application
function initApp() {
    // Get DOM references
    startCrawlingBtn = document.getElementById('startCrawling');
    stopCrawlingBtn = document.getElementById('stopCrawling');
    crawlerVersionSelect = document.getElementById('crawlerVersion');
    crawlerStatusSpan = document.getElementById('crawlerStatus');
    statusMessageDiv = document.getElementById('statusMessage');
    dbStatusDiv = document.getElementById('db-status');
    logsDiv = document.getElementById('logs');

    repoCountEl = document.getElementById('repoCount');
    releaseCountEl = document.getElementById('releaseCount');
    commitCountEl = document.getElementById('commitCount');
    durationEl = document.getElementById('duration');

    // Check initialization status first
    checkInitStatus().then(() => {
        // Then check database status
        checkDbStatus();

        // Start stats update if initialized
        if (isInitialized) {
            statsInterval = setInterval(updateStats, 1000);
        }
    });

    // Add event listeners to buttons
    startCrawlingBtn.addEventListener('click', startCrawler);
    stopCrawlingBtn.addEventListener('click', stopCrawler);

    // Initial log message
    addLogMessage('GitHub Crawler UI initialized');
}

// Event listener for DOM loaded
document.addEventListener('DOMContentLoaded', initApp);
