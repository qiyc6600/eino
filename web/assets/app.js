// ========== State ==========
let sessionId = '';
let currentUser = null;
let currentThread = 't_default';
let threads = [];
let isStreaming = false; // prevent duplicate sends
let currentModelId = 'mock'; // current model profile ID
let modelProfiles = []; // available model profiles
let pendingSwitchProfileId = null; // profile waiting for API key
let currentApprovals = []; // current pending approval requests

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
async function api(method, path, body) {
    const opts = {
        method,
        headers: { 'Content-Type': 'application/json' },
    };
    if (sessionId) {
        opts.headers['Authorization'] = `Bearer ${sessionId}`;
    }
    if (body) {
        opts.body = JSON.stringify(body);
    }
    const resp = await fetch(path, opts);
    const data = await resp.json();
    if (!resp.ok) {
        throw new Error(data.error || `HTTP ${resp.status}`);
    }
    return data;
}

// ========== Login ==========
async function doLogin() {
    const username = document.getElementById('loginUsername').value;
    const password = document.getElementById('loginPassword').value;
    try {
        const data = await api('POST', '/api/auth/login', { username, password });
        sessionId = data.sessionId;
        currentUser = data.user;
        showMainApp();
    } catch (e) {
        alert('登录失败：' + e.message);
    }
}

function quickLogin(username, password) {
    document.getElementById('loginUsername').value = username;
    document.getElementById('loginPassword').value = password;
    doLogin();
}

async function doLogout() {
    try { await api('POST', '/api/auth/logout'); } catch (e) {}
    sessionId = '';
    currentUser = null;
    document.getElementById('loginPage').classList.add('active');
    document.getElementById('mainApp').classList.remove('active');
}

// ========== Main App ==========
async function showMainApp() {
    document.getElementById('loginPage').classList.remove('active');
    document.getElementById('mainApp').classList.add('active');

    try {
        const me = await api('GET', '/api/auth/me');
        currentUser = me.user;
        renderUserInfo(me);
    } catch (e) {
        alert('获取用户信息失败：' + e.message);
        return;
    }

    loadTools();
    loadModels();
    refreshApprovals();
    refreshMemory();
    threads = loadThreadList();
    currentThread = threads[0] || 't_default';
    renderThreads();
    renderChat();
}

function renderUserInfo(me) {
    const userDiv = document.getElementById('currentUser');
    userDiv.innerHTML = `
        <div class="username">${me.user.username}</div>
        <div class="roles">${me.user.roles.map(r => `<span class="role-badge role-${r}">${r}</span>`).join(' ')}</div>
    `;
    const roleDiv = document.getElementById('roleInfo');
    const allTools = ['calculator', 'weather', 'grep', 'query_order', 'delete_order', 'send_email'];
    const allowedTools = me.tools || [];
    roleDiv.innerHTML = allTools.map(t => {
        const allowed = allowedTools.includes(t);
        return `<div class="perm-item ${allowed ? '' : 'deny'}">${allowed ? '✅' : '🚫'} ${t}</div>`;
    }).join('');
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
}

