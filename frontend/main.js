// frontend/main.js

import Modal from './modal.js';
import { state } from './state.js';
import { submitPostForm } from './api.js';
import {
    applyStatefulDOMChanges,
    toggleImageSize,
    toggleThreadVisibility,
    highlightTargetPost,
    quote
} from './dom.js';
import { showPreview, hidePreview } from './preview.js';

// --- HELPER FUNCTIONS ---
function saveNameToHistory(name) {
    let history = JSON.parse(localStorage.getItem('yib_nameHistory')) || [];
    history = history.filter(item => item !== name);
    history.unshift(name);
    if (history.length > 4) history.length = 4;
    localStorage.setItem('yib_nameHistory', JSON.stringify(history));
}

async function refreshContent() {
    if (state.isUserActionInProgress) return;
    try {
        const response = await fetch(window.location.href, { headers: { 'Accept': 'text/html' } });
        if (!response.ok) {
            stopAutoRefresh();
            return;
        }
        const html = await response.text();
        const parser = new DOMParser();
        const newDoc = parser.parseFromString(html, 'text/html');
        const currentContainer = document.getElementById('content-container');
        const newContainer = newDoc.getElementById('content-container');
        if (currentContainer && newContainer) {
            currentContainer.innerHTML = newContainer.innerHTML;
            applyStatefulDOMChanges();
        }
    } catch (error) {
        stopAutoRefresh();
    }
}

function startAutoRefresh() {
    stopAutoRefresh();
    const intervalInput = document.getElementById('auto-refresh-interval');
    let intervalSeconds = parseInt(intervalInput.value, 10);
    if (isNaN(intervalSeconds) || intervalSeconds < 10) intervalSeconds = 10;
    intervalInput.value = intervalSeconds;
    state.autoRefreshTimer = setInterval(refreshContent, intervalSeconds * 1000);
}

function stopAutoRefresh() {
    clearInterval(state.autoRefreshTimer);
    state.autoRefreshTimer = null;
}

// --- UNIFIED API HANDLER ---
async function handleModalApiSubmit(modal, url, data, successMessage) {
    try {
        const body = new URLSearchParams(data);
        const response = await fetch(url, { method: 'POST', body: body });
        
        const text = await response.text();
        if (!text) {
            throw new Error('Server sent an empty response.');
        }
        
        const result = JSON.parse(text);
        if (!response.ok) {
            throw new Error(result.error || 'An unknown error occurred.');
        }

        modal.close();
        Modal.alert('Success', successMessage || result.success);

        setTimeout(() => {
            if (result.redirect && result.redirect !== "") {
                window.location.assign(result.redirect);
            } else {
                window.location.reload();
            }
        }, 1200);

    } catch (error) {
        modal.close();
        const errorMessage = error.message || 'An unexpected error or invalid server response occurred.';
        Modal.alert('Error', errorMessage);
    }
}


// --- MODAL CREATION FUNCTIONS ---
function showReportModal(postId) {
    const csrfToken = document.querySelector('input[name="csrf_token"]')?.value;
    if (!csrfToken) return Modal.alert('Error', 'Could not find security token.');
    const content = `<form><textarea name="reason" class="modal-textarea" placeholder="Reason..." required></textarea></form>`;
    new Modal(`Report Post No. ${postId}`, content, [
        { id: 'report-cancel', text: 'Cancel' },
        {
            id: 'report-submit', text: 'Submit', class: 'button-primary',
            onClick: (e, modal) => {
                const data = { post_id: postId, reason: modal.modalEl.querySelector('textarea').value, csrf_token: csrfToken };
                handleModalApiSubmit(modal, '/report', data, "Report submitted successfully.");
            }
        }
    ]).show();
}

function showBanModal(link) {
    const { ipHash, cookieHash, csrfToken } = link.dataset;
    const content = `
        <form class="modal-form">
            <label for="ban-reason">Reason:</label>
            <input type="text" id="ban-reason" name="reason" required>
            <label for="ban-duration">Duration (hours, 0=perm):</label>
            <input type="number" id="ban-duration" name="duration" value="0" min="0">
        </form>`;
    new Modal('Apply Ban', content, [
        { id: 'ban-cancel', text: 'Cancel' },
        {
            id: 'ban-submit', text: 'Apply Ban', class: 'button-danger',
            onClick: (e, modal) => {
                const data = {
                    ip_hash: ipHash, cookie_hash: cookieHash,
                    reason: modal.modalEl.querySelector('#ban-reason').value,
                    duration: modal.modalEl.querySelector('#ban-duration').value,
                    csrf_token: csrfToken
                };
                handleModalApiSubmit(modal, '/mod/ban', data, "Ban successfully applied.");
            }
        }
    ]).show();
}

