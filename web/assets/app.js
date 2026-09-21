// ========== State ==========
let currentUser = null;
let currentThread = 't_default';
let threads = [];
let isStreaming = false; // prevent duplicate sends
let currentModelId = 'mock'; // current model profile ID
let modelProfiles = []; // available model profiles
let pendingSwitchProfileId = null; // profile waiting for API key
let currentApprovals = []; // current pending approval requests
// allowedTools is the server's answer to "what may I call", taken from
// /api/auth/me. It is never re-derived from roles here: the RBAC table lives on
// the server, and a copy of it in the page drifts the moment roles gain tools
// they did not have at build time — which is exactly what MCP-granted remote
// tools are.
let allowedTools = [];
let registeredTools = []; // every registered tool, for the "N/M" denominator

// ========== Persistence ==========
function cacheKey(threadId) {
    return `chat_${currentUser.username}_${threadId}`;
}

function loadMessages(threadId) {
    try {
        const raw = localStorage.getItem(cacheKey(threadId));
        return raw ? JSON.parse(raw) : [];
    } catch (e) { return []; }
}

function saveMessages(threadId, msgs) {
    try { localStorage.setItem(cacheKey(threadId), JSON.stringify(msgs)); } catch (e) {}
}

function pushMessage(threadId, role, content) {
    const msgs = loadMessages(threadId);
    msgs.push({ role, content });
    saveMessages(threadId, msgs);
}

function updateLastMessage(threadId, role, content) {
    const msgs = loadMessages(threadId);
    // Find the last message with this role and update it
    for (let i = msgs.length - 1; i >= 0; i--) {
        if (msgs[i].role === role) {
            msgs[i].content = content;
            saveMessages(threadId, msgs);
            return;
        }
    }
    // If not found, push a new one
    msgs.push({ role, content });
    saveMessages(threadId, msgs);
}

function loadThreadList() {
    try {
        const raw = localStorage.getItem(`threads_${currentUser.username}`);
        return raw ? JSON.parse(raw) : [currentThread];
    } catch (e) { return [currentThread]; }
}

function saveThreadList(list) {
    try { localStorage.setItem(`threads_${currentUser.username}`, JSON.stringify(list)); } catch (e) {}
}

// ========== API Helpers ==========
// The session travels in an HttpOnly cookie, so it is never read or written by
// page script and survives a reload. `credentials: 'same-origin'` is the default
// for same-origin requests; it is stated explicitly because auth depends on it.
async function api(method, path, body) {
    const opts = {
        method,
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
    };
    if (body) {
        opts.body = JSON.stringify(body);
    }
    const resp = await fetch(path, opts);
    const data = await resp.json();
    if (!resp.ok) {
        // A 401 is not a failed request to report in place — the credential is
        // gone and every later call will fail the same way. Return to the login
        // page instead. The login call itself is exempt: a wrong password is also
        // a 401, and it must not be dressed up as an expired session.
        if (resp.status === 401 && !path.startsWith('/api/auth/login')) {
            handleSessionExpired();
        }
        throw new Error(data.error || `HTTP ${resp.status}`);
    }
    return data;
}

// handleSessionExpired returns the page to login after a 401.
//
// It runs once per expiry: several requests can be in flight when the session
// dies, and each would otherwise re-show the notice and re-render.
let sessionExpiredHandled = false;
function handleSessionExpired() {
    if (sessionExpiredHandled) return;
    sessionExpiredHandled = true;
    currentUser = null;
    isStreaming = false;
    setStreamingControls(false);
    setStreamProgress('');
    document.getElementById('mainApp').classList.remove('active');
    document.getElementById('loginPage').classList.add('active');
    const notice = document.getElementById('loginNotice');
    if (notice) {
        notice.textContent = '会话已过期，请重新登录';
    }
}

// ========== Login ==========
async function doLogin() {
    const username = document.getElementById('loginUsername').value;
    const password = document.getElementById('loginPassword').value;
    try {
        const data = await api('POST', '/api/auth/login', { username, password });
        currentUser = data.user;
        // A fresh session clears both the notice and the once-per-expiry flag, so
        // the next expiry reports itself too.
        sessionExpiredHandled = false;
        const notice = document.getElementById('loginNotice');
        if (notice) notice.textContent = '';
        showMainApp();
    } catch (e) {
        alert('登录失败：' + e.message);
    }
}

async function doLogout() {
    try { await api('POST', '/api/auth/logout'); } catch (e) {}
    currentUser = null;
    document.getElementById('loginPage').classList.add('active');
    document.getElementById('mainApp').classList.remove('active');
}

// ========== Bootstrap ==========
// The session lives in an HttpOnly cookie, so a reload keeps the user signed in.
// Quiet mode means a visitor without a session simply stays on the login page.
document.addEventListener('DOMContentLoaded', () => { showMainApp(true); });

// ========== Main App ==========
// showMainApp reveals the main view. It probes the session before revealing
// anything, so a fresh visit never flashes the app before falling back to login,
// and `quiet` suppresses the alert when the caller already expects no session.
async function showMainApp(quiet = false) {
    try {
        const me = await api('GET', '/api/auth/me');
        currentUser = me.user;
        allowedTools = me.tools || [];
        renderUserInfo(me);
    } catch (e) {
        if (!quiet) {
            alert('获取用户信息失败：' + e.message);
        }
        return false;
    }

    document.getElementById('loginPage').classList.remove('active');
    document.getElementById('mainApp').classList.add('active');

    loadTools();
    loadModels();
    refreshApprovals();
    refreshMemory();
    refreshDocuments();
    threads = loadThreadList();
    currentThread = threads[0] || 't_default';
    renderThreads();
    renderChat();
    refreshTokenBar(currentThread);
    return true;
}

function renderUserInfo(me) {
    const userDiv = document.getElementById('currentUser');
    userDiv.innerHTML = `
        <div class="username">${me.user.username}</div>
        <div class="roles">${me.user.roles.map(r => `<span class="role-badge role-${r}">${r}</span>`).join(' ')}</div>
        <div class="tool-count">${toolCountText()}</div>
    `;
}

// toolCountText reports the ACL gap. The denominator is the number of registered
// tools as the server reported them, not a constant: it grows when MCP servers
// contribute tools, and a hardcoded 6 silently became a lie.
function toolCountText() {
    const total = registeredTools.length;
    if (!total) {
        return `可用工具 ${allowedTools.length}`;
    }
    return `可用工具 ${allowedTools.length}/${total}`;
}

// ========== Threads ==========
function renderThreads() {
    const listDiv = document.getElementById('threadList');
    listDiv.innerHTML = threads.map(t => `
        <div class="thread-item ${t === currentThread ? 'active' : ''}" style="display:flex;justify-content:space-between;align-items:center;">
            <span onclick="switchThread('${t}')" style="flex:1;cursor:pointer;">
                ${t === currentThread ? '📝 ' : ''}${t}
            </span>
            <span class="thread-delete" onclick="event.stopPropagation();deleteThread('${t}')" title="删除会话">✕</span>
        </div>
    `).join('');
}

