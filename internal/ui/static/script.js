//
let currentPage = 1;
let pageSize = 50;
let totalPages = 1;
let searchTimeout = null;
let currentSearchTerm = '';
let toastTimeout = null;

//
document.addEventListener('DOMContentLoaded', function() {
    const urlParams = new URLSearchParams(window.location.search);
    const pageParam = urlParams.get('page');
    const searchParam = urlParams.get('search');

    //
    if (pageParam && !isNaN(parseInt(pageParam))) {
        currentPage = parseInt(pageParam);
    }

    if (searchParam) {
        currentSearchTerm = searchParam;
        document.getElementById('searchInput').value = searchParam;
    }

    //
    fetchRepositories();

    //
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

    //
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

    //
    setupPopupClickHandlers();
});

//
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

//
function fetchRepositories() {
    // Api to fetch repositories
    const url = `/api/repos?page=${currentPage}&pageSize=${pageSize}&search=${encodeURIComponent(currentSearchTerm)}`;
    fetch(url)
        .then(response => response.json())
        .then(data => {
            displayRepositories(data.repositories);
            updatePagination(data.pagination);

            if (data.repositories.length === 0) {
                showToast('Không có dữ liệu!', 'info');
            }
        })
        .catch(error => {
            console.error('Error fetching repositories:', error);
            showToast('Không có dữ liệu!');
        });
}

// Check if a repository has releases
function checkRepositoryHasReleases(repoId) {
    return fetch(`/api/releases?repoId=${repoId}`)
        .then(response => response.json())
        .then(releases => releases.length > 0)
        .catch(error => {
            console.error('Error checking releases:', error);
            return false;
        });
}

// Check if a release has commits
function checkReleaseHasCommits(releaseId) {
    return fetch(`/api/commits?releaseId=${releaseId}`)
        .then(response => response.json())
        .then(commits => commits.length > 0)
        .catch(error => {
            console.error('Error checking commits:', error);
            return false;
        });
}

//
function displayRepositories(repositories) {
    const tableBody = document.getElementById('repoTableBody');
    tableBody.innerHTML = '';

    if (repositories.length === 0) {
        const row = document.createElement('tr');
        row.innerHTML = '<td colspan="8" align="center">No repositories found</td>';
        tableBody.appendChild(row);
        return;
    }

    // Keep track of pending promises
    const promises = [];

    repositories.forEach(repo => {
        const row = document.createElement('tr');

        // Create a temporary cell for action button that will be updated
        const actionCell = document.createElement('td');
        actionCell.className = 'action-cell';
        actionCell.innerHTML = '<span>Checking...</span>';

        row.innerHTML = `
            <td>${repo.id}</td>
            <td>${escapeHtml(repo.user)}</td>
            <td>${escapeHtml(repo.name)}</td>
            <td>${repo.starCount}</td>
            <td>${repo.forkCount}</td>
            <td>${repo.watchCount}</td>
            <td>${repo.issueCount}</td>
        `;
        row.appendChild(actionCell);
        tableBody.appendChild(row);

        // Check if the repository has releases
        const promise = checkRepositoryHasReleases(repo.id).then(hasReleases => {
            if (hasReleases) {
                actionCell.innerHTML = `<button onclick="showReleases(${repo.id})">Releases</button>`;
            } else {
                actionCell.innerHTML = '<span>No releases</span>';
            }
        });

        promises.push(promise);
    });

    // Wait for all checks to complete
    Promise.all(promises).catch(error => {
        console.error('Error checking repositories for releases:', error);
    });
}

//
function updatePagination(pagination) {
    totalPages = pagination.totalPages;
    document.getElementById('pageInfo').textContent = `Page ${pagination.page} of ${pagination.totalPages || 1}`;
    document.getElementById('prevPage').disabled = pagination.page <= 1;
    document.getElementById('nextPage').disabled = pagination.page >= pagination.totalPages;
}

//
function showReleases(repoId) {
    fetch(`/api/releases?repoId=${repoId}`)
        .then(response => response.json())
        .then(releases => {
            displayReleases(releases, repoId);
            const popup = document.getElementById('releasesPopup');
            popup.style.display = 'flex';
            popup.classList.add('active');

            if (releases.length === 0) {
                showToast('Không có dữ liệu!', 'info');
            }
        })
        .catch(error => {
            console.error('Error fetching releases:', error);
            showToast('Không có dữ liệu!');
        });
}