function showBoardDeleteModal(button) {
    const { boardId, csrfToken } = button.dataset;
    const message = `WARNING: Are you sure you want to permanently delete the board /${boardId}/? This will delete all threads, posts, and images and cannot be undone.`;
    new Modal('Confirmation Required', `<p>${message}</p>`, [
        { id: 'modal-cancel', text: 'Cancel' },
        {
            id: 'modal-confirm', text: 'Confirm', class: 'button-danger',
            onClick: (evt, modal) => {
                const data = { board_id: boardId, csrf_token: csrfToken };
                handleModalApiSubmit(modal, '/mod/delete-board', data, "Board deleted successfully.");
            }
        }
    ]).show();
}

// --- GLOBAL EVENT LISTENERS ---
function handlePostDeletion(target) {
    const postId = target.dataset.postId;
    const csrfToken = target.dataset.csrfToken;
    const isModDelete = target.classList.contains('js-mod-delete');
    const url = isModDelete ? '/mod/delete-post' : '/delete';
    
    const confirmMessage = isModDelete 
        ? `MOD ACTION: Are you sure you want to permanently delete post No. ${postId}?`
        : `Are you sure you want to delete your post No. ${postId}? This cannot be undone.`;

    const performDelete = async () => {
        try {
            const formData = new URLSearchParams({ post_id: postId, csrf_token: csrfToken });
            const response = await fetch(url, { method: 'POST', body: formData });
            const result = await response.json();

            if (!response.ok) {
                throw new Error(result.error || 'An unknown error occurred.');
            }

            if (result.redirect) {
                window.location.assign(result.redirect);
            } else {
                window.location.reload();
            }
        } catch (error) {
            Modal.alert('Deletion Error', error.message);
        }
    };

    new Modal('Confirm Deletion', `<p>${confirmMessage}</p>`, [
        {
            id: 'delete-cancel',
            text: 'Cancel',
            class: 'button-secondary'
        },
        {
            id: 'delete-confirm',
            text: 'Delete',
            class: 'button-danger',
            onClick: (e, modal) => {
                modal.close();
                performDelete();
            }
        }
    ]).show();
}

function handleGlobalClick(e) {
    const target = e.target;
    if (target.classList.contains('postImg') && target.closest('a')) {
        e.preventDefault();
        toggleImageSize(target);
    } else if (target.classList.contains('hide-thread-link')) {
        e.preventDefault();
        toggleThreadVisibility(document.body.dataset.boardId, target.dataset.threadId);
    } else if (target.classList.contains('report-link')) {
        e.preventDefault();
        showReportModal(target.dataset.postId);
    } else if (target.classList.contains('ban-link')) {
        e.preventDefault();
        showBanModal(target);
    } else if (target.classList.contains('js-user-delete') || target.classList.contains('js-mod-delete')) {
        e.preventDefault();
        handlePostDeletion(target);
    } else if (target.classList.contains('js-board-delete')) {
        e.preventDefault();
        showBoardDeleteModal(target);
    } else if (target.classList.contains('js-quote-link')) {
        e.preventDefault();
        quote(target.dataset.postId);
    }
}

function handleGlobalMouseOver(e) { if (e.target.classList.contains('backlink')) showPreview(e); }
function handleGlobalMouseOut(e) { if (e.target.classList.contains('backlink')) hidePreview(e); }

// --- INITIALIZATION FUNCTIONS ---
function initFileInputHandler() {
    const fileInput = document.getElementById('file-input');
    const clearButton = document.getElementById('file-clear');

    if (!fileInput || !clearButton) {
        return;
    }

    fileInput.addEventListener('change', () => {
        if (fileInput.files.length > 0) {
            clearButton.style.display = 'inline-block';
        } else {
            clearButton.style.display = 'none';
        }
    });

    clearButton.addEventListener('click', () => {
        fileInput.value = null; 
        fileInput.dispatchEvent(new Event('change'));
    });
}

function initAutoRefresh() {
    const checkbox = document.getElementById('auto-refresh-checkbox');
    const intervalInput = document.getElementById('auto-refresh-interval');
    if (!checkbox || !intervalInput) return;
    const savedInterval = localStorage.getItem('yib_refreshInterval');
    if (savedInterval) intervalInput.value = savedInterval;
    if (localStorage.getItem('yib_refreshEnabled') === 'true') {
        checkbox.checked = true;
        startAutoRefresh();
    }
    checkbox.addEventListener('change', () => {
        localStorage.setItem('yib_refreshEnabled', String(checkbox.checked));
        checkbox.checked ? startAutoRefresh() : stopAutoRefresh();
    });
    intervalInput.addEventListener('change', () => {
        localStorage.setItem('yib_refreshInterval', intervalInput.value);
        if (checkbox.checked) {
            stopAutoRefresh();
            startAutoRefresh();
        }
    });
}