function switchThread(threadId) {
    if (threadId === currentThread) return;
    currentThread = threadId;
    renderThreads();
    renderChat();
    refreshTokenBar(threadId);
}

function newThread() {
    const tid = 't_' + Date.now().toString(36);
    threads.push(tid);
    saveThreadList(threads);
    saveMessages(tid, []);
    currentThread = tid;
    renderThreads();
    renderChat();
    refreshTokenBar(tid);
}

async function deleteThread(threadId) {
    if (threads.length <= 1) {
        alert('至少保留一个会话');
        return;
    }
    if (!confirm(`确定删除会话 ${threadId} 吗？`)) return;

    // Remove from local state
    threads = threads.filter(t => t !== threadId);
    saveThreadList(threads);

    // Remove from localStorage
    localStorage.removeItem(cacheKey(threadId));

    // Remove from backend
    try {
        const resp = await fetch(`/api/chat/${threadId}/delete`, { method: 'DELETE', credentials: 'same-origin' });
        // The response used to be ignored, so an expired session made the local
        // delete look successful while the server kept the thread.
        if (resp.status === 401) handleSessionExpired();
    } catch (e) {}

    // If deleting current thread, switch to the first one
    if (threadId === currentThread) {
        currentThread = threads[0];
    }

    renderThreads();
    renderChat();
    refreshTokenBar(currentThread);
}

// ========== Chat Rendering ==========
function renderChat() {
    const container = document.getElementById('chatMessages');
    const msgs = loadMessages(currentThread);

    if (msgs.length === 0) {
        container.innerHTML = '<div class="empty-state">开始新的对话吧</div>';
        return;
    }

    let html = '';
    for (const msg of msgs) {
        if (msg.role === 'approval-card') {
            html += renderApprovalCardMessage(msg);
            continue;
        }
        const escaped = escapeHtml(msg.content);
        const cls = msg.role === 'user' ? 'msg-user' :
                    msg.role === 'assistant' ? 'msg-assistant' :
                    msg.role === 'interrupt' ? 'msg-interrupt' :
                    msg.role === 'acl-denied' ? 'msg-acl-denied' :
                    msg.role === 'approval' ? 'msg-approval' :
                    msg.role === 'system' ? 'msg-system' : 'msg-assistant';
        // Use data-streaming attribute to mark the streaming message
        const streaming = msg._streaming ? ' data-streaming="true"' : '';
        // A tool that draws a table pads its columns to line up, which only works
        // in a fixed-width font: in the proportional font the chat uses, the
        // borders come out ragged however carefully they were padded. The presence
        // of box-drawing characters is the signal — nothing else in a message
        // draws them — and the decision belongs here, in the renderer, rather than
        // in the tool that produced the text.
        const pre = isPreformatted(msg.content) ? ' msg-pre' : '';
        html += `<div class="msg ${cls}${pre}" style="white-space:pre-wrap"${streaming}>${escaped}</div>`;
    }

    container.innerHTML = html;
    container.scrollTop = container.scrollHeight;
}

// isPreformatted reports whether a message contains a drawn table.
function isPreformatted(text) {
    return /[\u2500-\u257F]/.test(text || '');
}

// Render an interactive approval card as a chat message.
// Two visual states: pending (with 批准/拒绝 actions) and resolved (badge only).
function renderApprovalCardMessage(m) {
    const isPlan = !m.toolName;
    const kind = isPlan ? '执行计划' : '工具调用';
    const title = isPlan ? 'Agent 执行计划' : m.toolName;
    const stateBadges = {
        pending: '<span class="ac-state pending"><i></i>待审批</span>',
        approved: '<span class="ac-state approved"><i></i>已批准</span>',
        rejected: '<span class="ac-state rejected"><i></i>已拒绝</span>',
        resolved: '<span class="ac-state resolved"><i></i>已处理</span>',
    };

    // Structured planned steps: explicit plan first, else the single tool call.
    const steps = [];
    if (Array.isArray(m.plan) && m.plan.length) {
        for (const s of m.plan) steps.push(s);
    } else if (m.toolName) {
        steps.push({ name: m.toolName, arguments: m.args });
    }

    let html = `
        <div class="msg msg-approval-card" data-interrupt-id="${m.interruptId}">
            <div class="ac-header">
                <span class="ac-icon">${isPlan ? '🗺️' : '🔧'}</span>
                <div class="ac-titles">
                    <div class="ac-title">${escapeHtml(title)}</div>
                    <div class="ac-kind">${kind} · 需要人工审批</div>
                </div>
                ${stateBadges[m.status] || ''}
            </div>`;
    if (steps.length) {
        html += `<div class="ac-plan">${steps.map(s => `
            <div class="ac-step">
                <span class="ac-step-name">${escapeHtml(s.name)}</span>
                ${s.arguments ? `<code class="ac-step-args">${escapeHtml(s.arguments)}</code>` : ''}
            </div>`).join('')}</div>`;
    }
    if (m.message) {
        html += `<div class="ac-desc">${escapeHtml(m.message)}</div>`;
    }
    if (m.status === 'pending') {
        html += `
            <div class="ac-actions">
                <input type="text" placeholder="拒绝原因（可选）" id="rr-${m.interruptId}">
                <button class="btn-ac-reject" onclick="decideApproval('${m.interruptId}', false)">拒绝</button>
                <button class="btn-ac-approve" onclick="decideApproval('${m.interruptId}', true)">✓ 批准</button>
            </div>`;
    }
    html += '</div>';
    return html;
}

// Push an interactive approval card into the thread's chat.
function pushApprovalCard(threadId, card) {
    const msgs = loadMessages(threadId);
    msgs.push({ role: 'approval-card', status: 'pending', ...card });
    saveMessages(threadId, msgs);
    renderChat();
}

// Keep chat cards in sync with the server-side pending approvals:
// add cards for pending approvals missing from the chat, and mark pending
// cards resolved when their interrupt is no longer pending (e.g. handled
// in another tab).
function syncThreadApprovalCards() {
    const msgs = loadMessages(currentThread);
    const pendingForThread = (currentApprovals || [])
        .filter(a => a.Status === 'pending' && a.ThreadID === currentThread);
    let changed = false;

    for (const a of pendingForThread) {
        const exists = msgs.some(m => m.role === 'approval-card' && m.interruptId === a.InterruptID);
        if (!exists) {
            msgs.push({
                role: 'approval-card', status: 'pending',
                interruptId: a.InterruptID,
                toolName: a.ToolName, nodeName: a.NodeName,
                riskLevel: a.RiskLevel, message: a.Message, args: a.Arguments,
                plan: Array.isArray(a.Payload?.plan) ? a.Payload.plan : undefined,
            });
            changed = true;
        }
    }
    for (const m of msgs) {
        if (m.role === 'approval-card' && m.status === 'pending' &&
            !pendingForThread.some(a => a.InterruptID === m.interruptId)) {
            m.status = 'resolved';
            changed = true;
        }
    }
    if (changed) {
        saveMessages(currentThread, msgs);
        renderChat();
    }
}

