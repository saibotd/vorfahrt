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
            <td><button onclick="deleteAccount(${acc.id})">Delete</button></td>
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

async function loadMessages() {
    const res = await fetch('/api/messages');
    const data = await res.json();
    const tbody = document.querySelector('#queue-table tbody');
    tbody.innerHTML = '';
    for (let msg of data) {
        tbody.innerHTML += `<tr>
            <td>${msg.id}</td>
            <td>${msg.sender}</td>
            <td>${msg.recipient}</td>
            <td>${msg.status}</td>
            <td>${msg.attempts || 0}</td>
            <td>${msg.error}</td>
            <td>${new Date(msg.created_at).toLocaleString()}</td>
        </tr>`;
    }
}

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
                <p class="status-line"><b>Reverse DNS (PTR):</b> <code>${st.ptr}</code> <span class="status-warning">&#9888; Mismatch</span></p>
                <small class="status-warning-text"><b>Action Required:</b> In your VPS / hosting control panel (Hetzner, Netcup, DigitalOcean, etc.), set the Reverse DNS (PTR) for <code>${st.ip}</code> to <code>${st.smtp_domain}</code>.</small>
            `;
        } else if (st.fcrdns_status === 'missing_ptr') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${st.smtp_domain}</code> (IP: <code>${st.ip}</code>)</p>
                <p class="status-line"><b>Reverse DNS (PTR):</b> <span class="status-missing">&#10006; Missing PTR Record</span></p>
                <small class="status-error-text"><b>Action Required:</b> Set the Reverse DNS (PTR) for <code>${st.ip}</code> to <code>${st.smtp_domain}</code> in your hosting provider panel.</small>
            `;
        } else if (st.fcrdns_status === 'missing_a') {
            fcrdnsHtml = `
                <p class="status-line"><b>Primary Hostname (EHLO):</b> <code>${st.smtp_domain}</code> <span class="status-missing">&#10006; Missing DNS A Record</span></p>
                <small class="status-error-text"><b>Action Required:</b> Create an A record for <code>${st.smtp_domain}</code> pointing to your server's public IP.</small>
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
                    <p class="status-line"><b>Outbound SMTP (Port 25):</b> <span class="status-missing">&#10006; Blocked / Unreachable</span></p>
                    <small class="status-error-text"><b>Action Required:</b> Outbound traffic on TCP port 25 is blocked. Most hosting providers (Hetzner, AWS, DigitalOcean, Linode, etc.) block port 25 by default to prevent spam. You must request port 25 unblocking in your provider's support/console or adjust your firewall before emails can be delivered.</small>
                </div>
            `;
        }

        container.innerHTML = fcrdnsHtml + outboundHtml;
    } catch (e) {
        container.innerHTML = '<span class="status-missing">Failed to load server status.</span>';
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
            const spfStatus = d.spf_found ? '<span class="status-ok">Found</span>' : '<span class="status-missing">Missing</span>';
            const dkimStatus = d.dkim_found ? '<span class="status-ok">Found</span>' : '<span class="status-missing">Missing</span>';
            const dmarcStatus = d.dmarc_found ? '<span class="status-ok">Found</span>' : '<span class="status-missing">Missing</span>';

            // Convert to base64 or stringify safely to pass into inline function
            const domainJson = encodeURIComponent(JSON.stringify(d));

            tbody.innerHTML += `<tr>
                <td>${d.domain}</td>
                <td>${spfStatus}</td>
                <td>${dkimStatus}</td>
                <td>${dmarcStatus}</td>
                <td><button onclick="showInstructions('${domainJson}')">DNS Setup</button></td>
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
    const container = document.getElementById('dns-instructions-container');
    container.style.display = 'block';

    const spfRecord = d.smtp_domain && d.smtp_domain !== 'localhost'
        ? `v=spf1 a:${d.smtp_domain} mx ~all`
        : `v=spf1 mx ~all`;

    container.innerHTML = `
        <h3>DNS Instructions for ${d.domain}</h3>
        <p>Configure the following TXT records in <b>${d.domain}</b>'s DNS manager:</p>

        <h4>1. SPF (Sender Policy Framework)</h4>
        <p>Add a TXT record for <b>@</b> (or apex domain) to authorize this server:</p>
        <pre>${spfRecord}</pre>

        <h4>2. DKIM (DomainKeys Identified Mail)</h4>
        <p>Add a TXT record for <b>${d.dkim_name}</b>:</p>
        <pre>${d.dkim_expected}</pre>

        <h4>3. DMARC</h4>
        <p>Add a TXT record for <b>_dmarc</b>:</p>
        <pre>v=DMARC1; p=none;</pre>

        <p class="dns-note">
            <i>Note on Reverse DNS (PTR):</i> rDNS is configured once at the server IP level for your primary mail host (see Server Identity section above). Recipient servers do not require individual sender domains to match the server IP's PTR record.
        </p>

        <br>
        <button onclick="document.getElementById('dns-instructions-container').style.display='none'">Close</button>
    `;
}

loadServerStatus();
loadAccounts();
loadMessages();
loadDomains();
setInterval(loadMessages, 5000);
