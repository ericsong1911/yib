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
            localizeTimestamps();

            if (result.newToken && result.newQuestion) {
                const tokenInput = form.querySelector('input[name="challenge_token"]');
                const questionSpan = document.getElementById('challenge-question');
                const answerInput = form.querySelector('input[name="challenge_answer"]');

                if (tokenInput && questionSpan) {
                    tokenInput.value = result.newToken;
                    questionSpan.textContent = result.newQuestion;
                    if (answerInput) {
                        answerInput.value = '';
                    }
                }
            }

            return { error: result.error };
        }
        return result;

    } catch (error) {
        Modal.alert('Post Error', 'An unexpected network error occurred. Please try again.');
        return { error: "Network Error" };
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