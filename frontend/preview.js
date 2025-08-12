// frontend/preview.js

import { state, config } from './state.js';
import { fetchPostPreviewHTML } from './api.js';

function createPreviewDiv() {
    const div = document.createElement('div');
    div.id = 'post-preview';
    document.body.appendChild(div);
    return div;
}

function positionPreview(e) {
    if (!state.previewDiv) return;

    const { innerWidth: vw, innerHeight: vh } = window;
    const { width: pw, height: ph } = state.previewDiv.getBoundingClientRect();
    const margin = 15;

    let x = e.clientX + margin;
    let y = e.clientY + margin;

    if (x + pw > vw - margin) x = e.clientX - pw - margin;
    if (y + ph > vh - margin) y = e.clientY - ph - margin;
    if (x < margin) x = margin;
    if (y < margin) y = margin;

    state.previewDiv.style.left = `${x}px`;
    state.previewDiv.style.top = `${y}px`;
}

function displayPreview(node, event) {
    if (!state.previewDiv) state.previewDiv = createPreviewDiv();
    state.previewDiv.innerHTML = '';
    state.previewDiv.appendChild(node);
    positionPreview(event);
    state.previewDiv.style.display = 'block';
}

async function fetchAndDisplayPreview(postId, event) {
    const numericId = postId.substring(1); // p123 -> 123
    
    // Use cache if available
    if (state.postPreviewCache.has(numericId)) {
        displayPreview(state.postPreviewCache.get(numericId).cloneNode(true), event);
        return;
    }

    try {
        const html = await fetchPostPreviewHTML(numericId);
        if (!html) return;

        const tempDiv = document.createElement('div');
        tempDiv.innerHTML = html;
        const postNode = tempDiv.firstElementChild;

        if (postNode) {
            state.postPreviewCache.set(numericId, postNode.cloneNode(true));
            displayPreview(postNode, event);
        }
    } catch (error) {
        console.error("Failed to fetch and display post preview:", error);
    }
}

export function showPreview(e) {
    clearTimeout(state.previewTimeout);
    state.previewTimeout = setTimeout(() => {
        const link = e.target;
        const postId = link.hash.substring(1); // #p123 -> p123
        if (!postId) return;

        const post = document.getElementById(postId);
        if (post) {
            // Post is on the current page, clone it
            displayPreview(post.cloneNode(true), e);
        } else {
            // Post is not on the page, fetch it via API
            fetchAndDisplayPreview(postId, e);
        }
    }, config.previewDelay);
}

export function hidePreview() {
    clearTimeout(state.previewTimeout);
    if (state.previewDiv) state.previewDiv.style.display = 'none';
}