//
function displayReleases(releases, repoId) {
    const tableBody = document.getElementById('releaseTableBody');
    tableBody.innerHTML = '';

    if (releases.length === 0) {
        const row = document.createElement('tr');
        row.innerHTML = '<td colspan="3" align="center">Không có dữ liệu!</td>';
        tableBody.appendChild(row);
        return;
    }

    // Keep track of pending promises
    const promises = [];

    releases.forEach(release => {
        const row = document.createElement('tr');

        // Create a temporary cell for action button that will be updated
        const actionCell = document.createElement('td');
        actionCell.className = 'action-cell';
        actionCell.innerHTML = '<span>Checking...</span>';

        row.innerHTML = `
            <td>${release.id}</td>
            <td>${escapeHtml(release.content.substring(0, 100))}${release.content.length > 100 ? '...' : ''}</td>
        `;
        row.appendChild(actionCell);
        tableBody.appendChild(row);

        // Check if the release has commits
        const promise = checkReleaseHasCommits(release.id).then(hasCommits => {
            if (hasCommits) {
                actionCell.innerHTML = `<button onclick="showCommits(${release.id})">Commits</button>`;
            } else {
                actionCell.innerHTML = '<span>No commits</span>';
            }
        });

        promises.push(promise);
    });

    // Wait for all checks to complete
    Promise.all(promises).catch(error => {
        console.error('Error checking releases for commits:', error);
    });

    document.getElementById('releasesPopup').querySelector('h2').textContent = `Releases for Repository #${repoId}`;
}

//
function showCommits(releaseId) {
    fetch(`/api/commits?releaseId=${releaseId}`)
        .then(response => response.json())
        .then(commits => {
            displayCommits(commits, releaseId);
            const popup = document.getElementById('commitsPopup');
            popup.style.display = 'flex';
            popup.classList.add('active');

            if (commits.length === 0) {
                showToast('Không có dữ liệu!', 'info');
            }
        })
        .catch(error => {
            console.error('Error fetching commits:', error);
            showToast('Không có dữ liệu!');
        });
}

// Display commits in the popup
function displayCommits(commits, releaseId) {
    const tableBody = document.getElementById('commitTableBody');
    tableBody.innerHTML = '';

    if (commits.length === 0) {
        const row = document.createElement('tr');
        row.innerHTML = '<td colspan="3" align="center">Không có dữ liệu!</td>';
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

    document.getElementById('commitsPopup').querySelector('h2').textContent = `Commits of Release #${releaseId}`;
}

function escapeHtml(text) {
    if (!text) return '';
    return text
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#039;");
}

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

//
function setupPopupClickHandlers() {
    document.getElementById('releasesPopup').addEventListener('click', function(event) {
        if (event.target === this) {
            closeReleasesPopup();
        }
    });

    document.getElementById('commitsPopup').addEventListener('click', function(event) {
        if (event.target === this) {
            closeCommitsPopup();
        }
    });

    // Close modal
    document.addEventListener('keydown', function(event) {
        if (event.key === 'Escape') {
            closeReleasesPopup();
            closeCommitsPopup();
        }
    });
}

//
function showToast(message, type = 'error') {
    if (toastTimeout) {
        clearTimeout(toastTimeout);
        const existingToast = document.getElementById('toast-notification');
        if (existingToast) {
            document.body.removeChild(existingToast);
        }
    }

    //
    const toast = document.createElement('div');
    toast.id = 'toast-notification';
    toast.innerText = message;

    //
    toast.style.position = 'fixed';
    toast.style.top = '20px';
    toast.style.right = '20px';
    toast.style.padding = '10px 25px';
    toast.style.borderRadius = '4px';
    toast.style.backgroundColor = type === 'error' ? '#f44336' : '#4CAF50';
    toast.style.color = 'white';
    toast.style.zIndex = '1000';
    toast.style.minWidth = '250px';
    toast.style.textAlign = 'center';
    toast.style.boxShadow = '0 2px 5px rgba(0,0,0,0.3)';
    toast.style.transition = 'opacity 0.75s ease-in-out';

    //
    document.body.appendChild(toast);

    // Auto close after 1 seconds
    toastTimeout = setTimeout(() => {
        toast.style.opacity = '0';
        setTimeout(() => {
            if (toast.parentNode) {
                document.body.removeChild(toast);
            }
        }, 750);
    }, 750);
}
