function escapeHtml(str) {
    if (str === null || str === undefined) return '';
    return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#039;');
}

async function loadAccounts() {
    try {
        const res = await fetch('/api/accounts');
        if (!res.ok) return;
        const data = await res.json();
        const tbody = document.querySelector('#accounts-table tbody');
        tbody.innerHTML = '';
        if (!Array.isArray(data)) return;

        for (const acc of data) {
            const tr = document.createElement('tr');
            tr.innerHTML = `
                <td>${escapeHtml(acc.id)}</td>
                <td>${acc.domain ? escapeHtml(acc.domain) : '<i>none</i>'}</td>
                <td>${escapeHtml(acc.username)}</td>
                <td><code>${escapeHtml(acc.password)}</code> <button class="btn-copy" type="button">Copy</button></td>
                <td><button class="btn-sm btn-delete" type="button">Delete</button></td>
            `;
            const copyBtn = tr.querySelector('.btn-copy');
            copyBtn.addEventListener('click', () => copyPassword(copyBtn, acc.password || ''));
            const deleteBtn = tr.querySelector('.btn-delete');
            deleteBtn.addEventListener('click', () => deleteAccount(acc.id));
            tbody.appendChild(tr);
        }
    } catch (e) {
        console.error('Failed to load accounts:', e);
    }
}

function copyPassword(btn, text) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        navigator.clipboard.writeText(text).then(() => {
            const orig = btn.textContent;
            btn.textContent = 'Copied!';
            setTimeout(() => btn.textContent = orig, 1500);
        }).catch(() => {
            prompt('Password:', text);
        });
    } else {
        prompt('Password:', text);
    }
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
    const res = await fetch('/api/accounts/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id })
    });
    if (!res.ok) {
        const err = await res.text();
        alert('Error deleting account: ' + err);
        return;
    }
    loadAccounts();
}

