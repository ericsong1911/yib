// frontend/api.js

import Modal from './modal.js';
import { localizeTimestamps } from './dom.js';

export async function submitPostForm(form) {
    const submitButton = form.querySelector('input[type="submit"]');
    const originalButtonText = submitButton.value;
    submitButton.disabled = true;
    submitButton.value = 'Posting...';

    try {
        const formData = new FormData(form);
        const response = await fetch('/post', {
            method: 'POST',
            body: formData,
        });
        const result = await response.json();

        if (!response.ok) {
            const errorContainer = document.createElement('div');
            errorContainer.innerHTML = result.error;
            Modal.alert('Post Error', errorContainer.innerHTML);
            localizeTimestamps(); // Run on new modal content
            return null; // Return null on failure
        }
        return result; // Return the JSON result on success

    } catch (error) {
        Modal.alert('Post Error', 'An unexpected network error occurred. Please try again.');
        return null; // Return null on failure
    } finally {
        // The caller will be responsible for re-enabling the button
        submitButton.disabled = false;
        submitButton.value = originalButtonText;
    }
}

export async function fetchNewChallenge() {
    try {
        const response = await fetch('/api/challenge');
        if (!response.ok) return null;
        return await response.json();
    } catch (error) {
        console.error("Failed to fetch new challenge:", error);
        return null;
    }
}

export async function fetchPostPreviewHTML(numericId) {
    try {
        const response = await fetch(`/api/post/${numericId}`);
        if (!response.ok) return null;
        return await response.text();
    } catch (error) {
        console.error("Failed to fetch post preview:", error);
        return null;
    }
}

export async function handleApiSubmit(modal, url, data, successMessage) {
    if (data.reason && !data.reason.trim()) {
        Modal.alert('Error', 'A reason is required.');
        return;
    }
    try {
        const formData = new URLSearchParams(data);
        const response = await fetch(url, { method: 'POST', body: formData });
        const result = await response.json();
        if (!response.ok) throw new Error(result.error);
        modal.close();
        Modal.alert('Success', successMessage || result.success);
    } catch (error) {
        modal.close();
        Modal.alert('Error', error.message);
    }
}