// Scroll the chat to the first approval card.
function scrollToApprovalCards() {
    const el = document.querySelector('#chatMessages [data-interrupt-id]');
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Update only the streaming message element (avoids full re-render)
function updateStreamingMessage(content) {
    const container = document.getElementById('chatMessages');
    const el = container.querySelector('[data-streaming="true"]');
    if (el) {
        el.textContent = content;
        container.scrollTop = container.scrollHeight;
    }
}

// ========== Chat ==========
let lastSendTime = 0;
const MIN_SEND_INTERVAL = 3000; // 3 seconds between requests to avoid 429

async function sendMessage() {
    const input = document.getElementById('chatInput');
    const message = input.value.trim();
    if (!message || isStreaming) return;

    // Rate limit: prevent sending too fast
    const now = Date.now();
    const elapsed = now - lastSendTime;
    if (elapsed < MIN_SEND_INTERVAL) {
        const waitSec = Math.ceil((MIN_SEND_INTERVAL - elapsed) / 1000);
        pushMessage(currentThread, 'assistant', `⏳ 请求过于频繁，请等待 ${waitSec} 秒后重试`);
        renderChat();
        return;
    }
    lastSendTime = now;

    input.value = '';

    // Notify user if there's a pending approval for this thread.
    // The backend sanitizes orphaned tool_calls so the request will still work,
    // but it's helpful to remind the user to resolve pending approvals.
    const pendingForThread = (currentApprovals || []).filter(a => a.Status === 'pending' && a.ThreadID === currentThread);
    if (pendingForThread.length > 0) {
        pushMessage(currentThread, 'system', `⏸️ 提示：当前会话有 ${pendingForThread.length} 个待审批项，建议先在审批中心处理。`);
        renderChat();
    }

    // Save user message
    pushMessage(currentThread, 'user', message);
    renderChat();

    isStreaming = true;
    setStreamingControls(true);

    try {
        const outcome = await chatStream(currentThread, message);
        if (outcome === 'aborted') {
            isStreaming = false;
            setStreamingControls(false);
            return;
        }
    } catch (e) {
        // Fallback: if streaming fails, try non-streaming
        try {
            const data = await api('POST', '/api/agent/chat', {
                threadId: currentThread,
                message,
                confirmBeforeExecute: document.getElementById('confirmBeforeExecute')?.checked || false,
            });
            if (data.status === 'interrupted') {
                // Inline approval card in the chat instead of a static notice.
                pushApprovalCard(currentThread, {
                    interruptId: data.interrupt.interrupt_id,
                    toolName: data.interrupt.tool_name,
                    nodeName: data.interrupt.node_name,
                    message: data.interrupt.message,
                    args: data.interrupt.arguments,
                    plan: data.interrupt.plan,
                });
                refreshApprovals();
            } else if (data.status === 'completed') {
                pushMessage(currentThread, 'assistant', data.answer || '（无回复）');
                // This response is a ChatRunResult, which carries no memory list —
                // only the SSE done frame adds one. Refresh instead of reading a
                // field that is not there.
                setTimeout(refreshMemory, 500);
            } else if (data.status === 'error' || data.status === 'cancelled') {
                // Same distinction the SSE path makes. These used to fall into the
                // catch-all below and reach the user labelled as an authorization
                // refusal, which is a different failure entirely.
                const label = data.status === 'cancelled' ? '⏹️ 已取消' : '❌ 执行失败';
                pushMessage(currentThread, 'assistant', `${label}${data.answer ? `：${data.answer}` : ''}`);
            } else {
                pushMessage(currentThread, 'assistant', `❌ 未知状态 ${data.status}${data.answer ? `：${data.answer}` : ''}`);
            }
        } catch (e2) {
            const errMsg = e2.message || e.message;
            if (errMsg.includes('429') || errMsg.toLowerCase().includes('too many')) {
                pushMessage(currentThread, 'assistant', '⏳ API 请求频率超限，请稍等几秒再试。');
            } else {
                pushMessage(currentThread, 'acl-denied', `❌ 请求失败：${errMsg}`);
            }
        }
        renderChat();
    }

    isStreaming = false;
    setStreamingControls(false);
}

// AbortController of the in-flight chat stream, so the stop button can cancel it.
let streamAbort = null;

// setStreamingControls swaps the send button for the stop button while a run is
// in flight.
function setStreamingControls(active) {
    const sendBtn = document.getElementById('sendBtn');
    const stopBtn = document.getElementById('stopBtn');
    if (sendBtn) sendBtn.style.display = active ? 'none' : '';
    if (stopBtn) stopBtn.style.display = active ? '' : 'none';
}

// stopStreaming closes the SSE connection. The server observes the disconnect
// through the request context and cancels the run.
function stopStreaming() {
    if (streamAbort) streamAbort.abort();
}

// setStreamProgress shows a live one-line status above the input while tools run.
function setStreamProgress(text) {
    const el = document.getElementById('streamProgress');
    if (!el) return;
    if (!text) {
        el.style.display = 'none';
        el.textContent = '';
        return;
    }
    el.style.display = 'block';
    el.textContent = text;
}

function progressTextForToolFrame(data) {
    const tool = data.tool || '工具';
    switch (data.phase) {
        case 'route': return `🔀 路由到 ${tool}`;
        case 'start': return `🔧 正在调用 ${tool}…`;
        case 'end': return `✅ ${tool} 执行完成`;
        case 'denied': return `🚫 ${tool} 被权限拦截`;
        case 'approval': return `⏸️ ${tool} 等待人工审批`;
        default: return `• ${tool}`;
    }
}

// chatStream sends a message and reads the SSE stream
async function chatStream(threadId, message) {
    const confirmBeforeExecute = document.getElementById('confirmBeforeExecute')?.checked || false;
    const controller = new AbortController();
    streamAbort = controller;
    let fullContent = '';

    try {
        const resp = await fetch('/api/agent/chat', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            credentials: 'same-origin',
            body: JSON.stringify({ threadId, message, stream: true, confirmBeforeExecute }),
            signal: controller.signal,
        });

        if (!resp.ok) {
            const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
            // These two paths read the body as a stream, so they cannot go through
            // api(); an expired session still has to return the page to login
            // rather than surface as a chat error.
            if (resp.status === 401) {
                handleSessionExpired();
            }
            throw new Error(err.error || `HTTP ${resp.status}`);
        }

        // Add a placeholder assistant message marked as streaming
        const msgs = loadMessages(threadId);
        msgs.push({ role: 'assistant', content: '', _streaming: true });
        saveMessages(threadId, msgs);
        renderChat();

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });

            // Parse SSE events from buffer
            const lines = buffer.split('\n');
            buffer = ''; // remaining incomplete line

            // The frame set is chunk / tool_call / done. There is no error frame:
            // a failed or cancelled run arrives as `done` carrying that status,
            // which is why the branches below check data.status instead of
            // waiting for an event that never comes.
            for (let i = 0; i < lines.length; i++) {
                const line = lines[i];

                if (line.startsWith('event: ')) {
                    const eventType = line.slice(7).trim();
                    // Next line should be "data: ..."
                    const dataLine = lines[i + 1];
                    if (dataLine && dataLine.startsWith('data: ')) {
                        const dataStr = dataLine.slice(6);
                        i++; // skip data line

                        try {
                            const data = JSON.parse(dataStr);

                            if (eventType === 'chunk' && data.content) {
                                fullContent += data.content;
                                // Update the streaming message in localStorage
                                updateStreamingMessageContent(threadId, fullContent);
                                // Update the DOM element directly (fast, no full re-render)
                                updateStreamingMessage(fullContent);
                            } else if (eventType === 'tool_call') {
                                // Live tool/route progress for this run.
                                setStreamProgress(progressTextForToolFrame(data));
                            } else if (eventType === 'done') {
                                setStreamProgress('');
                                // Handle HITL interrupt: show interrupt message + refresh approval cards
                                if (data.status === 'interrupted' && data.interrupt) {
                                    const interruptInfo = data.interrupt;
                                    // The assistant's pending text becomes a normal message;
                                    // the approval arrives as an interactive inline card.
                                    finalizeStreamingMessage(threadId, data.answer || '');
                                    pushApprovalCard(threadId, {
                                        interruptId: interruptInfo.interrupt_id,
                                        toolName: interruptInfo.tool_name,
                                        nodeName: interruptInfo.node_name,
                                        message: interruptInfo.message,
                                        args: interruptInfo.arguments,
                                        plan: interruptInfo.plan,
                                    });
                                    // Refresh approvals (updates badge + syncs cards).
                                    refreshApprovals();
                                } else if (data.status === 'error' || data.status === 'cancelled') {
                                    // A failed run may have already streamed part of an
                                    // answer; keep it and mark why it stopped.
                                    const label = data.status === 'cancelled' ? '⏹️ 已取消' : '❌ 执行失败';
                                    const reason = data.answer ? `：${data.answer}` : '';
                                    const partial = fullContent ? `${fullContent}\n\n${label}${reason}` : `${label}${reason}`;
                                    finalizeStreamingMessage(threadId, partial);
                                    renderChat();
                                } else {
                                    // Normal completion — save complete message
                                    finalizeStreamingMessage(threadId, data.answer || fullContent);
                                    renderChat();
                                }

                                // Use server-provided memory if available, else refresh via API
                                if (data.memory && data.memory.length > 0) {
                                    renderMemory(data.memory);
                                } else if (data.memory) {
                                    renderMemory([]);
                                } else {
                                    setTimeout(refreshMemory, 500);
                                }

                                // Render run events (supervisor routing, tool calls, compressions, etc.)
                                if (data.events && data.events.length > 0) {
                                    renderEvents(data.events);
                                }

                                // Update context token bar
                                if (data.contextTokens) {
                                    updateTokenBar(data.contextTokens, data.actualTokens);
                                }
                            }
                        } catch (e) {
                            // Ignore parse errors for individual events
                        }
                    }
                } else if (line && !line.startsWith(':') && i === lines.length - 1) {
                    // Incomplete line, keep in buffer
                    buffer = line;
                }
            }
        }

        // If we got content but no 'done' event, finalize anyway
        if (fullContent) {
            finalizeStreamingMessage(threadId, fullContent);
            renderChat();
        }
        return 'completed';
    } catch (e) {
        if (e && e.name === 'AbortError') {
            // The user stopped the run: keep whatever had already arrived.
            finalizeStreamingMessage(threadId, fullContent ? `${fullContent}\n\n⏹️ 已停止` : '⏹️ 已停止');
            renderChat();
            return 'aborted';
        }
        throw e;
    } finally {
        streamAbort = null;
        setStreamProgress('');
        setStreamingControls(false);
    }
}

