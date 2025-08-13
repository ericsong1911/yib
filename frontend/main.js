// frontend/main.js

import Modal from './modal.js';
import { state } from './state.js';
import { submitPostForm, handleApiSubmit, fetchNewChallenge } from './api.js';
import {
    applyStatefulDOMChanges,
    toggleImageSize,
    toggleThreadVisibility,
    highlightTargetPost,
    quote
} from './dom.js';
import { showPreview, hidePreview } from './preview.js';

function saveNameToHistory(name) {
    let history = JSON.parse(localStorage.getItem('yib_nameHistory')) || [];
    history = history.filter(item => item !== name);
    history.unshift(name);
    if (history.length > 4) history.length = 4;
    localStorage.setItem('yib_nameHistory', JSON.stringify(history));
}

async function refreshContent() {
    if (state.isUserActionInProgress) {
        return;
    }
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

function showReportModal(postId) {
    const csrfToken = document.querySelector('#postform-csrf-token')?.value;
    if (!csrfToken) return Modal.alert('Error', 'Could not find security token.');
    const content = `<form id="report-form-modal"><textarea name="reason" class="modal-textarea" placeholder="Reason..." required></textarea></form>`;
    new Modal(`Report Post No. ${postId}`, content, [
        { id: 'report-cancel', text: 'Cancel' },
        { id: 'report-submit', text: 'Submit', class: 'button-primary', onClick: (e, modal) => handleApiSubmit(modal, '/report', { post_id: postId, reason: modal.modalEl.querySelector('textarea').value, csrf_token: csrfToken }, "Report submitted successfully.") }
    ]).show();
}

function showBanModal(link) {
    const { ipHash, cookieHash, csrfToken } = link.dataset;
    const content = `
        <form id="ban-form-modal" class="modal-form">
            <label for="ban-reason">Reason:</label>
            <input type="text" id="ban-reason" name="reason" required>
            <label for="ban-duration">Duration (hours, 0=perm):</label>
            <input type="number" id="ban-duration" name="duration" value="0" min="0">
        </form>`;
    new Modal('Apply Ban', content, [
        { id: 'ban-cancel', text: 'Cancel' },
        { id: 'ban-submit', text: 'Apply Ban', class: 'button-danger', onClick: (e, modal) => {
            const reason = modal.modalEl.querySelector('#ban-reason').value;
            const duration = modal.modalEl.querySelector('#ban-duration').value;
            handleApiSubmit(modal, '/mod/ban', { ip_hash: ipHash, cookie_hash: cookieHash, reason, duration, csrf_token: csrfToken }, "Ban successfully applied.");
        }}
    ]).show();
}

function handleGlobalClick(e) {
    const target = e.target;
    if (target.classList.contains('postImg')) {
        e.preventDefault();
        toggleImageSize(target);
    } else if (target.classList.contains('hide-thread-link')) {
        e.preventDefault();
        const boardId = document.body.dataset.boardId;
        toggleThreadVisibility(boardId, target.dataset.threadId);
    } else if (target.classList.contains('report-link')) {
        e.preventDefault();
        showReportModal(target.dataset.postId);
    } else if (target.classList.contains('ban-link')) {
        e.preventDefault();
        showBanModal(target);
    } else if (target.classList.contains('js-quote-link')) {
        e.preventDefault();
        quote(target.dataset.postId);
    }
}

function handleGlobalMouseOver(e) {
    if (e.target.classList.contains('backlink')) {
        showPreview(e);
    }
}

function handleGlobalMouseOut(e) {
    if (e.target.classList.contains('backlink')) {
        hidePreview();
    }
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
        if (theme === 'default' || !theme) {
            theme = defaultTheme;
        }
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

function init() {
    document.addEventListener('click', handleGlobalClick);
    document.addEventListener('mouseover', handleGlobalMouseOver);
    document.addEventListener('mouseout', handleGlobalMouseOut);
    window.addEventListener('hashchange', highlightTargetPost);

    const postForm = document.querySelector('.postForm > form');
    postForm?.addEventListener('submit', async (e) => {
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
                window.location.assign(result.redirect);
                window.location.reload();
            } else {
                window.location.assign(result.redirect);
            }
            return;
        }

        if (result && result.error && result.error.includes("Invalid challenge answer")) {
            const newChallenge = await fetchNewChallenge();
            if (newChallenge) {
                const tokenInput = postForm.querySelector('input[name="challenge_token"]');
                const answerInput = postForm.querySelector('input[name="challenge_answer"]');
                const questionEl = document.getElementById('challenge-question');

                if (tokenInput && answerInput && questionEl) {
                    tokenInput.value = newChallenge.token;
                    questionEl.textContent = newChallenge.question;
                    answerInput.value = '';
                }
            }
        }
        
        state.isUserActionInProgress = false;
        const checkbox = document.getElementById('auto-refresh-checkbox');
        if (checkbox && checkbox.checked) {
            startAutoRefresh();
        }
    });

    initAutoRefresh();
    initThemeSwitcher();
    initNameHistory();
    applyStatefulDOMChanges();
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}