function newThread() {
    const tid = 't_' + Date.now().toString(36);
    threads.push(tid);
    saveThreadList(threads);
    saveMessages(tid, []);
    currentThread = tid;
    renderThreads();
    renderChat();
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
    try { await fetch(`/api/chat/${threadId}/delete`, { method: 'DELETE', headers: { 'Authorization': `Bearer ${sessionId}` } }); } catch (e) {}

    // If deleting current thread, switch to the first one
    if (threadId === currentThread) {
        currentThread = threads[0];
    }

    renderThreads();
    renderChat();
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
        const escaped = escapeHtml(msg.content);
        const cls = msg.role === 'user' ? 'msg-user' :
                    msg.role === 'assistant' ? 'msg-assistant' :
                    msg.role === 'interrupt' ? 'msg-interrupt' :
                    msg.role === 'acl-denied' ? 'msg-acl-denied' :
                    msg.role === 'approval' ? 'msg-approval' :
                    msg.role === 'system' ? 'msg-system' : 'msg-assistant';
        // Use data-streaming attribute to mark the streaming message
        const streaming = msg._streaming ? ' data-streaming="true"' : '';
        html += `<div class="msg ${cls}" style="white-space:pre-wrap"${streaming}>${escaped}</div>`;
    }

    container.innerHTML = html;
    container.scrollTop = container.scrollHeight;
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

    // Save user message
    pushMessage(currentThread, 'user', message);
    renderChat();

    isStreaming = true;

    try {
        await chatStream(currentThread, message);
    } catch (e) {
        // Fallback: if streaming fails, try non-streaming
        try {
            const data = await api('POST', '/api/agent/chat', { threadId: currentThread, message });
            if (data.status === 'interrupted') {
                pushMessage(currentThread, 'interrupt',
                    `⏸️ 运行已中断，等待审批\n工具：${data.interrupt.tool_name}\n原因：${data.interrupt.message}`);
                refreshApprovals();
            } else if (data.status === 'completed') {
                pushMessage(currentThread, 'assistant', data.answer || '（无回复）');
                if (data.memory && data.memory.length > 0) {
                    renderMemory(data.memory);
                } else {
                    setTimeout(refreshMemory, 500);
                }
            } else {
                pushMessage(currentThread, 'acl-denied', `❌ ${data.answer}`);
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
}

// chatStream sends a message and reads the SSE stream
async function chatStream(threadId, message) {
    const resp = await fetch('/api/agent/chat', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${sessionId}`,
        },
        body: JSON.stringify({ threadId, message, stream: true }),
    });

    if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(err.error || `HTTP ${resp.status}`);
    }

    // Add a placeholder assistant message marked as streaming
    const msgs = loadMessages(threadId);
    msgs.push({ role: 'assistant', content: '', _streaming: true });
    saveMessages(threadId, msgs);
    renderChat();

    let fullContent = '';
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
                            // Tool call notification — could show in UI
                        } else if (eventType === 'done') {
                            // Handle HITL interrupt: show interrupt message + refresh approval cards
                            if (data.status === 'interrupted' && data.interrupt) {
                                const interruptInfo = data.interrupt;
                                const interruptMsg = `⏸️ 运行已中断，等待审批\n工具：${interruptInfo.tool_name || interruptInfo.node_name || ''}\n原因：${interruptInfo.message || ''}`;
                                finalizeStreamingMessage(threadId, interruptMsg);
                                // Mark as interrupt type for distinct rendering
                                const msgs = loadMessages(threadId);
                                for (let i = msgs.length - 1; i >= 0; i--) {
                                    if (!msgs[i]._streaming) {
                                        msgs[i].role = 'interrupt';
                                        break;
                                    }
                                }
                                saveMessages(threadId, msgs);
                                renderChat();
                                // Show the approval modal immediately using the interrupt info
                                // from the SSE response (don't wait for the approvals API round-trip).
                                showApprovalModalFromInterrupt(interruptInfo);
                                // Also refresh the full approvals list (async, updates sidebar + modal).
                                refreshApprovals();
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
                                updateTokenBar(data.contextTokens);
                            }
                        } else if (eventType === 'error') {
                            finalizeStreamingMessage(threadId, `❌ ${data.error}`);
                            renderChat();
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
let dismissedInterruptIds = new Set();

async function refreshApprovals() {
    try {
        const data = await api('GET', '/api/approvals');
        currentApprovals = data || [];
        renderApprovals(currentApprovals);
    } catch (e) {}
}

function renderApprovals(approvals) {
    // Sidebar: show a compact indicator with pending count
    const listDiv = document.getElementById('approvalList');
    const pending = (approvals || []).filter(a => a.Status === 'pending');

    if (pending.length === 0) {
        listDiv.innerHTML = '<div class="empty-state">无待审批项</div>';
        closeApprovalModal();
        dismissedInterruptIds.clear();
        return;
    }

    listDiv.innerHTML = `
        <div class="approval-indicator" onclick="openApprovalModal()">
            <span class="indicator-dot"></span>
            <span class="indicator-text">${pending.length} 个待审批</span>
        </div>
        <button onclick="openApprovalModal()" class="btn btn-sm btn-full" style="margin-top:6px;">查看并处理</button>
    `;

    // Auto-open the modal if there's a new pending interrupt the user hasn't
    // dismissed yet. If all current pending items were already dismissed, keep
    // the modal closed (user chose "稍后处理").
    const hasNew = pending.some(a => !dismissedInterruptIds.has(a.InterruptID));
    if (hasNew) {
        openApprovalModal();
    }
}

function renderApprovalCards(containerId, approvals) {
    const container = document.getElementById(containerId);
    container.innerHTML = approvals.map(a => `
        <div class="approval-card">
            <div class="card-title">${a.Type === 'tool' ? '🔧 ' : '📋 '}${a.ToolName || a.NodeName}</div>
            <div class="card-detail">
                ${a.Arguments ? `参数：${a.Arguments}<br>` : ''}
                风险：${a.RiskLevel || 'N/A'}<br>
                原因：${a.Message || 'N/A'}
            </div>
            <div class="card-actions">
                <input type="text" placeholder="拒绝原因（可选）" id="rr-${a.InterruptID}">
                <button class="btn btn-sm btn-approve" onclick="decideApproval('${a.InterruptID}', true)">批准</button>
                <button class="btn btn-sm btn-reject" onclick="decideApproval('${a.InterruptID}', false)">拒绝</button>
            </div>
        </div>
    `).join('');
}

function openApprovalModal() {
    const modal = document.getElementById('approvalModal');
    const pending = currentApprovals.filter(a => a.Status === 'pending');
    if (pending.length === 0) return;
    renderApprovalCards('approvalModalList', pending);
    modal.style.display = 'flex';
}

// Show the approval modal immediately using interrupt info from the SSE
// response, without waiting for the /api/approvals round-trip.
function showApprovalModalFromInterrupt(info) {
    const modal = document.getElementById('approvalModal');
    const card = document.createElement('div');
    card.className = 'approval-card';
    card.innerHTML = `
        <div class="card-title">${info.type === 'tool' ? '🔧 ' : '📋 '}${info.tool_name || info.node_name || ''}</div>
        <div class="card-detail">
            ${info.arguments ? '参数：' + info.arguments + '<br>' : ''}
            风险：high<br>
            原因：${info.message || 'N/A'}
        </div>
        <div class="card-actions">
            <input type="text" placeholder="拒绝原因（可选）" id="rr-${info.interrupt_id}">
            <button class="btn btn-sm btn-approve" onclick="decideApproval('${info.interrupt_id}', true)">批准</button>
            <button class="btn btn-sm btn-reject" onclick="decideApproval('${info.interrupt_id}', false)">拒绝</button>
        </div>
    `;
    const list = document.getElementById('approvalModalList');
    list.innerHTML = '';
    list.appendChild(card);
    modal.style.display = 'flex';
}

function closeApprovalModal() {
    const modal = document.getElementById('approvalModal');
    if (modal.style.display === 'none') return;
    modal.style.display = 'none';
    // Mark all currently pending interrupts as dismissed so we don't auto-reopen
    // for the same items.
    currentApprovals.filter(a => a.Status === 'pending').forEach(a => {
        dismissedInterruptIds.add(a.InterruptID);
    });
}

async function decideApproval(interruptId, approved) {
    const reasonEl = document.getElementById(`rr-${interruptId}`);
    const reason = reasonEl ? reasonEl.value : '';
    try {
        const data = await api('POST', `/api/approvals/${interruptId}/decision`, { approved, reason });
        pushMessage(currentThread, 'approval',
            `${approved ? '✅' : '🚫'} 审批结果：${data.answer || (approved ? '已批准' : '已拒绝')}`);
        renderChat();
        // This interrupt is resolved — no longer dismissed-tracking needed.
        dismissedInterruptIds.delete(interruptId);
        await refreshApprovals();
    } catch (e) {
        alert('审批操作失败：' + e.message);
    }
}

// ========== Tools ==========
async function loadTools() {
    try {
        const tools = await api('GET', '/api/tools');
        renderTools(tools);
    } catch (e) {}
}

function renderTools(tools) {
    const listDiv = document.getElementById('toolList');
    const allowedTools = (currentUser && currentUser.roles) ?
        (currentUser.roles.includes('admin') ?
            ['calculator', 'weather', 'grep', 'query_order', 'delete_order', 'send_email'] :
            ['calculator', 'weather', 'query_order']) : [];
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
    try {
        const data = await api('GET', '/api/memory');
        console.log('[Memory] data:', data);
        renderMemory(data);
    } catch (e) {
        console.error('[Memory] refresh failed:', e);
        listDiv.innerHTML = '<div class="empty-state">加载失败，点击刷新重试</div>';
    }
}

function renderMemory(entries) {
    const listDiv = document.getElementById('memoryList');
    if (!listDiv) return;
    if (!entries || entries.length === 0) {
        listDiv.innerHTML = '<div class="empty-state">暂无记忆（发送"我喜欢用Python"试试）</div>';
        return;
    }
    listDiv.innerHTML = entries.map(e => `
        <div class="memory-item">
            <span class="memory-key">${e.key}</span>: <span class="memory-value">${e.value}</span>
            <span class="memory-delete" onclick="deleteMemory('${e.key}')" title="删除">✕</span>
        </div>
    `).join('');
}

async function deleteMemory(key) {
    try {
        await api('DELETE', '/api/memory/' + key);
        refreshMemory();
    } catch (e) {
        alert('删除失败：' + e.message);
    }
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
    switch (type) {
        // 场景 1：Visitor 越权 — ACL 拒绝
        case 'visitor_deny':
            if (currentUser.username !== 'visitor') {
                alert('请先用 visitor 账号登录（visitor / visitor123）');
                return;
            }
            pushMessage(currentThread, 'system', '🎬 演示开始：visitor 尝试删除订单 → ACL 拒绝');
            renderChat();
            document.getElementById('chatInput').value = '删除订单A-1001';
            await sendMessageAsync();
            break;

        // 场景 2：Admin 审批 — 高危工具审批
        case 'admin_approve':
            if (currentUser.username !== 'admin') {
                alert('请先用 admin 账号登录（admin / admin123）');
                return;
            }
            pushMessage(currentThread, 'system', '🎬 演示开始：删除订单 → 触发审批弹窗 → 批准/拒绝');
            renderChat();
            document.getElementById('chatInput').value = '删除订单A-1001';
            await sendMessageAsync();
            break;

        // 场景 3：不同工具路由 — Supervisor 路由到不同子 Agent
        case 'diff_tools':
            pushMessage(currentThread, 'system', '🎬 演示开始：依次调用不同工具，展示 Supervisor 路由');
            renderChat();
            for (const msg of ['计算 123*456', '上海天气怎么样', '查询我的订单']) {
                document.getElementById('chatInput').value = msg;
                await sendMessageAsync();
                await sleep(500);
            }
            break;

        // 场景 4：长对话裁剪 — 上下文管理 + LLM 摘要压缩
        case 'long_chat':
            pushMessage(currentThread, 'system', '🎬 演示开始：连续多轮对话 → 触发上下文裁剪 → 事件面板显示 summary_compress');
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
                await sendMessageAsync();
                await sleep(300);
            }
            const eventItems = document.querySelectorAll('#eventList .event-item');
            const hasCompress = Array.from(eventItems).some(el => el.textContent.includes('compress'));
            if (hasCompress) {
                pushMessage(currentThread, 'system', '✅ 上下文裁剪已触发！查看右侧运行事件面板的 📦 事件。');
            } else {
                pushMessage(currentThread, 'system', '💡 当前消息量尚未超过裁剪阈值。可继续对话，或启动时设低阈值：MAX_TOKENS=1500 SUMMARIZE_THRESHOLD_RATIO=0.5 ./agent-server.exe');
            }
            renderChat();
            break;

        // 场景 5：记忆管理 — 偏好保存 + 跨会话验证
        case 'memory':
            pushMessage(currentThread, 'system', '🎬 演示开始：表达偏好 → 保存记忆 → 验证跨会话记忆');
            renderChat();
            document.getElementById('chatInput').value = '我喜欢用Python，偏好深色主题';
            await sendMessageAsync();
            await sleep(500);
            document.getElementById('chatInput').value = '我的偏好吗？';
            await sendMessageAsync();
            await sleep(500);
            await refreshMemory();
            pushMessage(currentThread, 'system', '💡 查看右侧 🧠 用户记忆面板确认偏好已保存。切换会话后再次询问偏好可验证跨会话记忆。');
            renderChat();
            break;
    }
}

async function sendMessageAsync() {
    const input = document.getElementById('chatInput');
    const message = input.value.trim();
    if (!message) return;
    input.value = '';
    pushMessage(currentThread, 'user', message);
    renderChat();
    try {
        await chatStream(currentThread, message);
    } catch (e) {
        pushMessage(currentThread, 'acl-denied', `❌ ${e.message}`);
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
function updateTokenBar(info) {
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
    text.innerHTML = `上下文: ${current} / ${max} tokens（阈值 ${threshold}）${compressedBadge}`;
}
