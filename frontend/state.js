// frontend/state.js

export const state = {
    hiddenThreads: {},
    autoRefreshTimer: null,
    refreshCountdownValue: 0,
    isUserActionInProgress: false,
    postPreviewCache: new Map(),
    previewTimeout: null,
    previewDiv: null,
};

export const config = {
    previewDelay: 150,
    imageMaxWidth: '250px',
    imageMaxHeight: '250px',
    imageExpandedMaxWidth: '90vw',
    imageExpandedMaxHeight: '90vh',
};