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

            // Check if the server sent us a new challenge.
            if (result.newToken && result.newQuestion) {
                // Find the challenge elements in the main form.
                const tokenInput = form.querySelector('input[name="challenge_token"]');
                const questionSpan = document.getElementById('challenge-question');
                const answerInput = form.querySelector('input[name="challenge_answer"]');

                if (tokenInput && questionSpan) {
                    // Update the form with the new challenge data.
                    tokenInput.value = result.newToken;
                    questionSpan.textContent = result.newQuestion;
                    // Clear the user's old, incorrect answer.
                    if (answerInput) {
                        answerInput.value = ''; 
                    }
                }
            }

            return { error: result.error }; // Return the error message on failure
        }
        return result; // Return the JSON result on success

    } catch (error) {
        Modal.alert('Post Error', 'An unexpected network error occurred. Please try again.');
        return { error: "Network Error" }; // Return a generic error on failure
    } finally {
        submitButton.disabled = false;
        submitButton.value = originalButtonText;
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