// Update the streaming message content in localStorage
function updateStreamingMessageContent(threadId, content) {
    const msgs = loadMessages(threadId);
    for (let i = msgs.length - 1; i >= 0; i--) {
        if (msgs[i]._streaming) {
            msgs[i].content = content;
            saveMessages(threadId, msgs);
            return;
        }
    }
}

// Mark streaming message as complete (remove _streaming flag)
function finalizeStreamingMessage(threadId, content) {
    const msgs = loadMessages(threadId);
    for (let i = msgs.length - 1; i >= 0; i--) {
        if (msgs[i]._streaming) {
            msgs[i].content = content;
            delete msgs[i]._streaming;
            saveMessages(threadId, msgs);
            return;
        }
    }
}

// ========== Approvals ==========
// Track which pending interrupts the user has dismissed from the modal, so we
// don't re-pop the modal for an item the user explicitly chose to handle later.

async function refreshApprovals() {
    try {
        const data = await api('GET', '/api/approvals');
        currentApprovals = data || [];
        renderApprovals(currentApprovals);
    } catch (e) {}
}

function renderApprovals(approvals) {
    // Right panel: compact pending count; the interactive cards live in the chat.
    const listDiv = document.getElementById('approvalList');
    const pending = (approvals || []).filter(a => a.Status === 'pending');
    updateApprovalBadge(pending.length);
    syncThreadApprovalCards();

    if (pending.length === 0) {
        listDiv.innerHTML = '<div class="empty-state">无待审批项</div>';
        return;
    }

    listDiv.innerHTML = `
        <div class="approval-indicator" onclick="scrollToApprovalCards()">
            <span class="indicator-dot"></span>
            <span class="indicator-text">${pending.length} 个待审批（在对话中处理）</span>
        </div>
        <button onclick="scrollToApprovalCards()" class="btn btn-sm btn-full" style="margin-top:6px;">跳转到审批卡片</button>
    `;
}

// ========== Right Panel Tabs & Demo Dropdown ==========
function switchPanelTab(name) {
    document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));
    document.querySelectorAll('.panel-tab').forEach(t => t.classList.remove('active', 'has-new'));
    const panel = document.getElementById('panel-' + name);
    if (panel) panel.classList.add('active');
    const tab = document.getElementById('tab-' + name);
    if (tab) tab.classList.add('active');
}