async function loadMessages() {
    try {
        const res = await fetch('/api/messages');
        if (!res.ok) return;
        const data = await res.json();
        const tbody = document.querySelector('#queue-table tbody');
        tbody.innerHTML = '';

        if (!Array.isArray(data) || data.length === 0) {
            tbody.innerHTML = '<tr><td colspan="8"><i>No messages in queue</i></td></tr>';
            return;
        }

        for (const msg of data) {
            const safeStatus = escapeHtml(msg.status);
            const statusBadge = `<span class="badge badge-${safeStatus}">${safeStatus}</span>`;
            const subjectText = msg.subject ? escapeHtml(msg.subject) : '<i class="text-muted">(no subject)</i>';

            const tr = document.createElement('tr');
            tr.className = 'clickable-row';
            tr.innerHTML = `
                <td>${escapeHtml(msg.id)}</td>
                <td>${subjectText}</td>
                <td>${escapeHtml(msg.sender)}</td>
                <td>${escapeHtml(msg.recipient)}</td>
                <td>${statusBadge}</td>
                <td>${escapeHtml(msg.attempts || 0)}</td>
                <td>${escapeHtml(new Date(msg.created_at).toLocaleString())}</td>
                <td><button class="btn-sm btn-details" type="button">Details</button></td>
            `;
            tr.addEventListener('click', () => showMessageDetail(msg.id));
            tr.querySelector('.btn-details').addEventListener('click', (event) => {
                event.stopPropagation();
                showMessageDetail(msg.id);
            });
            tbody.appendChild(tr);
        }
    } catch (e) {
        console.error('Failed to load messages:', e);
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
        const res = await fetch(`/api/messages/detail?id=${encodeURIComponent(id)}`);
        if (!res.ok) {
            body.innerHTML = '<span class="status-err">Failed to load message details.</span>';
            return;
        }

        const msg = await res.json();
        const safeStatus = escapeHtml(msg.status);
        title.innerHTML = `Message #${escapeHtml(msg.id)} <span class="badge badge-${safeStatus}" style="margin-left: 10px;">${safeStatus}</span>`;

        let html = `
            <table class="detail-table">
                <tr><th>Status</th><td><span class="badge badge-${safeStatus}">${safeStatus}</span></td></tr>
                <tr><th>Subject</th><td><b>${msg.subject ? escapeHtml(msg.subject) : '<i>(no subject)</i>'}</b></td></tr>
                <tr><th>From</th><td><code>${escapeHtml(msg.sender)}</code></td></tr>
                <tr><th>To</th><td><code>${escapeHtml(msg.recipient)}</code></td></tr>
                <tr><th>Attempts</th><td>${escapeHtml(msg.attempts || 0)} / 5</td></tr>
                ${msg.next_retry_at && msg.status === 'queued' ? `<tr><th>Next Retry</th><td>${escapeHtml(new Date(msg.next_retry_at).toLocaleString())}</td></tr>` : ''}
                <tr><th>Submitted</th><td>${escapeHtml(new Date(msg.created_at).toLocaleString())}</td></tr>
                <tr><th>Last Updated</th><td>${escapeHtml(new Date(msg.updated_at).toLocaleString())}</td></tr>
                ${msg.size_bytes > 0 ? `<tr><th>Payload Size</th><td>${escapeHtml(msg.size_bytes)} bytes</td></tr>` : ''}
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
            for (const [k, v] of Object.entries(msg.headers)) {
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

// Close dialogs when clicking directly on their backdrops (ignoring keyboard clicks)
function setupDialogBackdrops() {
    document.querySelectorAll('dialog').forEach((dlg) => {
        dlg.addEventListener('click', (event) => {
            if (event.target !== dlg || event.detail === 0) {
                return;
            }
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
        if (!res.ok) {
            throw new Error(`HTTP ${res.status}`);
        }
        const st = await res.json();
        const safeDomain = escapeHtml(st.smtp_domain || 'localhost');
        const safeIP = escapeHtml(st.ip);
        const safePTR = escapeHtml(st.ptr);
        const safeTarget = escapeHtml(st.outbound_port_25_target || 'portquiz.net:25');

        let fcrdnsHtml = '';
        if (st.fcrdns_status === 'ok') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${safeDomain}</code> (IP: <code>${safeIP}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <code>${safePTR}</code> <span class="status-ok">&#10004; Valid FCrDNS</span></p>
                <small class="text-muted">Forward-Confirmed Reverse DNS is verified. Recipient mail servers (Gmail, Outlook, etc.) will accept outbound connections from this IP.</small>
            `;
        } else if (st.fcrdns_status === 'mismatch') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${safeDomain}</code> (IP: <code>${safeIP}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <code>${safePTR}</code> <span class="status-warn">&#9888; Mismatch</span></p>
                <small class="status-warn"><b>Action Required:</b> In your VPS / hosting control panel (Hetzner, Netcup, DigitalOcean, etc.), set the Reverse DNS (PTR) for <code>${safeIP}</code> to <code>${safeDomain}</code>.</small>
            `;
        } else if (st.fcrdns_status === 'missing_ptr') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${safeDomain}</code> (IP: <code>${safeIP}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <span class="status-err">&#10006; Missing PTR Record</span></p>
                <small class="status-err"><b>Action Required:</b> Set the Reverse DNS (PTR) for <code>${safeIP}</code> to <code>${safeDomain}</code> in your hosting provider panel.</small>
            `;
        } else if (st.fcrdns_status === 'missing_a') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${safeDomain}</code> <span class="status-err">&#10006; Missing DNS A Record</span></p>
                <small class="status-err"><b>Action Required:</b> Create an A record for <code>${safeDomain}</code> pointing to your server's public IP.</small>
            `;
        } else {
            fcrdnsHtml = `
                <p class="status-line text-muted"><code>SMTP_DOMAIN</code> is currently set to <code>${safeDomain}</code>.</p>
                <small class="text-muted">Set <code>SMTP_DOMAIN=mail.yourdomain.com</code> in your environment / docker-compose to establish your server's canonical sending identity.</small>
            `;
        }

        let outboundHtml = '';
        if (st.outbound_port_25_status === 'ok') {
            outboundHtml = `
                <div class="outbound-status-box">
                    <p class="status-line"><b>Outbound SMTP (Port 25):</b> <span class="status-ok">&#10004; Open / Reachable</span> <small class="text-muted">(Target: <code>${safeTarget}</code>)</small></p>
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
    try {
        const res = await fetch('/api/domains');
        if (!res.ok) return;
        const data = await res.json();
        const tbody = document.querySelector('#domains-table tbody');
        tbody.innerHTML = '';

        const domainSelect = document.getElementById('account-domain');
        const currentSelected = domainSelect.value;
        domainSelect.innerHTML = '';

        if (Array.isArray(data) && data.length > 0) {
            for (const d of data) {
                const spfStatus = d.spf_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';
                const dkimStatus = d.dkim_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';
                const dmarcStatus = d.dmarc_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';
                let bimiStatus = d.bimi_found ? '<span class="badge badge-positive">Found</span>' : '<span class="badge badge-negative">Missing</span>';
                if (d.bimi_found) {
                    bimiStatus += d.bimi_svg_ok
                        ? ' <span class="badge badge-positive">SVG Tiny PS</span>'
                        : ` <span class="badge badge-negative" title="${escapeHtml(d.bimi_svg_error || 'Invalid SVG')}">SVG Invalid</span>`;
                    if (d.bimi_pem_set) {
                        bimiStatus += d.bimi_pem_ok
                            ? ' <span class="badge badge-positive">PEM 200</span>'
                            : ' <span class="badge badge-negative">PEM Error</span>';
                    }
                }

                const tr = document.createElement('tr');
                tr.className = 'clickable-row';
                tr.innerHTML = `
                    <td>${escapeHtml(d.domain)}</td>
                    <td>${spfStatus}</td>
                    <td>${dkimStatus}</td>
                    <td>${dmarcStatus}</td>
                    <td>${bimiStatus}</td>
                    <td><button class="btn-sm btn-dns" type="button">DNS Setup</button></td>
                `;
                tr.addEventListener('click', () => showInstructions(d));
                tr.querySelector('.btn-dns').addEventListener('click', (event) => {
                    event.stopPropagation();
                    showInstructions(d);
                });
                tbody.appendChild(tr);

                const opt = document.createElement('option');
                opt.value = d.domain;
                opt.textContent = d.domain;
                domainSelect.appendChild(opt);
            }
            if (currentSelected && data.some((d) => d.domain === currentSelected)) {
                domainSelect.value = currentSelected;
            }
        } else {
            tbody.innerHTML = '<tr><td colspan="6"><i>No domains found (ACME_FILE not set or empty)</i></td></tr>';
            const opt = document.createElement('option');
            opt.value = '';
            opt.textContent = '-- No domains available --';
            domainSelect.appendChild(opt);
        }
    } catch (e) {
        console.error('Failed to load domains:', e);
    }
}

