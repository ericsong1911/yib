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
    const margin = 15; // Space from the edge of the screen

    let x = e.clientX + margin;
    let y = e.clientY + margin;

    // If it goes off the right edge, flip it to the left of the cursor.
    if (x + pw > vw) {
        x = e.clientX - pw - margin;
    }

    // If it goes off the bottom edge, flip it to the top of the cursor.
    if (y + ph > vh) {
        y = e.clientY - ph - margin;
    }

    // If it now goes off the left edge (e.g., cursor is far left), clamp it.
    if (x < 0) {
        x = margin;
    }

    // If it now goes off the top edge (e.g., cursor is at the top), clamp it.
    if (y < 0) {
        y = margin;
    }

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