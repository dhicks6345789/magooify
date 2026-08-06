document.addEventListener('DOMContentLoaded', () => {
  const userBadge = document.getElementById('user-badge');
  const userName = document.getElementById('user-name');
  const userMode = document.getElementById('user-mode');
  const connBadge = document.getElementById('conn-badge');
  const infoMode = document.getElementById('info-mode');
  const infoUser = document.getElementById('info-user');
  const infoAuth = document.getElementById('info-auth');
  const infoGo = document.getElementById('info-go');
  const infoOs = document.getElementById('info-os');
  const infoUptime = document.getElementById('info-uptime');
  const itemsCount = document.getElementById('items-count');
  const itemsList = document.getElementById('items-list');
  const itemForm = document.getElementById('item-form');
  const itemName = document.getElementById('item-name');
  const apiResponse = document.getElementById('api-response');

  function escapeHtml(value) {
    const div = document.createElement('div');
    div.textContent = value == null ? '' : String(value);
    return div.innerHTML;
  }

  function setResponse(text) {
    apiResponse.textContent = text;
  }

  async function fetchJSON(url, options) {
    const res = await fetch(url, options);
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `Request to ${url} failed (${res.status})`);
    }
    return data;
  }

  async function loadUser() {
    try {
      const data = await fetchJSON('api/v1/user');
      userName.textContent = data.username;
      userMode.textContent = `${data.mode} mode (${data.auth_type})`;
      infoUser.textContent = data.username;
      infoAuth.textContent = data.auth_type;
      connBadge.classList.remove('text-bg-danger');
      connBadge.classList.add('text-bg-success');
      connBadge.textContent = 'connected';
      userBadge.style.opacity = '1';
    } catch (err) {
      userName.textContent = 'Offline / Disconnected';
      userMode.textContent = 'connection failed';
      infoUser.textContent = '-';
      infoAuth.textContent = '-';
      connBadge.classList.remove('text-bg-success');
      connBadge.classList.add('text-bg-danger');
      connBadge.textContent = 'disconnected';
    }
  }

  async function loadSystemInfo() {
    try {
      const data = await fetchJSON('api/v1/info');
      infoMode.textContent = data.mode;
      infoGo.textContent = data.go_version;
      infoOs.textContent = `${data.os}/${data.arch}`;
      infoUptime.textContent = data.uptime;
    } catch (err) {
      infoMode.textContent = '-';
      infoGo.textContent = '-';
      infoOs.textContent = '-';
      infoUptime.textContent = '-';
    }
  }

  async function loadItems() {
    try {
      const data = await fetchJSON('api/v1/items');
      const items = data.items || [];
      itemsCount.textContent = `${items.length} item${items.length === 1 ? '' : 's'}`;
      itemsList.innerHTML = '';
      if (items.length === 0) {
        itemsList.innerHTML = '<li class="list-group-item text-center text-secondary">No items found. Create one above!</li>';
        return;
      }
      items.forEach((item) => {
        const li = document.createElement('li');
        li.className = 'list-group-item d-flex justify-content-between align-items-center';
        const info = document.createElement('div');
        info.innerHTML =
          `<div class="fw-semibold">${escapeHtml(item.name)}</div>` +
          `<div class="small text-secondary">Created by ${escapeHtml(item.created_by)} at ${escapeHtml(new Date(item.created_at).toLocaleString())}</div>`;
        const badge = document.createElement('span');
        badge.className = 'badge text-bg-primary';
        badge.textContent = `ID: ${item.id}`;
        li.appendChild(info);
        li.appendChild(badge);
        itemsList.appendChild(li);
      });
    } catch (err) {
      itemsList.innerHTML = '<li class="list-group-item text-center text-secondary">Failed to load items.</li>';
    }
  }

  itemForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const name = itemName.value.trim();
    if (!name) return;
    try {
      const data = await fetchJSON('api/v1/items', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      itemName.value = '';
      setResponse(JSON.stringify(data, null, 2));
      loadItems();
    } catch (err) {
      setResponse(`Error adding item: ${err.message}`);
    }
  });

  document.querySelectorAll('[data-endpoint]').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const endpoint = btn.dataset.endpoint;
      setResponse('Fetching API response...');
      try {
        const data = await fetchJSON(endpoint);
        setResponse(JSON.stringify(data, null, 2));
      } catch (err) {
        setResponse(`Error calling ${endpoint}: ${err.message}`);
      }
    });
  });

  loadUser();
  loadSystemInfo();
  loadItems();
});