function updateApprovalBadge(pendingCount) {
    const badge = document.getElementById('approvalBadge');
    const tab = document.getElementById('tab-approvals');
    if (!badge || !tab) return;
    if (pendingCount > 0) {
        badge.textContent = pendingCount;
        badge.style.display = 'inline-block';
        if (!tab.classList.contains('active')) {
            tab.classList.add('has-new');
        }
    } else {
        badge.style.display = 'none';
        tab.classList.remove('has-new');
    }
}

function toggleDemoMenu(e) {
    if (e) e.stopPropagation();
    const menu = document.getElementById('demoMenu');
    if (menu) menu.style.display = menu.style.display === 'none' ? 'block' : 'none';
}

function runDemoFromMenu(type) {
    const menu = document.getElementById('demoMenu');
    if (menu) menu.style.display = 'none';
    runDemo(type);
}

// Close the demo menu when clicking anywhere outside it.
document.addEventListener('click', (e) => {
    const menu = document.getElementById('demoMenu');
    if (menu && menu.style.display !== 'none' && !menu.contains(e.target)) {
        menu.style.display = 'none';
    }
});

// decideApproval submits a decision and streams the resumed run's progress.
//
// A resume executes the gated tool and then continues the ReAct loop, so it can
// take as long as a chat turn and can interrupt again — neither of which the old
// one-shot JSON response could show.
//
// No stop button here on purpose: once the approval is claimed the server runs to
// completion regardless of this connection, so offering "stop" would imply an
// effect it does not have.
async function decideApproval(interruptId, approved) {
    const reasonEl = document.getElementById(`rr-${interruptId}`);
    const reason = reasonEl ? reasonEl.value : '';

    setStreamProgress(approved ? '✅ 已批准，正在执行…' : '🚫 已拒绝，正在重新规划…');

    let answer = '';
    let interrupted = null;
    let failure = null;
    try {
        const resp = await fetch(`/api/approvals/${interruptId}/decision`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            credentials: 'same-origin',
            body: JSON.stringify({ approved, reason, stream: true }),
        });
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
            // These two paths read the body as a stream, so they cannot go through
            // api(); an expired session still has to return the page to login
            // rather than surface as a chat error.
            if (resp.status === 401) {
                handleSessionExpired();
            }
            throw new Error(err.error || `HTTP ${resp.status}`);
        }

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = '';
            for (let i = 0; i < lines.length; i++) {
                const line = lines[i];
                if (line.startsWith('event: ')) {
                    const eventType = line.slice(7).trim();
                    const dataLine = lines[i + 1];
                    if (dataLine && dataLine.startsWith('data: ')) {
                        const dataStr = dataLine.slice(6);
                        i++;
                        try {
                            const data = JSON.parse(dataStr);
                            if (eventType === 'chunk' && data.content) {
                                answer += data.content;
                                setStreamProgress('正在生成回复…');
                            } else if (eventType === 'tool_call') {
                                setStreamProgress(progressTextForToolFrame(data));
                            } else if (eventType === 'done') {
                                if (data.answer) answer = data.answer;
                                if (data.interrupt) interrupted = data.interrupt;
                                if (data.status === 'error' || data.status === 'cancelled') {
                                    failure = data.answer || data.status;
                                }
                            }
                        } catch (e) {
                            // Ignore parse errors for individual frames
                        }
                    }
                } else if (line && !line.startsWith(':') && i === lines.length - 1) {
                    buffer = line;
                }
            }
        }
    } catch (e) {
        failure = e.message || String(e);
    } finally {
        setStreamProgress('');
    }

    // Flip the card: it is resolved when the run moved on, failed otherwise.
    const msgs = loadMessages(currentThread);
    for (const m of msgs) {
        if (m.role === 'approval-card' && m.interruptId === interruptId && m.status === 'pending') {
            m.status = failure ? 'resolved' : (approved ? 'approved' : 'rejected');
        }
    }
    saveMessages(currentThread, msgs);
    if (failure) {
        pushMessage(currentThread, 'approval', `⚠️ 审批已提交，但恢复过程出现问题：${failure}`);
    } else {
        pushMessage(currentThread, 'approval',
            `${approved ? '✅' : '🚫'} 审批结果：${answer || (approved ? '已批准' : '已拒绝')}`);
    }
    renderChat();

    // A resumed run can interrupt again: surface the new card the same way the
    // chat stream does.
    if (interrupted) {
        pushApprovalCard(currentThread, {
            interruptId: interrupted.interrupt_id,
            toolName: interrupted.tool_name,
            nodeName: interrupted.node_name,
            message: interrupted.message,
            args: interrupted.arguments,
            plan: interrupted.plan,
        });
    }
    await refreshApprovals();
}

// ========== Tools ==========
async function loadTools() {
    try {
        const tools = await api('GET', '/api/tools');
        registeredTools = tools || [];
        renderTools(registeredTools);
        // The count in the sidebar needs both halves, and this is where the
        // denominator arrives.
        renderUserInfo({ user: currentUser });
    } catch (e) {}
}

// renderTools marks each tool available or not using the server's list. The
// per-role tool table is not duplicated here on purpose — see the note on
// allowedTools.
function renderTools(tools) {
    const listDiv = document.getElementById('toolList');
    listDiv.innerHTML = tools.map(t => {
        const available = allowedTools.includes(t.name);
        const riskClass = t.risk_level || 'low';
        return `<div class="tool-item ${available ? 'available' : 'unavailable'}">
            <span>${available ? '✅' : '🚫'} ${t.name}</span>
            <span class="tool-risk ${riskClass}">${t.risk_level}</span>
        </div>`;
    }).join('');
}

// ========== Memory ==========
async function refreshMemory() {
    const listDiv = document.getElementById('memoryList');
    if (!listDiv) return;
    listDiv.innerHTML = '<div class="empty-state">加载中...</div>';
    await refreshMemorySettings();
    try {
        const data = await api('GET', '/api/memory');
        console.log('[Memory] data:', data);
        renderMemory(data);
    } catch (e) {
        console.error('[Memory] refresh failed:', e);
        listDiv.innerHTML = '<div class="empty-state">加载失败，点击刷新重试</div>';
    }
}

// refreshMemorySettings syncs the "do not remember" toggle with the server.
async function refreshMemorySettings() {
    const toggle = document.getElementById('memoryDisabled');
    if (!toggle) return;
    try {
        const settings = await api('GET', '/api/memory/settings');
        toggle.checked = settings.enabled === false;
        applyMemoryDisabledState(toggle.checked);
    } catch (e) {
        console.warn('[Memory] settings unavailable:', e.message || e);
    }
}

