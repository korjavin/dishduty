document.addEventListener('DOMContentLoaded', () => {
    const API_BASE_URL = '/api/dishduty';
    const PB_API_BASE_URL = '/api/collections';

    const currentAssigneeNameEl = document.getElementById('current-assignee-name');
    const calendarGridEl = document.getElementById('calendar-grid');
    const addToQueueForm = document.getElementById('add-to-queue-form');
    const workerSelectEl = document.getElementById('worker-select');

    const adminPasswordModal = document.getElementById('admin-password-modal');
    const adminPasswordInput = document.getElementById('admin-password-input');
    const adminPasswordSubmitBtn = document.getElementById('admin-password-submit');
    const adminActionFeedbackEl = document.getElementById('admin-action-feedback');
    const closeModalButton = document.querySelector('.modal .close-button');

    let adminPasswordCallback = null;

    // --- Modal Management ---
    function openAdminPasswordModal(callback) {
        adminPasswordCallback = callback;
        adminPasswordInput.value = '';
        adminActionFeedbackEl.textContent = '';
        adminPasswordModal.style.display = 'block';
        adminPasswordInput.focus();
    }

    function closeAdminPasswordModal() {
        adminPasswordModal.style.display = 'none';
        adminPasswordCallback = null;
    }

    closeModalButton.onclick = closeAdminPasswordModal;
    window.onclick = function(event) {
        if (event.target == adminPasswordModal) {
            closeAdminPasswordModal();
        }
    }

    adminPasswordSubmitBtn.onclick = async () => {
        const password = adminPasswordInput.value;
        if (!password) {
            adminActionFeedbackEl.textContent = 'Password cannot be empty.';
            adminActionFeedbackEl.style.color = 'red';
            return;
        }
        if (adminPasswordCallback) {
            adminActionFeedbackEl.textContent = 'Processing...';
            adminActionFeedbackEl.style.color = 'orange';
            await adminPasswordCallback(password);
        }
    };

    // --- API Helper ---
    async function fetchData(url, options = {}) {
        try {
            const response = await fetch(url, options);
            if (!response.ok) {
                const errorData = await response.json().catch(() => ({ message: response.statusText }));
                throw new Error(errorData.message || `HTTP error! status: ${response.status}`);
            }
            return await response.json();
        } catch (error) {
            console.error('Fetch error:', error);
            alert(`Error: ${error.message}`);
            throw error;
        }
    }

    // --- Fetch and Display Current Assignee ---
    async function fetchAndDisplayCurrentAssignee() {
        try {
            const data = await fetchData(`${API_BASE_URL}/current-assignee`);
            currentAssigneeNameEl.textContent = data.name || 'N/A';
        } catch (error) {
            currentAssigneeNameEl.textContent = 'Error loading';
        }
    }

    // --- Fetch and Display Calendar ---
    async function fetchAndDisplayCalendar() {
        try {
            const data = await fetchData(`${API_BASE_URL}/calendar`);
            renderCalendar(data.calendar);
        } catch (error) {
            calendarGridEl.innerHTML = '<p style="color:red;">Error loading calendar data.</p>';
        }
    }

    function renderCalendar(calendarDays) {
        calendarGridEl.innerHTML = ''; // Clear previous entries
        const today = new Date();
        today.setHours(0, 0, 0, 0); // Normalize today's date

        calendarDays.forEach(dayData => {
            const dayEl = document.createElement('div');
            dayEl.classList.add('calendar-day');

            const dateEl = document.createElement('div');
            dateEl.classList.add('date');
            const dayDate = new Date(dayData.date);
            dayDate.setHours(0,0,0,0); // Normalize for comparison

            dateEl.textContent = dayDate.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

            const assigneeEl = document.createElement('div');
            assigneeEl.classList.add('assignee');
            assigneeEl.textContent = dayData.assignee_name || 'Unassigned';

            if (dayData.is_current) {
                assigneeEl.classList.add('current');
            } else if (dayData.is_future_projection) {
                assigneeEl.classList.add('future');
            }

            dayEl.appendChild(dateEl);
            dayEl.appendChild(assigneeEl);

            // "Not Done" button for yesterday
            const yesterday = new Date(today);
            yesterday.setDate(today.getDate() - 1);

            if (dayDate.getTime() === yesterday.getTime() && !dayData.is_future_projection) {
                const notDoneButton = document.createElement('button');
                notDoneButton.classList.add('not-done-button');
                notDoneButton.textContent = 'Not Done';
                notDoneButton.onclick = () => handleNotDone(dayData.date);
                dayEl.appendChild(notDoneButton);
            }
            calendarGridEl.appendChild(dayEl);
        });
    }

    // --- "Not Done" Functionality ---
    function handleNotDone(dateString) {
        openAdminPasswordModal(async (adminPassword) => {
            try {
                await fetchData(`${API_BASE_URL}/mark-not-done`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ date: dateString, admin_password: adminPassword }),
                });
                adminActionFeedbackEl.textContent = 'Marked as not done successfully!';
                adminActionFeedbackEl.style.color = 'green';
                fetchAndDisplayCurrentAssignee();
                fetchAndDisplayCalendar();
                setTimeout(closeAdminPasswordModal, 1500);
            } catch (error) {
                adminActionFeedbackEl.textContent = `Error: ${error.message}`;
                adminActionFeedbackEl.style.color = 'red';
            }
        });
    }

    // --- Populate Worker Select ---
    async function populateWorkerSelect() {
        try {
            // Assuming PocketBase default API structure for listing records
            const data = await fetchData(`${PB_API_BASE_URL}/workers/records?sort=name`);
            workerSelectEl.innerHTML = ''; // Clear existing options
            if (data.items && data.items.length > 0) {
                data.items.forEach(worker => {
                    const option = document.createElement('option');
                    option.value = worker.id; // Use worker ID
                    option.textContent = worker.name;
                    workerSelectEl.appendChild(option);
                });
            } else {
                 workerSelectEl.innerHTML = '<option value="">No workers found</option>';
            }
        } catch (error) {
            workerSelectEl.innerHTML = '<option value="">Error loading workers</option>';
        }
    }

    // --- "Add to Queue" Functionality ---
    addToQueueForm.addEventListener('submit', (event) => {
        event.preventDefault();
        const workerId = workerSelectEl.value;
        const duration = parseInt(document.getElementById('duration-input').value, 10);

        if (!workerId) {
            alert('Please select a worker.');
            return;
        }
        if (isNaN(duration) || duration < 1 || duration > 7) {
            alert('Please enter a valid duration (1-7 days).');
            return;
        }

        openAdminPasswordModal(async (adminPassword) => {
            try {
                await fetchData(`${API_BASE_URL}/queue/add`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        worker_id: workerId,
                        days: duration,
                        admin_password: adminPassword
                    }),
                });
                adminActionFeedbackEl.textContent = 'Added to queue successfully!';
                adminActionFeedbackEl.style.color = 'green';
                fetchAndDisplayCalendar(); // Refresh calendar to show projection
                addToQueueForm.reset();
                setTimeout(closeAdminPasswordModal, 1500);
            } catch (error) {
                adminActionFeedbackEl.textContent = `Error: ${error.message}`;
                adminActionFeedbackEl.style.color = 'red';
            }
        });
    });

    // --- Initial Load ---
    function initializeApp() {
        fetchAndDisplayCurrentAssignee();
        fetchAndDisplayCalendar();
        populateWorkerSelect();
    }

    initializeApp();
});