document.addEventListener('DOMContentLoaded', () => {
    const auditLogTableBody = document.querySelector('#auditLogTable tbody');

    async function fetchAuditLog() {
        try {
            // Assuming PocketBase is running on the default port 8090
            // and accessible at the same host.
            // Adjust '/api/' if your PocketBase instance is hosted elsewhere
            // or if you have a reverse proxy with a different base path.
            const response = await fetch('/api/collections/action_log/records?sort=-created');
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            const data = await response.json();
            displayAuditLog(data.items);
        } catch (error) {
            console.error('Error fetching audit log:', error);
            if (auditLogTableBody) {
                const row = auditLogTableBody.insertRow();
                const cell = row.insertCell();
                cell.colSpan = 3;
                cell.textContent = 'Error loading audit log. See console for details.';
                cell.style.textAlign = 'center';
            }
        }
    }

    function displayAuditLog(logEntries) {
        if (!auditLogTableBody) {
            console.error('Audit log table body not found');
            return;
        }
        auditLogTableBody.innerHTML = ''; // Clear existing entries

        if (!logEntries || logEntries.length === 0) {
            const row = auditLogTableBody.insertRow();
            const cell = row.insertCell();
            cell.colSpan = 3;
            cell.textContent = 'No audit log entries found.';
            cell.style.textAlign = 'center';
            return;
        }

        logEntries.forEach(entry => {
            const row = auditLogTableBody.insertRow();

            const timestampCell = row.insertCell();
            // PocketBase 'created' field is in 'YYYY-MM-DD HH:MM:SS.mmmZ' format
            // We can format it for better readability.
            const date = new Date(entry.created);
            timestampCell.textContent = date.toLocaleString('sv-SE', { // Using a locale that gives YYYY-MM-DD HH:MM:SS
                year: 'numeric',
                month: '2-digit',
                day: '2-digit',
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit'
            });

            const actionTypeCell = row.insertCell();
            actionTypeCell.textContent = entry.action_type || 'N/A';

            const detailsCell = row.insertCell();
            detailsCell.textContent = entry.details || '';
        });
    }

    fetchAuditLog();
});