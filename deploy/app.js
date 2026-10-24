(function () {
  'use strict';

  var root = document.getElementById('ewl-app');
  if (!root) return;

  var phases = {
    connected: '已连接',
    connecting: '正在连接',
    synchronizing: '正在同步开关',
    reconnecting: '连接中断，自动重试中',
    authorization_required: '需要重新登录',
    waiting_clock: '等待路由器校时',
    waiting_devices: '所有映射已暂停',
    stopped: '服务未运行',
    starting: '正在启动'
  };
  var eventLabels = {
    connecting_cloud: '开始连接云端',
    cloud_connected: '云端连接成功，正在同步开关',
    baseline_no_wake: '完成初始同步，未触发唤醒',
    state_push: '收到虚拟开关变化',
    would_wake_dry_run: '测试通过：收到开启操作，未发送唤醒包',
    wake_sent: '已发送唤醒包',
    wake_failed: '唤醒包发送失败',
    service_stopped: '服务已停止',
    token_refreshed: '登录授权已自动刷新',
    no_enabled_targets: '所有设备映射已暂停',
    time_not_synchronized: '等待路由器时间同步',
    login_required: '需要登录易微联',
    cloud_connection_closed: '云端连接已断开，准备重连',
    websocket_disconnected: '云端连接已断开，准备重连',
    ui_enable: '已启用服务和开机自启动',
    ui_disable: '已停用服务和开机自启动',
    ui_reconnect: '已请求重新连接云端',
    ui_settings: '已保存运行设置',
    ui_mapping_saved: '已保存设备映射',
    ui_mapping_enabled: '已启用设备映射',
    ui_mapping_paused: '已暂停设备映射',
    ui_scan: '已同步云端虚拟开关',
    ui_login_success: '账号登录成功',
    ui_app_saved: '应用凭证已更新；请重新登录账号'
  };
  var timer;
  var failed = false;
  var requesting = false;
  var previousEvents = '';

  function formatTime(value) {
    if (!value || /^0001/.test(value)) return '暂无记录';
    var date = new Date(value);
    if (isNaN(date.getTime())) return '暂无记录';
    return date.toLocaleString('zh-CN', {
      month: '2-digit', day: '2-digit', hour: '2-digit',
      minute: '2-digit', second: '2-digit', hour12: false
    });
  }

  function setText(key, value) {
    root.querySelectorAll('[data-value="' + key + '"]').forEach(function (element) {
      element.textContent = value;
    });
  }

  function eventDescription(event) {
    var label = eventLabels[event.kind];
    if (!label && /^cloud_error_/.test(event.kind)) {
      label = '云端返回错误（' + event.kind.replace('cloud_error_', '') + '）';
    }
    if (!label) label = '连接异常，客户端正在恢复';
    if (event.kind === 'state_push') {
      label += '：' + (event.state === 'on' ? '开启' : '关闭');
    }
    return label;
  }

  function renderEvents(items, targets) {
    var table = document.getElementById('ewl-event-list');
    if (!table) return;

    var events = Array.isArray(items) ? items.slice(0, 25) : [];
    var key = JSON.stringify(events);
    if (key === previousEvents) return;
    previousEvents = key;
    table.replaceChildren();

    if (!events.length) {
      var emptyRow = document.createElement('tr');
      var emptyCell = document.createElement('td');
      emptyCell.colSpan = 3;
      emptyCell.textContent = '暂无事件。服务启用后，连接和开关操作会显示在这里。';
      emptyRow.appendChild(emptyCell);
      table.appendChild(emptyRow);
      return;
    }

    events.forEach(function (event) {
      var row = document.createElement('tr');
      row.className = 'cbi-section-table-row';
      var date = document.createElement('td');
      var message = document.createElement('td');
      var device = document.createElement('td');
      var target = targets.find(function (item) { return item.device_id === event.device_id; });
      date.textContent = formatTime(event.time);
      message.textContent = eventDescription(event);
      device.textContent = target ? target.name : '';
      row.append(date, message, device);
      table.appendChild(row);
    });
  }

  function updateState(data) {
    var targets = Array.isArray(data.targets) ? data.targets : [];
    setText('phase', phases[data.phase] || '等待状态');
    setText('wake_count', data.wake_count || 0);
    setText('reconnect_count', data.reconnect_count || 0);
    setText('boot', data.boot_enabled ? '已启用开机自启动' : '未启用开机自启动');
    setText('last_seen', formatTime(data.last_seen));
    setText('started', formatTime(data.started));
    setText('service', data.running ? '服务运行中' : '服务未运行');

    root.querySelectorAll('[data-device]').forEach(function (row) {
      var target = targets.find(function (item) {
        return item.device_id === row.getAttribute('data-device');
      });
      if (!target) return;
      row.querySelector('[data-device-state]').textContent = !target.enabled
        ? '暂停监听' : !data.running ? '服务未运行'
        : target.state === 'on' ? '开启'
        : target.state === 'off' ? '关闭' : '等待同步';
      row.querySelector('[data-device-time]').textContent = formatTime(target.last_wake);
    });

    renderEvents(data.events, targets);
    document.getElementById('ewl-updated').textContent =
      '显示更新于 ' + new Date().toLocaleTimeString('zh-CN', { hour12: false });
  }

  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(poll, failed ? 15000 : 5000);
  }

  function poll() {
    if (document.hidden || requesting) {
      schedule();
      return;
    }
    requesting = true;
    var controller = new AbortController();
    var timeout = setTimeout(function () { controller.abort(); }, 8000);
    fetch(root.getAttribute('data-state-url'), {
      credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
      headers: { Accept: 'application/json' }
    }).then(function (response) {
      if (response.status === 401 || response.status === 403) throw new Error('session');
      if (!response.ok) throw new Error('network');
      if ((response.headers.get('content-type') || '').indexOf('application/json') < 0) {
        throw new Error('session');
      }
      return response.json();
    }).then(function (data) {
      failed = false;
      updateState(data);
      document.getElementById('ewl-refresh-error').hidden = true;
    }).catch(function (error) {
      failed = true;
      var notice = document.getElementById('ewl-refresh-error');
      notice.hidden = false;
      notice.textContent = error.message === 'session'
        ? '路由器登录已过期，请刷新页面后重新登录。当前服务运行不受页面登录影响。'
        : '暂时无法更新显示，请检查与路由器的连接。已显示的数据可能不是最新状态。';
    }).finally(function () {
      clearTimeout(timeout);
      requesting = false;
      schedule();
    });
  }

  root.querySelectorAll('[data-time]').forEach(function (element) {
    element.textContent = formatTime(element.getAttribute('data-time'));
  });
  root.querySelectorAll('[data-toggle]').forEach(function (button) {
    button.addEventListener('click', function () {
      var id = button.getAttribute('data-toggle');
      var target = document.getElementById(id);
      if (!target) return;
      target.hidden = !target.hidden;
      root.querySelectorAll('[data-toggle="' + id + '"]').forEach(function (item) {
        item.setAttribute('aria-expanded', String(!target.hidden));
      });
      if (!target.hidden) {
        var input = target.querySelector('input:not([type=hidden])');
        if (input) input.focus();
      }
    });
  });
  root.querySelectorAll('form').forEach(function (form) {
    form.addEventListener('submit', function (event) {
      var warning = form.getAttribute('data-confirm');
      if (warning && !window.confirm(warning)) {
        event.preventDefault();
        return;
      }
      var button = form.querySelector('button[type=submit]');
      if (!button) return;
      button.dataset.originalLabel = button.textContent;
      button.disabled = true;
      button.setAttribute('aria-busy', 'true');
      button.textContent = '正在处理…';
    });
  });
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) {
      clearTimeout(timer);
      poll();
    }
  });
  window.addEventListener('pageshow', function (event) {
    if (!event.persisted) return;
    root.querySelectorAll('button[aria-busy]').forEach(function (button) {
      button.disabled = false;
      button.removeAttribute('aria-busy');
      button.textContent = button.dataset.originalLabel || '重试';
    });
  });
  poll();
}());
