// frontend/dom.js

import { state, config } from './state.js';

export function localizeTimestamps() {
    const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
    const pad = num => String(num).padStart(2, '0');

    document.querySelectorAll('time.post-time').forEach(el => {
        const isoString = el.getAttribute('datetime');
        if (!isoString || el.dataset.localized === 'true') return;

        const date = new Date(isoString);
        if (isNaN(date)) return;

        const year = String(date.getFullYear()).slice(-2);
        const month = pad(date.getMonth() + 1);
        const day = pad(date.getDate());
        const dayOfWeek = days[date.getDay()];
        const hours = pad(date.getHours());
        const minutes = pad(date.getMinutes());
        const seconds = pad(date.getSeconds());

        el.textContent = `${month}/${day}/${year}(${dayOfWeek})${hours}:${minutes}:${seconds}`;
        el.dataset.localized = 'true';
    });
}

export function highlightTargetPost() {
    document.querySelectorAll('.post.highlight').forEach(el => el.classList.remove('highlight'));
    if (window.location.hash) {
        try {
            const target = document.querySelector(window.location.hash);
            if (target && target.classList.contains('post')) {
                target.classList.add('highlight');
            }
        } catch (e) { /* Ignore invalid selectors */ }
    }
}

// The quote function is now a standard module export.
export function quote(postNum) {
    const textarea = document.querySelector('textarea[name="content"]');
    if (!textarea) return; // Return early if no textarea is found
    const quoteText = '>>' + postNum + '\n';
    textarea.focus();

    if (typeof textarea.selectionStart === 'number') {
        const start = textarea.selectionStart;
        const end = textarea.selectionEnd;
        textarea.value = textarea.value.substring(0, start) + quoteText + textarea.value.substring(end);
        textarea.selectionStart = textarea.selectionEnd = start + quoteText.length;
    } else {
        textarea.value += quoteText;
    }
    textarea.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

export function toggleImageSize(img) {
    if (!img || img.tagName !== 'IMG') {
        return;
    }
    const parentLink = img.closest('a');
    if (!parentLink) {
        return;
    }

    const isExpanded = img.dataset.expanded === 'true';

    if (isExpanded) {
        // --- COLLAPSE LOGIC ---
        img.style.maxWidth = '';
        img.style.maxHeight = '';
        img.dataset.expanded = 'false';

        setTimeout(() => {
            if (img.dataset.expanded === 'false' && img.dataset.thumbSrc) {
                img.src = img.dataset.thumbSrc;
            }
        }, 300);
    } else {
        // --- EXPAND LOGIC ---
        img.dataset.thumbSrc = img.src;
        img.src = parentLink.href;
        img.style.maxWidth = config.imageExpandedMaxWidth;
        img.style.maxHeight = config.imageExpandedMaxHeight;
        img.dataset.expanded = 'true';
    }
}

export function loadHiddenThreads() {
    try {
        state.hiddenThreads = JSON.parse(localStorage.getItem('hiddenThreads')) || {};
    } catch (e) {
        state.hiddenThreads = {};
    }
}

export function applyHiddenThreads() {
    const boardId = document.body.dataset.boardId;
    if (!boardId || !state.hiddenThreads[boardId]) return;
    state.hiddenThreads[boardId].forEach(threadId => {
        const threadEl = document.getElementById('t' + threadId);
        if (threadEl) {
            threadEl.classList.add('collapsed');
            const linkEl = threadEl.querySelector('.hide-thread-link');
            if (linkEl) linkEl.textContent = 'Show';
        }
    });
}

export function toggleThreadVisibility(boardId, threadId) {
    if (!boardId || !threadId) return;
    if (!state.hiddenThreads[boardId]) state.hiddenThreads[boardId] = [];

    const threadEl = document.getElementById('t' + threadId);
    const linkEl = threadEl?.querySelector(`.hide-thread-link[data-thread-id='${threadId}']`);
    const index = state.hiddenThreads[boardId].indexOf(threadId);

    if (index > -1) {
        state.hiddenThreads[boardId].splice(index, 1);
        threadEl?.classList.remove('collapsed');
        if(linkEl) linkEl.textContent = 'Hide';
    } else {
        state.hiddenThreads[boardId].push(threadId);
        threadEl?.classList.add('collapsed');
        if(linkEl) linkEl.textContent = 'Show';
    }
    localStorage.setItem('hiddenThreads', JSON.stringify(state.hiddenThreads));
}

export function applyStatefulDOMChanges() {
    loadHiddenThreads();
    applyHiddenThreads();
    highlightTargetPost();
    localizeTimestamps();
}