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

    // --- Message Display Function ---
    // Updated displayMessage function
    function displayMessage(message, type = 'info', context = '') {
        const messageArea = document.getElementById('message-area');
        const fullMessage = context ? `${context}: ${message}` : message;

        if (!messageArea) {
            console.error('Message area element (#message-area) not found in HTML. Falling back to alert.');
            window.alert(`[${type.toUpperCase()}] ${fullMessage}`);
            return;
        }
        messageArea.textContent = fullMessage;
        messageArea.className = 'message-area'; // Reset classes
        messageArea.classList.add(`message-area--${type}`); // Apply type-specific class
        if (type === 'error') {
            messageArea.setAttribute('aria-live', 'assertive');
        } else {
            messageArea.setAttribute('aria-live', 'polite');
        }


        // Clear message after 7 seconds
        setTimeout(() => {
            if (messageArea.textContent === fullMessage) {
                messageArea.textContent = '';
                messageArea.className = 'message-area';
                messageArea.removeAttribute('aria-live');
            }
        }, 7000);
    }
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
            // displayMessage(`Error: ${error.message}`, 'error'); // Removed generic message
            throw error;
        }
    }

    // --- Fetch and Display Current Assignee ---
    function fetchCurrentAssignee() {
        const apiUrl = `${API_BASE_URL}/current-assignee`;
        console.log("Fetching current assignee from:", apiUrl);
        fetch(apiUrl)
            .then(response => {
                if (!response.ok) {
                    if (response.status === 404) {
                        return { worker_name: "N/A (None Assigned)" };
                    }
                    return response.json().then(errData => {
                        throw new Error(errData.message || `HTTP error! status: ${response.status}`);
                    });
                }
                return response.json();
            })
            .then(data => {
                if (currentAssigneeNameEl) {
                    currentAssigneeNameEl.textContent = data && data.worker_name ? data.worker_name : "N/A";
                    currentAssigneeNameEl.style.fontWeight = data && data.worker_name && data.worker_name !== "N/A" && data.worker_name !== "N/A (None Assigned)" ? "bold" : "normal";
                }
            })
            .catch(error => {
                console.error('Error fetching current assignee:', error);
                displayMessage(error.message || 'Could not load current assignee.', 'error', 'Current Assignee Load');
                if (currentAssigneeNameEl) {
                    currentAssigneeNameEl.textContent = "N/A (Error)";
                    currentAssigneeNameEl.style.fontWeight = "normal";
                }
            });
    }

    // --- Fetch and Display Calendar ---
    async function fetchAndDisplayCalendar() {
        try {
            const today = new Date();
            const startDate = new Date(today);
            startDate.setDate(today.getDate() - 7);
            const endDate = new Date(today);
            endDate.setDate(today.getDate() + 7);

            const formatDate = (date) => {
                const yyyy = date.getFullYear();
                const mm = String(date.getMonth() + 1).padStart(2, '0'); // Months are 0-indexed
                const dd = String(date.getDate()).padStart(2, '0');
                return `${yyyy}-${mm}-${dd}`;
            };

            const startDateStr = formatDate(startDate);
            const endDateStr = formatDate(endDate);

            const apiUrl = `${API_BASE_URL}/calendar?start_date=${startDateStr}&end_date=${endDateStr}`;
            console.log("Fetching calendar data from:", apiUrl); // Added for debugging

            const data = await fetchData(apiUrl);

            // Process data according to new structure { assignments: [], queued_assignments: [] }
            const allEntries = [];
            if (data && Array.isArray(data.assignments)) {
                data.assignments.forEach(item => {
                    allEntries.push({...item, type: 'assignment'});
                });
            } else {
                console.warn("data.assignments is not an array or is missing:", data ? data.assignments : data);
                // Optionally display a message to the user or part of the calendar
            }

            if (data && Array.isArray(data.queued_assignments)) {
                data.queued_assignments.forEach(item => {
                    allEntries.push({...item, type: 'queued'});
                });
            } else {
                console.warn("data.queued_assignments is not an array or is missing:", data ? data.queued_assignments : data);
            }
            
            // Sort entries by date (important if mixing assignments and queue items that might overlap)
            allEntries.sort((a, b) => new Date(a.date) - new Date(b.date));

            renderCalendar(allEntries);

        } catch (error) {
            console.error('Error fetching calendar data:', error);
            displayMessage(error.message || 'Could not load calendar data.', 'error', 'Calendar Load');
            calendarGridEl.innerHTML = '<p style="color:red;">Error loading calendar data.</p>';
        }
    }

    function renderCalendar(entries) { // Renamed parameter to 'entries'
        calendarGridEl.innerHTML = ''; // Clear previous entries
        const today = new Date();
        today.setHours(0, 0, 0, 0); // Normalize today's date
        const yesterday = new Date(today);
        yesterday.setDate(today.getDate() - 1);

        if (!entries || entries.length === 0) {
            calendarGridEl.innerHTML = '<p>No calendar data to display.</p>';
            return;
        }

        entries.forEach(item => { // Changed from dayData to item
            const dayEl = document.createElement('div');
            dayEl.classList.add('calendar-day');

            const dateEl = document.createElement('div');
            dateEl.classList.add('date');
            // item.date is already YYYY-MM-DD string from Go
            // To ensure correct Date object instantiation, especially across timezones,
            // it's safer to parse YYYY-MM-DD by splitting or ensuring UTC context if needed.
            // For local display, new Date(item.date) often works but can be tricky.
            // Let's assume item.date is "YYYY-MM-DD" and we want to treat it as local.
            // item.date is "YYYY-MM-DD HH:MM:SS.SSSZ" from PocketBase
            const itemDate = new Date(item.date); // Directly parse the ISO string
            itemDate.setHours(0,0,0,0); // Normalize for comparison

            dateEl.textContent = itemDate.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

            const assigneeEl = document.createElement('div');
            assigneeEl.classList.add('assignee');
            assigneeEl.textContent = item.worker_name || 'N/A'; // Use item.worker_name

            // Styling based on item.status and item.type
            if (item.type === 'queued') {
                assigneeEl.classList.add('queued');
                dayEl.classList.add('day-queued');
            } else if (item.type === 'assignment') {
                // Statuses from Go: "assigned", "past_done", "past_not_done"
                // (Original "done", "not_done" are transformed by backend for past dates)
                if (item.status === 'assigned') {
                    if (itemDate.getTime() === today.getTime()) {
                        assigneeEl.classList.add('current');
                        dayEl.classList.add('day-current');
                    } else if (itemDate.getTime() > today.getTime()) {
                        assigneeEl.classList.add('future');
                        dayEl.classList.add('day-future');
                    } else { // Past "assigned" (should ideally be "past_not_done" from backend)
                        assigneeEl.classList.add('past-unresolved');
                        dayEl.classList.add('day-past-unresolved');
                    }
                } else if (item.status === 'past_done') {
                    assigneeEl.classList.add('past-done');
                    dayEl.classList.add('day-past-done');
                } else if (item.status === 'past_not_done') {
                    assigneeEl.classList.add('past-not-done');
                    dayEl.classList.add('day-past-not-done');
                }
            }


            dayEl.appendChild(dateEl);
            dayEl.appendChild(assigneeEl);

            // "Not Done" button for assignments from yesterday that are not already "past_done" or "past_not_done"
            // The backend now sends specific "past_done" / "past_not_done" statuses.
            // The button should ideally be for *today's* task if it's 'assigned' or yesterday's if it was 'assigned'
            // Let's stick to the original logic: button for yesterday's assignment if its status allows action.
            // The backend status for yesterday would be 'past_done' or 'past_not_done'.
            // If yesterday's assignment was 'assigned' and not completed, backend sends 'past_not_done'.
            // If it was 'done', backend sends 'past_done'.
            // The "Not Done" button is to *mark* something as not done.
            // This implies it should be for an assignment that is currently "assigned" or "done".
            // The original logic was: `dayDate.getTime() === yesterday.getTime() && !dayData.is_future_projection`
            // Let's refine: Show "Not Done" for yesterday's *assignment* if its status was 'done' (to change it)
            // or if it was 'assigned' (meaning it became 'past_not_done' implicitly).
            // The `handleNotDone` function POSTs to `/api/dishduty/mark-not-done` which expects a date.
            // This endpoint in Go changes status to "not_done".
            // So, the button makes sense for an assignment on `yesterday` that is currently `assigned` or `done`.
            // The backend logic for `handleGetCalendarData` now sets status to `past_done` or `past_not_done`.
            // So, if `item.date` is yesterday, and `item.status` is `past_done`, we might offer to change it to `past_not_done`.
            // Or, if it's `assigned` (for today), offer to mark it `not_done` at end of day (handled by admin).
            // The original `handleNotDone` was for `dayData.date` (yesterday).
            // Let's keep the button for yesterday's assignment if it's not already 'past_not_done'.
            if (item.type === 'assignment' && itemDate.getTime() === yesterday.getTime() && item.status !== 'past_not_done') {
                const notDoneButton = document.createElement('button');
                notDoneButton.classList.add('not-done-button');
                notDoneButton.textContent = 'Mark Not Done';
                notDoneButton.onclick = () => handleNotDone(item.date); // Pass item.date
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
                displayMessage('Task marked as not done successfully. Calendar updated.', 'success', 'Mark Not Done');
                adminActionFeedbackEl.textContent = 'Marked as not done successfully!'; // Modal feedback
                adminActionFeedbackEl.style.color = 'green';
                fetchCurrentAssignee();
                fetchAndDisplayCalendar();
                setTimeout(closeAdminPasswordModal, 1500);
            } catch (error) {
                displayMessage(error.message || 'Failed to mark task as not done.', 'error', 'Mark Not Done');
                adminActionFeedbackEl.textContent = `Error: ${error.message}`; // Modal feedback
                adminActionFeedbackEl.style.color = 'red';
            }
        });
    }

    // --- Fetch Workers for Select ---
    async function fetchWorkers() {
        const apiUrl = `${API_BASE_URL}/workers`; // Use the new custom endpoint
        console.log("Fetching workers from:", apiUrl);

        try {
            const workers = await fetchData(apiUrl); // data is an array of worker records
            
            if (!workerSelectEl) {
                console.error("Worker select element not found");
                return;
            }

            workerSelectEl.innerHTML = '<option value="">Select Worker</option>'; // Clear existing options
            
            if (workers && workers.length > 0) {
                workers.forEach(worker => {
                    const option = document.createElement('option');
                    option.value = worker.id;
                    option.textContent = worker.name;
                    workerSelectEl.appendChild(option);
                });
            } else {
                 workerSelectEl.innerHTML = '<option value="">No workers found</option>';
                 displayMessage('No workers found in the database.', 'info', 'Workers List Load');
            }
        } catch (error) {
            console.error('Error fetching workers:', error);
            displayMessage(error.message || 'Could not load workers list.', 'error', 'Workers List Load');
            if (workerSelectEl) {
                workerSelectEl.innerHTML = '<option value="">Error loading workers</option>';
            }
        }
    }

    // --- "Add to Queue" Functionality ---
    addToQueueForm.addEventListener('submit', (event) => {
        event.preventDefault();
        const workerId = workerSelectEl.value;
        const durationDays = parseInt(document.getElementById('duration-input').value, 10);

        if (!workerId) {
            displayMessage('Please select a worker.', 'error', 'Add Duty Validation');
            return;
        }
        if (isNaN(durationDays) || durationDays < 1 || durationDays > 7) {
            displayMessage('Duration must be a number between 1 and 7.', 'error', 'Add Duty');
            return;
        }

        openAdminPasswordModal(async (adminPassword) => {
            try {
                const payload = {
                    worker_id: workerId,
                    duration_days: durationDays,
                    admin_password: adminPassword
                };
                await fetchData(`${API_BASE_URL}/queue/add`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload),
                });
                displayMessage('Duty added to queue successfully!', 'success', 'Add Duty');
                adminActionFeedbackEl.textContent = 'Added to queue successfully!'; // Modal feedback
                adminActionFeedbackEl.style.color = 'green';
                fetchAndDisplayCalendar(); // Refresh calendar to show projection
                addToQueueForm.reset();
                setTimeout(closeAdminPasswordModal, 1500);
            } catch (error) {
                displayMessage(error.message || 'Failed to add duty.', 'error', 'Add Duty');
                adminActionFeedbackEl.textContent = `Error: ${error.message}`; // Modal feedback
                adminActionFeedbackEl.style.color = 'red';
            }
        });
    });

    // --- Initial Load ---
    function initializeApp() {
        fetchCurrentAssignee();
        fetchAndDisplayCalendar();
        fetchWorkers();
    }

    initializeApp();
});