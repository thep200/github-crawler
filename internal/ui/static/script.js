// Global variables
let currentPage = 1;
let pageSize = 50;
let totalPages = 1;
let searchTimeout = null;
let currentSearchTerm = '';

// Event listeners
document.addEventListener('DOMContentLoaded', function() {
    // Parse URL parameters for initial state
    const urlParams = new URLSearchParams(window.location.search);
    const pageParam = urlParams.get('page');
    const searchParam = urlParams.get('search');

    // Set initial values from URL if available
    if (pageParam && !isNaN(parseInt(pageParam))) {
        currentPage = parseInt(pageParam);
    }

    if (searchParam) {
        currentSearchTerm = searchParam;
        document.getElementById('searchInput').value = searchParam;
    }

    // Initial load of repository data
    fetchRepositories();

    // Set up search input with debounce
    document.getElementById('searchInput').addEventListener('input', function() {
        if (searchTimeout) {
            clearTimeout(searchTimeout);
        }

        searchTimeout = setTimeout(() => {
            currentSearchTerm = this.value.trim();
            currentPage = 1;
            updateUrl();
            fetchRepositories();
        }, 300);
    });

    // Set up pagination buttons
    document.getElementById('prevPage').addEventListener('click', function() {
        if (currentPage > 1) {
            currentPage--;
            updateUrl();
            fetchRepositories();
        }
    });

    document.getElementById('nextPage').addEventListener('click', function() {
        if (currentPage < totalPages) {
            currentPage++;
            updateUrl();
            fetchRepositories();
        }
    });
});

// Update URL with current search and page parameters
function updateUrl() {
    const params = new URLSearchParams();

    if (currentSearchTerm) {
        params.set('search', currentSearchTerm);
    }

    if (currentPage > 1) {
        params.set('page', currentPage);
    }

    const newUrl = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
    history.pushState({ page: currentPage, search: currentSearchTerm }, '', newUrl);
}

// Fetch repositories from API
function fetchRepositories() {
    const url = `/api/repos?page=${currentPage}&pageSize=${pageSize}&search=${encodeURIComponent(currentSearchTerm)}`;

    fetch(url)
        .then(response => response.json())
        .then(data => {
            displayRepositories(data.repositories);
            updatePagination(data.pagination);
        })
        .catch(error => {
            console.error('Error fetching repositories:', error);
            alert('Failed to fetch repositories. Please try again.');
        });
}

// Display repositories in the table
function displayRepositories(repositories) {
    const tableBody = document.getElementById('repoTableBody');
    tableBody.innerHTML = '';

    if (repositories.length === 0) {
        const row = document.createElement('tr');
        row.innerHTML = '<td colspan="8" align="center">No repositories found</td>';
        tableBody.appendChild(row);
        return;
    }

    repositories.forEach(repo => {
        const row = document.createElement('tr');
        row.innerHTML = `
            <td>${repo.id}</td>
            <td>${escapeHtml(repo.user)}</td>
            <td>${escapeHtml(repo.name)}</td>
            <td>${repo.starCount}</td>
            <td>${repo.forkCount}</td>
            <td>${repo.watchCount}</td>
            <td>${repo.issueCount}</td>
            <td class="action-cell">
                <button onclick="showReleases(${repo.id})">View Releases</button>
            </td>
        `;
        tableBody.appendChild(row);
    });
}

// Update pagination controls
function updatePagination(pagination) {
    totalPages = pagination.totalPages;

    document.getElementById('pageInfo').textContent = `Page ${pagination.page} of ${pagination.totalPages || 1}`;
    document.getElementById('prevPage').disabled = pagination.page <= 1;
    document.getElementById('nextPage').disabled = pagination.page >= pagination.totalPages;
}

// Show releases popup for a repository
function showReleases(repoId) {
    fetch(`/api/releases?repoId=${repoId}`)
        .then(response => response.json())
        .then(releases => {
            displayReleases(releases, repoId);
            const popup = document.getElementById('releasesPopup');
            popup.style.display = 'flex';
            popup.classList.add('active');
        })
        .catch(error => {
            console.error('Error fetching releases:', error);
            alert('Failed to fetch releases. Please try again.');
        });
}

// Display releases in the popup
function displayReleases(releases, repoId) {
    const tableBody = document.getElementById('releaseTableBody');
    tableBody.innerHTML = '';

    if (releases.length === 0) {
        const row = document.createElement('tr');
        row.innerHTML = '<td colspan="3" align="center">No releases found for this repository</td>';
        tableBody.appendChild(row);
        return;
    }

    releases.forEach(release => {
        const row = document.createElement('tr');
        row.innerHTML = `
            <td>${release.id}</td>
            <td>${escapeHtml(release.content.substring(0, 100))}${release.content.length > 100 ? '...' : ''}</td>
            <td class="action-cell">
                <button onclick="showCommits(${release.id})">View Commits</button>
            </td>
        `;
        tableBody.appendChild(row);
    });

    document.getElementById('releasesPopup').querySelector('h2').textContent = `Releases for Repository #${repoId}`;
}

// Show commits popup for a release
function showCommits(releaseId) {
    fetch(`/api/commits?releaseId=${releaseId}`)
        .then(response => response.json())
        .then(commits => {
            displayCommits(commits, releaseId);
            const popup = document.getElementById('commitsPopup');
            popup.style.display = 'flex'; // Changed from 'block' to 'flex'
            popup.classList.add('active');
        })
        .catch(error => {
            console.error('Error fetching commits:', error);
            alert('Failed to fetch commits. Please try again.');
        });
}

// Display commits in the popup
function displayCommits(commits, releaseId) {
    const tableBody = document.getElementById('commitTableBody');
    tableBody.innerHTML = '';

    if (commits.length === 0) {
        const row = document.createElement('tr');
        row.innerHTML = '<td colspan="3" align="center">No commits found for this release</td>';
        tableBody.appendChild(row);
        return;
    }

    commits.forEach(commit => {
        const row = document.createElement('tr');
        row.innerHTML = `
            <td>${commit.id}</td>
            <td>${escapeHtml(commit.hash)}</td>
            <td>${escapeHtml(commit.message.substring(0, 100))}${commit.message.length > 100 ? '...' : ''}</td>
        `;
        tableBody.appendChild(row);
    });

    document.getElementById('commitsPopup').querySelector('h2').textContent = `Commits for Release #${releaseId}`;
}

// Helper function to escape HTML to prevent XSS
function escapeHtml(text) {
    if (!text) return '';
    return text
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#039;");
}

// Add functions to close popups
function closeReleasesPopup() {
    const popup = document.getElementById('releasesPopup');
    popup.style.display = 'none';
    popup.classList.remove('active');
}

function closeCommitsPopup() {
    const popup = document.getElementById('commitsPopup');
    popup.style.display = 'none';
    popup.classList.remove('active');
}