// toggleMemorySetting persists the switch and reflects it in the panel.
async function toggleMemorySetting(disabled) {
    try {
        await api('PUT', '/api/memory/settings', { enabled: !disabled });
        applyMemoryDisabledState(disabled);
        pushMessage(currentThread, 'system', disabled
            ? '🙈 已禁止记忆：不再提取新记忆，也不再注入已有记忆（条目仍保留）'
            : '🧠 已恢复记忆：新对话会重新提取并注入记忆');
        renderChat();
    } catch (e) {
        // Roll the checkbox back so it never disagrees with the server.
        const toggle = document.getElementById('memoryDisabled');
        if (toggle) toggle.checked = !disabled;
        pushMessage(currentThread, 'system', `记忆设置保存失败：${e.message}`);
        renderChat();
    }
}

function applyMemoryDisabledState(disabled) {
    const listDiv = document.getElementById('memoryList');
    if (!listDiv) return;
    listDiv.classList.toggle('memory-disabled', !!disabled);
    let notice = document.getElementById('memoryDisabledNotice');
    if (disabled) {
        if (!notice) {
            notice = document.createElement('div');
            notice.id = 'memoryDisabledNotice';
            notice.className = 'empty-state';
            notice.textContent = '记忆已关闭：下方为已有条目，不再被提取或注入';
            listDiv.parentNode.insertBefore(notice, listDiv);
        }
    } else if (notice) {
        notice.remove();
    }
}

const MEMORY_TYPE_LABELS = {
    preference: '偏好',
    identity: '身份',
    fact: '事实',
    episode: '情景',
    rule: '规则',
};

function memoryTypeLabel(e) {
    return MEMORY_TYPE_LABELS[e.type || 'preference'] || '其他';
}

function memoryImportance(e) {
    const n = e.importance && e.importance >= 1 && e.importance <= 5 ? e.importance : 3;
    return '★'.repeat(n) + '☆'.repeat(5 - n);
}

function memoryTooltip(e) {
    const parts = [];
    if (e.source) parts.push('来源: ' + e.source);
    if (e.source_thread_id) parts.push('线程: ' + e.source_thread_id);
    if (e.source_excerpt) parts.push('原话: ' + e.source_excerpt);
    if (e.access_count) parts.push('被使用 ' + e.access_count + ' 次');
    if (e.updated_at) parts.push('更新于 ' + e.updated_at);
    return parts.join('\n') || '';
}