function initThemeSwitcher() {
    const switcher = document.getElementById('theme-switcher');
    const body = document.body;
    const themeLink = document.getElementById('theme-link');
    const defaultTheme = body.dataset.defaultTheme;
    function applyTheme(theme) {
        if (theme === 'default' || !theme) theme = defaultTheme;
        body.className = `theme-${theme}`;
        themeLink.href = `/static/themes/${theme}.css`;
    }
    const savedTheme = localStorage.getItem('yib_theme') || 'default';
    if (switcher) switcher.value = savedTheme;
    applyTheme(savedTheme);
    switcher?.addEventListener('change', () => {
        const selectedTheme = switcher.value;
        localStorage.setItem('yib_theme', selectedTheme);
        applyTheme(selectedTheme);
    });
}

function initNameHistory() {
    const container = document.querySelector('.name-history-container');
    const nameInput = container?.querySelector('input[name="name"]');
    if (!container || !nameInput) return;
    const history = JSON.parse(localStorage.getItem('yib_nameHistory')) || [];
    if (history.length > 0) {
        const dropdown = document.createElement('div');
        dropdown.className = 'name-history-dropdown';
        history.forEach(name => {
            const option = document.createElement('div');
            option.className = 'name-history-option';
            option.textContent = name;
            option.addEventListener('mousedown', (e) => {
                e.preventDefault();
                nameInput.value = name;
                dropdown.style.display = 'none';
            });
            dropdown.appendChild(option);
        });
        container.appendChild(dropdown);
        nameInput.addEventListener('focus', () => { dropdown.style.display = 'block'; });
        nameInput.addEventListener('blur', () => { setTimeout(() => { dropdown.style.display = 'none'; }, 150); });
    }
}

function initBoardSelector() {
    const selector = document.getElementById('board-switcher');
    if (!selector) return;
    selector.addEventListener('change', () => {
        const boardUrl = selector.value;
        if (boardUrl) window.location.href = boardUrl;
    });
}

function initManualBanForm() {
    const form = document.getElementById('manual-ban-form');
    if (!form) return;
    form.addEventListener('submit', function(event) {
        event.preventDefault();
        const dummyModal = { close: () => {} }; // For API handler
        const data = {
            ip_hash: document.getElementById('manual-ban-ip-hash').value,
            cookie_hash: document.getElementById('manual-ban-cookie-hash').value,
            reason: document.getElementById('manual-ban-reason').value,
            duration: document.getElementById('manual-ban-duration').value,
            csrf_token: document.getElementById('manual-ban-csrf').value,
        };
        if (!data.ip_hash && !data.cookie_hash) {
            Modal.alert('Error', 'You must provide at least one hash to ban.');
            return;
        }
        const promise = handleModalApiSubmit(dummyModal, '/mod/ban', data, "Ban successfully applied.");
        promise.then(() => form.reset()).catch(() => {});
    });
}


function initMainPostForm() {
    const postForm = document.querySelector('.postForm > form');
    if (!postForm) return;

    postForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        state.isUserActionInProgress = true;
        stopAutoRefresh();

        const result = await submitPostForm(postForm);

        if (result && result.redirect) {
            const nameInput = postForm.querySelector('input[name="name"]');
            if (nameInput && nameInput.value) {
                saveNameToHistory(nameInput.value);
            }

            const currentUrl = new URL(window.location.href);
            const redirectUrl = new URL(result.redirect, window.location.origin);

            if (currentUrl.pathname === redirectUrl.pathname) {

                const newHash = redirectUrl.hash;
                window.location.hash = newHash;
                window.location.reload();

            } else {
                window.location.assign(result.redirect);
            }
            
            return;
        }

        state.isUserActionInProgress = false;
        const checkbox = document.getElementById('auto-refresh-checkbox');
        if (checkbox && checkbox.checked) {
            startAutoRefresh();
        }
    });
}

// --- MAIN INITIALIZATION ---
function init() {
    document.addEventListener('click', handleGlobalClick);
    
    initMainPostForm();
    initManualBanForm();
    initThemeSwitcher();
    initBoardSelector();
    initNameHistory();
    initAutoRefresh();
    initFileInputHandler();

    // Run logic specific to user-facing pages.
    if (document.body.id !== 'mod-page') {
        document.addEventListener('mouseover', handleGlobalMouseOver);
        document.addEventListener('mouseout', handleGlobalMouseOut);
        window.addEventListener('hashchange', highlightTargetPost);
        applyStatefulDOMChanges();
    }
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}