// frontend/modal.js

export default class Modal {
    constructor(title, content, buttons = []) {
        this.title = title;
        this.content = content;
        this.buttons = buttons;
        this.modalEl = null;
    }

    show() {
        this.close(); // Ensure no other modals are open
        const buttonHTML = this.buttons.map(btn =>
            `<button type="button" class="${btn.class || ''}" id="${btn.id}">${btn.text}</button>`
        ).join('');

        const modalHTML = `
            <div class="modal-backdrop">
                <div class="modal-content">
                    <h3 class="modal-title">${this.title}</h3>
                    <div class="modal-body">${this.content}</div>
                    <div class="modal-buttons">${buttonHTML}</div>
                </div>
            </div>`;
        document.body.insertAdjacentHTML('beforeend', modalHTML);
        this.modalEl = document.querySelector('.modal-backdrop');

        this.buttons.forEach(btn => {
            const btnEl = document.getElementById(btn.id);
            if (btnEl) {
                btnEl.addEventListener('click', (e) => {
                    if (btn.onClick) {
                        btn.onClick(e, this);
                    } else {
                        this.close();
                    }
                });
            }
        });

        this.modalEl.addEventListener('click', e => {
            if (e.target === this.modalEl) {
                this.close();
            }
        });
    }

        close() {

            const existingModal = document.querySelector('.modal-backdrop');

            if (existingModal) {

                existingModal.remove();

            }

        }

    

        static escapeHTML(str) {

            if (!str) return '';

            return String(str).replace(/[&<>'"]/g, 

                tag => ({

                    '&': '&amp;',

                    '<': '&lt;',

                    '>': '&gt;',

                    "'": '&#39;',

                    '"': '&quot;'

                }[tag]));

        }

    

        static alert(title, message) {

            new Modal(title, `<p>${Modal.escapeHTML(message)}</p>`, [{ id: 'modal-ok', text: 'OK', class: 'button-primary' }]).show();

        }

    }

    