export function initEmojiPicker() {
    const toggleBtn = document.getElementById('emoji-toggle');
    const textarea = document.querySelector('textarea[name="content"]');
    
    if (!toggleBtn || !textarea) return;

    // Create picker container dynamically
    const picker = document.createElement('div');
    picker.id = 'emoji-picker';
    picker.className = 'emoji-picker hidden';
    picker.style.display = 'none';
    
    // Simple emoji list
    const emojis = [
        '😀', '😂', '🤣', '😉', '😊', '😍', '🤔', '😐', '😑', '😶',
        '🙄', '😏', '😣', '😥', '😮', '😫', '😴', '😌', '😛', '😜',
        '😒', '😓', '😔', '😕', '😖', '🙃', '😷', '🤒', '🤕', '🤑',
        '😲', '😞', '😟', '😤', '😢', '😭', '😦', '😧', '😨', '😩',
        '😬', '😰', '😱', '😳', '😵', '😡', '😠', '😈', '💀', '👽',
        '👍', '👎', '👌', '👊', '✊', '✌️', '👋', '✋', '👐', '🙏'
    ];

    emojis.forEach(emoji => {
        const span = document.createElement('span');
        span.textContent = emoji;
        span.className = 'emoji-item';
        span.style.cursor = 'pointer';
        span.style.padding = '5px';
        span.style.fontSize = '1.2em';
        
        span.addEventListener('click', () => {
            insertAtCursor(textarea, emoji);
            picker.style.display = 'none';
        });
        
        picker.appendChild(span);
    });

    // Position the picker near the button
    toggleBtn.parentNode.insertBefore(picker, toggleBtn.nextSibling);

    toggleBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        if (picker.style.display === 'none') {
            picker.style.display = 'grid';
            picker.style.gridTemplateColumns = 'repeat(10, 1fr)';
            picker.style.gap = '5px';
            picker.style.position = 'absolute';
            picker.style.backgroundColor = 'var(--bg-color, #fff)';
            picker.style.border = '1px solid var(--border-color, #ccc)';
            picker.style.padding = '5px';
            picker.style.zIndex = '1000';
            picker.style.maxWidth = '300px';
        } else {
            picker.style.display = 'none';
        }
    });

    // Close on click outside
    document.addEventListener('click', (e) => {
        if (!picker.contains(e.target) && e.target !== toggleBtn) {
            picker.style.display = 'none';
        }
    });
}

function insertAtCursor(field, value) {
    if (field.selectionStart || field.selectionStart === '0') {
        const startPos = field.selectionStart;
        const endPos = field.selectionEnd;
        field.value = field.value.substring(0, startPos) + value + field.value.substring(endPos, field.value.length);
        field.selectionStart = startPos + value.length;
        field.selectionEnd = startPos + value.length;
    } else {
        field.value += value;
    }
    field.focus();
}
