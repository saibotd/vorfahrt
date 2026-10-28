async function loadAccounts() {
    const res = await fetch('/api/accounts');
    const data = await res.json();
    const tbody = document.querySelector('#accounts-table tbody');
    tbody.innerHTML = '';
    for (let acc of data) {
        tbody.innerHTML += `<tr>
            <td>${acc.id}</td>
            <td>${acc.domain || '<i>none</i>'}</td>
            <td>${acc.username}</td>
            <td><code>${acc.password}</code> <button class="btn-copy" onclick="copyPassword(this, '${acc.password}')">Copy</button></td>
            <td><button class="btn-sm" onclick="deleteAccount(${acc.id})">Delete</button></td>
        </tr>`;
    }
}

function copyPassword(btn, text) {
    navigator.clipboard.writeText(text).then(() => {
        const orig = btn.textContent;
        btn.textContent = 'Copied!';
        setTimeout(() => btn.textContent = orig, 1500);
    }).catch(() => {
        prompt('Password:', text);
    });
}

async function addAccount() {
    const domain = document.getElementById('account-domain').value;
    const u = document.getElementById('username').value.trim();
    if (!domain) {
        alert('Please select a domain.');
        return;
    }
    if (!u) {
        alert('Please enter a username.');
        return;
    }
    const res = await fetch('/api/accounts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ domain: domain, username: u })
    });
    if (!res.ok) {
        const err = await res.text();
        alert('Error creating account: ' + err);
        return;
    }
    document.getElementById('username').value = '';
    loadAccounts();
}

async function deleteAccount(id) {
    await fetch('/api/accounts/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id })
    });
    loadAccounts();
}

function escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;')
              .replace(/</g, '&lt;')
              .replace(/>/g, '&gt;')
              .replace(/"/g, '&quot;')
              .replace(/'/g, '&#039;');
}

async function loadMessages() {
    const res = await fetch('/api/messages');
    const data = await res.json();
    const tbody = document.querySelector('#queue-table tbody');
    tbody.innerHTML = '';

    if (!data || data.length === 0) {
        tbody.innerHTML = '<tr><td colspan="8"><i>No messages in queue</i></td></tr>';
        return;
    }

    for (let msg of data) {
        const statusBadge = `<span class="badge badge-${msg.status}">${msg.status}</span>`;
        const subjectText = msg.subject ? escapeHtml(msg.subject) : '<i class="text-muted">(no subject)</i>';

        tbody.innerHTML += `<tr class="clickable-row" onclick="showMessageDetail(${msg.id})">
            <td>${msg.id}</td>
            <td>${subjectText}</td>
            <td>${escapeHtml(msg.sender)}</td>
            <td>${escapeHtml(msg.recipient)}</td>
            <td>${statusBadge}</td>
            <td>${msg.attempts || 0}</td>
            <td>${new Date(msg.created_at).toLocaleString()}</td>
            <td><button class="btn-sm" onclick="event.stopPropagation(); showMessageDetail(${msg.id})">Details</button></td>
        </tr>`;
    }
}

async function showMessageDetail(id) {
    const dialog = document.getElementById('message-dialog');
    const title = document.getElementById('dialog-title');
    const body = document.getElementById('dialog-body');

    title.textContent = `Message #${id}`;
    body.innerHTML = '<span class="text-muted">Loading message details...</span>';
    if (!dialog.open) {
        dialog.showModal();
    }

    try {
        const res = await fetch(`/api/messages/detail?id=${id}`);
        if (!res.ok) {
            body.innerHTML = '<span class="status-err">Failed to load message details.</span>';
            return;
        }

        const msg = await res.json();
        title.innerHTML = `Message #${msg.id} <span class="badge badge-${msg.status}" style="margin-left: 10px;">${msg.status}</span>`;

        let html = `
            <table class="detail-table">
                <tr><th>Status</th><td><span class="badge badge-${msg.status}">${msg.status}</span></td></tr>
                <tr><th>Subject</th><td><b>${msg.subject ? escapeHtml(msg.subject) : '<i>(no subject)</i>'}</b></td></tr>
                <tr><th>From</th><td><code>${escapeHtml(msg.sender)}</code></td></tr>
                <tr><th>To</th><td><code>${escapeHtml(msg.recipient)}</code></td></tr>
                <tr><th>Attempts</th><td>${msg.attempts || 0} / 5</td></tr>
                <tr><th>Submitted</th><td>${new Date(msg.created_at).toLocaleString()}</td></tr>
                <tr><th>Last Updated</th><td>${new Date(msg.updated_at).toLocaleString()}</td></tr>
                ${msg.size_bytes > 0 ? `<tr><th>Payload Size</th><td>${msg.size_bytes} bytes</td></tr>` : ''}
            </table>
        `;

        if (msg.error) {
            html += `
                <h4 style="margin: 15px 0 6px 0;" class="status-err">Delivery Error</h4>
                <div class="code-box status-err">${escapeHtml(msg.error)}</div>
            `;
        }

        if (msg.headers && Object.keys(msg.headers).length > 0) {
            html += `<h4 style="margin: 15px 0 6px 0;">Headers</h4><table class="detail-table">`;
            for (let [k, v] of Object.entries(msg.headers)) {
                html += `<tr><th style="width: 160px;">${escapeHtml(k)}</th><td><code>${escapeHtml(v)}</code></td></tr>`;
            }
            html += `</table>`;
        }

        if (msg.body_preview) {
            html += `
                <h4 style="margin: 15px 0 6px 0;">Body Preview</h4>
                <div class="code-box">${escapeHtml(msg.body_preview)}</div>
            `;
        } else if (!msg.has_data && msg.status === 'sent') {
            html += `
                <p class="text-muted" style="margin-top: 15px; font-size: 13px;">
                    <i>Note:</i> Raw email payload was cleared after successful delivery to preserve disk space.
                </p>
            `;
        }

        body.innerHTML = html;
    } catch (e) {
        body.innerHTML = '<span class="status-err">Error loading message details.</span>';
    }
}

function closeMessageDetail() {
    const dialog = document.getElementById('message-dialog');
    if (dialog && dialog.open) {
        dialog.close();
    }
}

function closeDnsDialog() {
    const dialog = document.getElementById('dns-dialog');
    if (dialog && dialog.open) {
        dialog.close();
    }
}

// Close dialogs when clicking on their backdrops
document.addEventListener('DOMContentLoaded', () => {
    setupDialogBackdrops();
});

function setupDialogBackdrops() {
    document.querySelectorAll('dialog').forEach((dlg) => {
        dlg.addEventListener('click', (event) => {
            const rect = dlg.getBoundingClientRect();
            const clickedInDialog = (
                rect.top <= event.clientY &&
                event.clientY <= rect.top + rect.height &&
                rect.left <= event.clientX &&
                event.clientX <= rect.left + rect.width
            );
            if (!clickedInDialog) {
                dlg.close();
            }
        });
    });
}
setupDialogBackdrops();

async function loadServerStatus(recheck = false) {
    const container = document.getElementById('server-status-container');
    if (recheck) {
        container.innerHTML = '<span class="text-muted">Checking server status...</span>';
    }
    try {
        const url = recheck ? '/api/server-status?recheck=true' : '/api/server-status';
        const res = await fetch(url);
        const st = await res.json();

        let fcrdnsHtml = '';
        if (st.fcrdns_status === 'ok') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${st.smtp_domain}</code> (IP: <code>${st.ip}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <code>${st.ptr}</code> <span class="status-ok">&#10004; Valid FCrDNS</span></p>
                <small class="text-muted">Forward-Confirmed Reverse DNS is verified. Recipient mail servers (Gmail, Outlook, etc.) will accept outbound connections from this IP.</small>
            `;
        } else if (st.fcrdns_status === 'mismatch') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${st.smtp_domain}</code> (IP: <code>${st.ip}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <code>${st.ptr}</code> <span class="status-warn">&#9888; Mismatch</span></p>
                <small class="status-warn"><b>Action Required:</b> In your VPS / hosting control panel (Hetzner, Netcup, DigitalOcean, etc.), set the Reverse DNS (PTR) for <code>${st.ip}</code> to <code>${st.smtp_domain}</code>.</small>
            `;
        } else if (st.fcrdns_status === 'missing_ptr') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${st.smtp_domain}</code> (IP: <code>${st.ip}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <span class="status-err">&#10006; Missing PTR Record</span></p>
                <small class="status-err"><b>Action Required:</b> Set the Reverse DNS (PTR) for <code>${st.ip}</code> to <code>${st.smtp_domain}</code> in your hosting provider panel.</small>
            `;
        } else if (st.fcrdns_status === 'missing_a') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${st.smtp_domain}</code> <span class="status-err">&#10006; Missing DNS A Record</span></p>
                <small class="status-err"><b>Action Required:</b> Create an A record for <code>${st.smtp_domain}</code> pointing to your server's public IP.</small>
            `;
        } else {
            fcrdnsHtml = `
                <p class="status-line text-muted"><code>SMTP_DOMAIN</code> is currently set to <code>${st.smtp_domain || 'localhost'}</code>.</p>
                <small class="text-muted">Set <code>SMTP_DOMAIN=mail.yourdomain.com</code> in your environment / docker-compose to establish your server's canonical sending identity.</small>
            `;
        }

        let outboundHtml = '';
        if (st.outbound_port_25_status === 'ok') {
            outboundHtml = `
                <div class="outbound-status-box">
                    <p class="status-line"><b>Outbound SMTP (Port 25):</b> <span class="status-ok">&#10004; Open / Reachable</span> <small class="text-muted">(Target: <code>${st.outbound_port_25_target || 'portquiz.net:25'}</code>)</small></p>
                    <small class="text-muted">Your server can establish outgoing TCP connections on port 25 to deliver email to remote mail servers.</small>
                </div>
            `;
        } else if (st.outbound_port_25_status === 'blocked') {
            outboundHtml = `
                <div class="outbound-status-box">
                    <p class="status-line"><b>Outbound SMTP (Port 25):</b> <span class="status-err">&#10006; Blocked / Unreachable</span></p>
                    <small class="status-err"><b>Action Required:</b> Outbound traffic on TCP port 25 is blocked. Most hosting providers (Hetzner, AWS, DigitalOcean, Linode, etc.) block port 25 by default to prevent spam. You must request port 25 unblocking in your provider's support/console or adjust your firewall before emails can be delivered.</small>
                </div>
            `;
        }

        container.innerHTML = fcrdnsHtml + outboundHtml;
    } catch (e) {
        container.innerHTML = '<span class="status-err">Failed to load server status.</span>';
    }
}

async function loadDomains() {
    const res = await fetch('/api/domains');
    const data = await res.json();
    const tbody = document.querySelector('#domains-table tbody');
    tbody.innerHTML = '';

    const domainSelect = document.getElementById('account-domain');
    const currentSelected = domainSelect.value;
    domainSelect.innerHTML = '';

    if (data && data.length > 0) {
        for (let d of data) {
            const spfStatus = d.spf_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';
            const dkimStatus = d.dkim_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';
            const dmarcStatus = d.dmarc_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';

            // Convert to base64 or stringify safely to pass into inline function
            const domainJson = encodeURIComponent(JSON.stringify(d));

            tbody.innerHTML += `<tr class="clickable-row" onclick="showInstructions('${domainJson}')">
                <td>${d.domain}</td>
                <td>${spfStatus}</td>
                <td>${dkimStatus}</td>
                <td>${dmarcStatus}</td>
                <td><button class="btn-sm" onclick="event.stopPropagation(); showInstructions('${domainJson}')">DNS Setup</button></td>
            </tr>`;

            const opt = document.createElement('option');
            opt.value = d.domain;
            opt.textContent = d.domain;
            domainSelect.appendChild(opt);
        }
        if (currentSelected) {
            domainSelect.value = currentSelected;
        }
    } else {
        tbody.innerHTML = '<tr><td colspan="5"><i>No domains found (ACME_FILE not set or empty)</i></td></tr>';
        const opt = document.createElement('option');
        opt.value = '';
        opt.textContent = '-- No domains available --';
        domainSelect.appendChild(opt);
    }
}

function showInstructions(encodedData) {
    const d = JSON.parse(decodeURIComponent(encodedData));
    const dialog = document.getElementById('dns-dialog');
    const title = document.getElementById('dns-dialog-title');
    const body = document.getElementById('dns-dialog-body');

    title.textContent = `DNS Setup for ${d.domain}`;

    const spfRecord = d.smtp_domain && d.smtp_domain !== 'localhost'
        ? `v=spf1 a:${d.smtp_domain} mx ~all`
        : `v=spf1 mx ~all`;

    body.innerHTML = `
        <p>Add the following TXT records in <b>${escapeHtml(d.domain)}</b>'s DNS manager:</p>

        <h4 style="margin: 16px 0 6px 0;">1. SPF (Sender Policy Framework)</h4>
        <p class="status-line"><small class="text-muted">Host / Name: <code>@</code> (or apex domain)</small></p>
        <div class="code-box">${escapeHtml(spfRecord)}</div>

        <h4 style="margin: 16px 0 6px 0;">2. DKIM (DomainKeys Identified Mail)</h4>
        <p class="status-line"><small class="text-muted">Host / Name: <code>${escapeHtml(d.dkim_name)}</code></small></p>
        <div class="code-box">${escapeHtml(d.dkim_expected)}</div>

        <h4 style="margin: 16px 0 6px 0;">3. DMARC</h4>
        <p class="status-line"><small class="text-muted">Host / Name: <code>_dmarc</code></small></p>
        <div class="code-box">v=DMARC1; p=none;</div>

        <p class="dns-note">
            <i>Note on Reverse DNS (PTR):</i> rDNS is configured once at the server IP level for your primary mail host (see Server Identity section above). Recipient servers do not require individual sender domains to match the server IP's PTR record.
        </p>
    `;

    if (!dialog.open) {
        dialog.showModal();
    }
}

loadServerStatus();
loadAccounts();
loadMessages();
loadDomains();
setInterval(loadMessages, 5000);