function showInstructions(d) {
    const dialog = document.getElementById('dns-dialog');
    const title = document.getElementById('dns-dialog-title');
    const body = document.getElementById('dns-dialog-body');

    title.textContent = `DNS Setup for ${d.domain}`;

    const spfRecord = d.smtp_domain && d.smtp_domain !== 'localhost'
        ? `v=spf1 a:${d.smtp_domain} mx ~all`
        : `v=spf1 mx ~all`;
    const bimiHost = String(d.domain || '').replace(/^\*\./, '');

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

        <h4 style="margin: 16px 0 6px 0;">4. BIMI (Optional)</h4>
        <p class="status-line"><small class="text-muted">Host / Name: <code>default._bimi</code> (requires DMARC <code>p=quarantine</code> or <code>p=reject</code>)</small></p>
        <div class="code-box">v=BIMI1; l=https://${escapeHtml(bimiHost)}/logo.svg;</div>
        ${d.bimi_found && !d.bimi_svg_ok && d.bimi_svg_error ? `<p class="status-line"><small class="status-err">SVG check failed: ${escapeHtml(d.bimi_svg_error)}</small></p>` : ''}
        ${d.bimi_found && d.bimi_pem_set && !d.bimi_pem_ok ? `<p class="status-line"><small class="status-err">PEM URL did not return HTTP 200.</small></p>` : ''}
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
