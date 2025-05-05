// Global state
const state = {
    currentPage: 1,
    pageSize: 25,
    totalPages: 1,
    searchQuery: '',
    currentRepoId: null,
    currentRepoName: null
};

// DOM elements
const elements = {
    repoTableBody: document.getElementById('repoTableBody'),
    loadingIndicator: document.getElementById('loadingIndicator'),
    noResultsMessage: document.getElementById('noResultsMessage'),
    pageInfo: document.getElementById('pageInfo'),
    prevPageBtn: document.getElementById('prevPage'),
    nextPageBtn: document.getElementById('nextPage'),
    searchInput: document.getElementById('searchInput'),
    searchButton: document.getElementById('searchButton'),
    releaseModal: document.getElementById('releaseModal'),
    closeReleaseModal: document.getElementById('closeReleaseModal'),
    releaseModalTitle: document.getElementById('releaseModalTitle'),
    releasesList: document.getElementById('releasesList'),
    releaseLoadingIndicator: document.getElementById('releaseLoadingIndicator'),
    noReleasesMessage: document.getElementById('noReleasesMessage')
};

// Event listeners
document.addEventListener('DOMContentLoaded', () => {
    fetchRepositories();

    // Pagination
    elements.prevPageBtn.addEventListener('click', () => {
        if (state.currentPage > 1) {
            state.currentPage--;
            fetchRepositories();
        }
    });

    elements.nextPageBtn.addEventListener('click', () => {
        if (state.currentPage < state.totalPages) {
            state.currentPage++;
            fetchRepositories();
        }
    });

    // Search
    elements.searchButton.addEventListener('click', () => {
        state.searchQuery = elements.searchInput.value.trim();
        state.currentPage = 1;
        fetchRepositories();
    });

    elements.searchInput.addEventListener('keypress', (e) => {
        if (e.key === 'Enter') {
            state.searchQuery = elements.searchInput.value.trim();
            state.currentPage = 1;
            fetchRepositories();
        }
    });

    // Modal close buttons
    elements.closeReleaseModal.addEventListener('click', () => {
        elements.releaseModal.style.display = 'none';
    });

    // Close modal when clicking outside
    window.addEventListener('click', (e) => {
        if (e.target === elements.releaseModal) {
            elements.releaseModal.style.display = 'none';
        }
    });
});

// Fetch repositories from the API
async function fetchRepositories() {
    try {
        showLoading(true);

        let url = `/api/repos?page=${state.currentPage}&pageSize=${state.pageSize}`;
        if (state.searchQuery) {
            url += `&search=${encodeURIComponent(state.searchQuery)}`;
        }

        const response = await fetch(url);
        if (!response.ok) {
            throw new Error(`HTTP error! Status: ${response.status}`);
        }

        const data = await response.json();
        renderRepositories(data.repositories);
        updatePagination(data.pagination);

    } catch (error) {
        console.error('Error fetching repositories:', error);
        elements.noResultsMessage.textContent = `Error: ${error.message}`;
        elements.noResultsMessage.style.display = 'block';
    } finally {
        showLoading(false);
    }
}

// Render repository data to the table
function renderRepositories(repositories) {
    elements.repoTableBody.innerHTML = '';

    if (!repositories || repositories.length === 0) {
        elements.noResultsMessage.style.display = 'block';
        return;
    }

    elements.noResultsMessage.style.display = 'none';

    repositories.forEach(repo => {
        const row = document.createElement('tr');

        row.innerHTML = `
            <td>${escapeHtml(repo.name)}</td>
            <td>${escapeHtml(repo.user)}</td>
            <td>★ ${repo.starCount.toLocaleString()}</td>
            <td>${repo.forkCount.toLocaleString()}</td>
            <td>${repo.watchCount.toLocaleString()}</td>
            <td>${repo.issueCount.toLocaleString()}</td>
            <td>
                <button data-repo-id="${repo.id}" data-repo-name="${escapeHtml(repo.name)}">Releases</button>
            </td>
        `;

        elements.repoTableBody.appendChild(row);
    });

    // Add event listeners to the release buttons
    document.querySelectorAll('button[data-repo-id]').forEach(button => {
        button.addEventListener('click', () => {
            const repoId = button.getAttribute('data-repo-id');
            const repoName = button.getAttribute('data-repo-name');
            openReleaseModal(repoId, repoName);
        });
    });
}

// Update pagination controls
function updatePagination(pagination) {
    state.totalPages = pagination.totalPages;
    elements.pageInfo.textContent = `Page ${pagination.page} of ${pagination.totalPages}`;

    elements.prevPageBtn.disabled = pagination.page <= 1;
    elements.nextPageBtn.disabled = pagination.page >= pagination.totalPages;
}

// Show/hide loading indicators
function showLoading(isLoading) {
    elements.loadingIndicator.style.display = isLoading ? 'block' : 'none';
    elements.repoTableBody.style.display = isLoading ? 'none' : 'block';
}

// Open modal with releases for a repository
async function openReleaseModal(repoId, repoName) {
    state.currentRepoId = repoId;
    state.currentRepoName = repoName;

    elements.releaseModalTitle.textContent = `Releases for ${repoName}`;
    elements.releaseModal.style.display = 'block';
    elements.releasesList.innerHTML = '';
    elements.releaseLoadingIndicator.style.display = 'block';
    elements.noReleasesMessage.style.display = 'none';

    try {
        const response = await fetch(`/api/releases?repoId=${repoId}`);
        if (!response.ok) {
            throw new Error(`HTTP error! Status: ${response.status}`);
        }

        const releases = await response.json();
        renderReleases(releases);

    } catch (error) {
        console.error('Error fetching releases:', error);
        elements.noReleasesMessage.textContent = `Error: ${error.message}`;
        elements.noReleasesMessage.style.display = 'block';
    } finally {
        elements.releaseLoadingIndicator.style.display = 'none';
    }
}

// Render releases in the modal
function renderReleases(releases) {
    elements.releasesList.innerHTML = '';

    if (!releases || releases.length === 0) {
        elements.noReleasesMessage.style.display = 'block';
        return;
    }

    elements.noReleasesMessage.style.display = 'none';

    releases.forEach(release => {
        const releaseCard = document.createElement('div');
        releaseCard.className = 'release-card';

        // Truncate content if it's too long
        const content = release.content && release.content.length > 300
            ? release.content.substring(0, 300) + '...'
            : release.content || 'No description provided';

        releaseCard.innerHTML = `
            <h3>Release #${release.id}</h3>
            <p>${escapeHtml(content)}</p>
            <div>Created: ${release.createdAt}</div>
        `;

        elements.releasesList.appendChild(releaseCard);
    });
}

// Helper function to escape HTML entities
function escapeHtml(text) {
    if (!text) return '';

    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