function memoryItemHTML(e) {
    const history = (e.history && e.history.length)
        ? ` <span class="memory-history" title="${e.history.map(h => h.value).join(' ← ').replace(/"/g, '&quot;')}">⏳${e.history.length}</span>`
        : '';
    const stars = memoryImportance(e);
    const tip = memoryTooltip(e).replace(/"/g, '&quot;');
    return `
        <div class="memory-item${e.archived ? ' archived' : ''}" title="${tip}">
            <span class="memory-type">${memoryTypeLabel(e)}</span>
            <span class="memory-key">${e.key}</span>: <span class="memory-value">${e.value}</span>
            ${history}
            <span class="memory-stars">${stars}</span>
            <span class="memory-delete" onclick="deleteMemory('${e.key}')" title="删除">✕</span>
        </div>`;
}

function renderMemory(entries) {
    const listDiv = document.getElementById('memoryList');
    if (!listDiv) return;
    if (!entries || entries.length === 0) {
        listDiv.innerHTML = '<div class="empty-state">暂无记忆（发送"我喜欢用Python"试试）</div>';
        return;
    }

    const active = entries.filter(e => !e.archived);
    const archived = entries.filter(e => e.archived);

    // Group active entries by type, fixed display order.
    const order = ['preference', 'identity', 'rule', 'fact', 'episode'];
    const groups = {};
    for (const e of active) {
        const t = memoryTypeLabel(e);
        (groups[t] = groups[t] || []).push(e);
    }

    let html = '';
    for (const t of order) {
        if (!groups[t]) continue;
        html += `<div class="memory-group-title">${t}（${groups[t].length}）</div>`;
        html += groups[t].map(memoryItemHTML).join('');
        delete groups[t];
    }
    // Any non-standard types still get rendered.
    for (const t of Object.keys(groups)) {
        html += `<div class="memory-group-title">${t}（${groups[t].length}）</div>`;
        html += groups[t].map(memoryItemHTML).join('');
    }

    if (archived.length > 0) {
        html += `<details class="memory-archived"><summary>已归档（${archived.length}）</summary>`;
        html += archived.map(memoryItemHTML).join('');
        html += '</details>';
    }

    listDiv.innerHTML = html || '<div class="empty-state">暂无记忆</div>';
}

async function consolidateMemory() {
    try {
        const result = await api('POST', '/api/memory/consolidate', {});
        let msg = '🧹 整合完成';
        if (result.archived_count > 0) msg += `，归档 ${result.archived_count} 条`;
        if (result.profile_updated) msg += '，画像已更新';
        if (result.new_fact_keys && result.new_fact_keys.length) msg += `，沉淀新事实: ${result.new_fact_keys.join(', ')}`;
        if (result.skipped) msg += `（${result.skipped}）`;
        pushMessage(currentThread, 'system', msg);
        refreshMemory();
    } catch (e) {
        alert('整合失败：' + e.message);
    }
}

async function deleteMemory(key) {
    try {
        await api('DELETE', '/api/memory/' + key);
        refreshMemory();
    } catch (e) {
        alert('删除失败：' + e.message);
    }
}

// ========== Documents (per-user RAG corpus) ==========

async function refreshDocuments() {
    const listDiv = document.getElementById('documentList');
    if (!listDiv) return;
    listDiv.innerHTML = '<div class="empty-state">加载中...</div>';
    try {
        renderDocuments(await api('GET', '/api/documents'));
    } catch (e) {
        listDiv.innerHTML = '<div class="empty-state">加载失败，点击刷新重试</div>';
    }
    const hint = document.getElementById('docHint');
    if (hint) {
        hint.textContent = '提示：检索质量取决于 EMBEDDING_PROVIDER。默认的 hash 伪嵌入只能做词面匹配，接入 openai 或 ollama 后才是真正的语义检索。';
    }
}

// loadDocumentFile reads a .txt/.md file in the browser and fills the textarea, so
// the upload stays a plain JSON request — no multipart handling on either side.
function loadDocumentFile(input) {
    const file = input.files && input.files[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
        document.getElementById('docContent').value = String(reader.result || '');
        const nameField = document.getElementById('docName');
        if (!nameField.value) nameField.value = file.name.replace(/\.(txt|md)$/i, '');
    };
    reader.onerror = () => alert('读取文件失败');
    reader.readAsText(file);
    input.value = ''; // allow re-selecting the same file
}

async function uploadDocument() {
    const name = document.getElementById('docName').value.trim();
    const content = document.getElementById('docContent').value.trim();
    if (!name || !content) {
        alert('请填写文档名称与内容');
        return;
    }
    try {
        const doc = await api('POST', '/api/documents', { name, content });
        document.getElementById('docName').value = '';
        document.getElementById('docContent').value = '';
        pushMessage(currentThread, 'system', `📄 已上传文档《${doc.name}》，切分为 ${doc.chunks} 个片段`);
        renderChat();
        refreshDocuments();
    } catch (e) {
        alert('上传失败：' + e.message);
    }
}

async function deleteDocument(id, name) {
    if (!confirm(`确定删除文档《${name}》吗？`)) return;
    try {
        await api('DELETE', '/api/documents/' + id);
        refreshDocuments();
    } catch (e) {
        alert('删除失败：' + e.message);
    }
}

function renderDocuments(docs) {
    const listDiv = document.getElementById('documentList');
    if (!listDiv) return;
    if (!docs || docs.length === 0) {
        listDiv.innerHTML = '<div class="empty-state">还没有文档。上传一份资料后提问，回答会引用其中的片段。</div>';
        return;
    }
    listDiv.innerHTML = docs.map(d => `
        <div class="document-item">
            <div class="document-title">${escapeHtml(d.name)}</div>
            <div class="document-meta">${d.chunks} 个片段 · ${d.chars} 字
                <span class="document-delete" onclick="deleteDocument('${d.id}', '${escapeHtml(d.name)}')" title="删除文档">✕</span>
            </div>
        </div>`).join('');
}

// ========== Events ==========
function renderEvents(events) {
    const listDiv = document.getElementById('eventList');
    listDiv.innerHTML = (events || []).map(e => {
        let cls = '';
        let icon = '•';
        if (e.type === 'supervisor_route') { cls = 'route'; icon = '🔀'; }
        else if (e.type === 'tool_call_start') { cls = 'tool'; icon = '🔧'; }
        else if (e.type === 'tool_call_end') { cls = 'tool'; icon = '✅'; }
        else if (e.type === 'acl_denied') { cls = 'denied'; icon = '🚫'; }
        else if (e.type === 'hitl_interrupt') { cls = 'interrupt'; icon = '⏸️'; }
        else if (e.type === 'hitl_resume') { cls = 'interrupt'; icon = '▶️'; }
        else if (e.type === 'summary_compress') { cls = 'route'; icon = '📦'; }
        else if (e.type === 'agent_start') { cls = 'route'; icon = '🤖'; }
        else if (e.type === 'model_call_start') { cls = 'tool'; icon = '💭'; }
        else if (e.type === 'model_call_end') { cls = 'tool'; icon = '💬'; }
        return `<div class="event-item ${cls}">${icon} ${e.detail || e.type}</div>`;
    }).join('');
    listDiv.scrollTop = listDiv.scrollHeight;
}

// ========== Demo Scripts ==========
async function runDemo(type) {
    // Pin the demo to the thread selected at start: switching to another
    // thread mid-demo must not redirect the remaining scripted messages.
    const demoThread = currentThread;
    switch (type) {
        // 场景 1：Visitor 越权 — ACL 拒绝
        case 'visitor_deny':
            if (!currentUser.roles.includes('visitor')) {
                alert('请先使用 visitor 角色账号登录');
                return;
            }
            pushMessage(demoThread, 'system', '🎬 演示开始：visitor 尝试删除订单 → ACL 拒绝');
            renderChat();
            document.getElementById('chatInput').value = '删除订单A-1001';
            await sendMessageAsync(demoThread);
            break;

        // 场景 2：Admin 审批 — 高危工具审批
        case 'admin_approve':
            if (!currentUser.roles.includes('admin')) {
                alert('请先使用 admin 角色账号登录');
                return;
            }
            pushMessage(demoThread, 'system', '🎬 演示开始：删除订单 → 对话内审批卡片 → 批准/拒绝');
            renderChat();
            document.getElementById('chatInput').value = '删除订单A-1001';
            await sendMessageAsync(demoThread);
            break;

        // 场景 3：不同工具路由 — Supervisor 路由到不同子 Agent
        case 'diff_tools':
            pushMessage(demoThread, 'system', '🎬 演示开始：依次调用不同工具，展示 Supervisor 路由');
            renderChat();
            for (const msg of ['计算 123*456', '上海天气怎么样', '查询我的订单']) {
                document.getElementById('chatInput').value = msg;
                await sendMessageAsync(demoThread);
                await sleep(500);
            }
            break;

        // 场景 4：长对话裁剪 — 上下文管理 + LLM 摘要压缩
        case 'long_chat':
            pushMessage(demoThread, 'system', '🎬 演示开始：连续多轮对话 → 触发上下文裁剪 → 事件面板显示 summary_compress');
            renderChat();
            const chatMsgs = [
                '你好，我是admin',
                '查询我的订单',
                '计算 99*88',
                '北京天气怎么样',
                '查询我的订单',
                '计算 1024/8',
                '查询我的订单',
                '上海天气怎么样',
                '查询我的订单',
                '计算 256+512',
            ];
            for (let i = 0; i < chatMsgs.length; i++) {
                document.getElementById('chatInput').value = chatMsgs[i];
                await sendMessageAsync(demoThread);
                await sleep(300);
            }
            const eventItems = document.querySelectorAll('#eventList .event-item');
            const hasCompress = Array.from(eventItems).some(el => el.textContent.includes('compress'));
            if (hasCompress) {
                pushMessage(demoThread, 'system', '✅ 上下文裁剪已触发！查看右侧运行事件面板的 📦 事件。');
            } else {
                pushMessage(demoThread, 'system', '💡 当前消息量尚未超过裁剪阈值。可继续对话，或启动时设低阈值：MAX_TOKENS=1500 SUMMARIZE_THRESHOLD_RATIO=0.5 ./agent-server.exe');
            }
            renderChat();
            break;

        // 场景 5：记忆管理 — 偏好保存 + 跨会话验证
        case 'memory':
            pushMessage(demoThread, 'system', '🎬 演示开始：表达偏好 → 保存记忆 → 验证跨会话记忆');
            renderChat();
            document.getElementById('chatInput').value = '我喜欢用Python，偏好深色主题';
            await sendMessageAsync(demoThread);
            await sleep(500);
            document.getElementById('chatInput').value = '我的偏好吗？';
            await sendMessageAsync(demoThread);
            await sleep(500);
            await refreshMemory();
            pushMessage(demoThread, 'system', '💡 查看右侧 🧠 用户记忆面板确认偏好已保存。切换会话后再次询问偏好可验证跨会话记忆。');
            renderChat();
            break;
    }
}

async function sendMessageAsync(threadId = currentThread) {
    const input = document.getElementById('chatInput');
    const message = input.value.trim();
    if (!message) return;
    input.value = '';
    pushMessage(threadId, 'user', message);
    renderChat();
    try {
        await chatStream(threadId, message);
    } catch (e) {
        pushMessage(threadId, 'acl-denied', `❌ ${e.message}`);
        renderChat();
    }
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

// ========== Model Switching ==========
async function loadModels() {
    console.log('[ModelSwitch] loadModels called');
    try {
        const data = await api('GET', '/api/models');
        console.log('[ModelSwitch] API response:', data);
        modelProfiles = data.models || [];
        currentModelId = data.current_model || 'mock';
        renderModels();
    } catch (e) {
        console.error('[ModelSwitch] loadModels failed:', e);
    }
}

function renderModels() {
    const container = document.getElementById('modelSelector');
    console.log('[ModelSwitch] renderModels, container:', container, 'profiles:', modelProfiles.length);
    if (!container) return;

    if (modelProfiles.length === 0) {
        container.innerHTML = '<div class="empty-state">加载模型列表失败</div>';
        return;
    }

    container.innerHTML = modelProfiles.map(m => {
        const isActive = m.id === currentModelId;
        const keyStatus = !m.needs_api_key ? 'no-need'
            : m.has_api_key ? 'has-key'
            : 'no-key';
        const keyLabel = !m.needs_api_key ? '无需Key'
            : m.has_api_key ? '🔑 已配置'
            : '⚠️ 需Key';
        // For models with a configured key, show an edit icon to update it
        const editKeyBtn = m.needs_api_key && m.has_api_key
            ? `<span class="model-edit-key" onclick="event.stopPropagation(); editApiKey('${m.id}')" title="修改 API Key">✏️</span>`
            : '';
        return `<div class="model-item ${isActive ? 'active' : ''}" onclick="switchModel('${m.id}')">
            <span class="model-dot ${m.id}"></span>
            <span class="model-name">${m.name}${isActive ? ' ✓' : ''}</span>
            <span class="model-key-status ${keyStatus}">${keyLabel}</span>
            ${editKeyBtn}
        </div>`;
    }).join('');
}

async function switchModel(profileId) {
    // Find the profile
    const profile = modelProfiles.find(m => m.id === profileId);
    if (!profile) return;

    // If already the current model, open key editor for models that need keys
    if (profileId === currentModelId) {
        if (profile.needs_api_key) {
            editApiKey(profileId);
        }
        return;
    }

    // If needs API key and doesn't have one, show modal
    if (profile.needs_api_key && !profile.has_api_key) {
        pendingSwitchProfileId = profileId;
        document.getElementById('apiKeyModalTitle').textContent = '🔑 输入 API Key';
        document.getElementById('apiKeyModalDesc').textContent =
            `切换到 ${profile.name} 需要 API Key，请输入。`;
        document.getElementById('apiKeyInput').value = '';
        document.getElementById('apiKeyModal').style.display = 'flex';
        return;
    }

    // Switch directly (no key needed or already stored)
    await doSwitchModel(profileId, '');
}

// editApiKey opens the modal to update the API key for a model profile
function editApiKey(profileId) {
    const profile = modelProfiles.find(m => m.id === profileId);
    if (!profile) return;
    pendingSwitchProfileId = profileId;
    document.getElementById('apiKeyModalTitle').textContent = '✏️ 修改 API Key';
    document.getElementById('apiKeyModalDesc').textContent =
        `修改 ${profile.name} 的 API Key。留空则保持不变。`;
    document.getElementById('apiKeyInput').value = '';
    document.getElementById('apiKeyInput').placeholder = '输入新的 API Key...';
    document.getElementById('apiKeyModal').style.display = 'flex';
}

async function doSwitchModel(profileId, apiKey) {
    try {
        const data = await api('POST', '/api/models/switch', {
            profile_id: profileId,
            api_key: apiKey || undefined,
        });
        currentModelId = data.current_model || profileId;
        // Update has_api_key status
        const profile = modelProfiles.find(m => m.id === profileId);
        if (profile && apiKey) {
            profile.has_api_key = true;
        }
        renderModels();
        pushMessage(currentThread, 'assistant', `🔄 已切换到模型：${profile ? profile.name : profileId}`);
        renderChat();
    } catch (e) {
        alert('模型切换失败：' + e.message);
    }
}

function confirmApiKey() {
    const apiKey = document.getElementById('apiKeyInput').value.trim();
    if (!apiKey) {
        alert('请输入 API Key');
        return;
    }
    document.getElementById('apiKeyModal').style.display = 'none';
    doSwitchModel(pendingSwitchProfileId, apiKey);
    pendingSwitchProfileId = null;
}

function cancelApiKey() {
    document.getElementById('apiKeyModal').style.display = 'none';
    pendingSwitchProfileId = null;
}

// ========== Context Token Bar ==========
// Refresh the token bar with a thread's own usage (read-only recount on the
// server), so switching conversations shows that conversation's numbers
// instead of stale values from the last active thread.
async function refreshTokenBar(threadId = currentThread) {
    try {
        const info = await api('GET', `/api/chat/${threadId}/tokens`);
        if (info && typeof info.current === 'number') {
            updateTokenBar(info);
        }
    } catch (e) {
        // Visible in console: usually means the server predates this endpoint
        // (needs rebuild/restart) or the session expired.
        console.warn('[TokenBar] refresh failed for', threadId, e.message || e);
    }
}

function updateTokenBar(info, actualUsage) {
    const fill = document.getElementById('tokenBarFill');
    const text = document.getElementById('tokenBarText');
    const thresholdMark = document.getElementById('tokenBarThreshold');
    if (!fill || !text) return;

    const current = info.current || 0;
    const max = info.max || 8000;
    const threshold = info.threshold || Math.floor(max * 0.8);

    // Bar width relative to max (cap at 100%)
    const pct = Math.min(100, Math.round((current / max) * 100));
    fill.style.width = pct + '%';

    // Threshold marker position
    const thresholdPct = Math.min(100, Math.round((threshold / max) * 100));
    thresholdMark.style.left = thresholdPct + '%';

    // Color by usage
    fill.className = 'token-bar-fill';
    if (info.compressed) {
        fill.classList.add('compress');
    } else if (current >= threshold) {
        fill.classList.add('warn');
    } else if (pct >= 70) {
        fill.classList.add('warn');
    }

    const compressedBadge = info.compressed ? ' <span class="compressed-badge">📦 已压缩</span>' : '';
    // The bar shows a local estimate; the provider's own count is the authority
    // when it reports one.
    let actualNote = '';
    if (actualUsage && actualUsage.last_prompt_tokens) {
        actualNote = ` ｜ 实际 ${actualUsage.last_prompt_tokens}（${actualUsage.calls} 次调用，输出 ${actualUsage.completion_tokens}）`;
    }
    text.innerHTML = `上下文: ${current} / ${max} tokens（阈值 ${threshold}）${compressedBadge}${actualNote}`;
}
