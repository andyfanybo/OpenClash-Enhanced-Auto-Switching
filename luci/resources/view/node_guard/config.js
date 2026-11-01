'use strict';
'require view';
'require request';
'require poll';
'require ui';
'require dom';

var base = ['admin', 'services', 'openclash'];
var healthLabels = {
	healthy: '正常', unavailable: '不可用', control_error: '接口异常', skipped: '已跳过'
};
var notes = {
	'check next candidate in next one-second batch': '下一轮检查后续候选',
	'no available next candidate': '本轮没有可用候选，继续检查',
	'switched after failed connectivity check': '故障后已自动切换',
	'selection changed through child group': '内层策略组已切换',
	'selection changed; discard old result': '选择已变化，忽略旧检测结果',
	'candidate changed; discard old result': '候选已变化，下一轮重新检查',
	'direct or policy action; skip': '当前为直连或拒绝等策略动作',
	'candidate control error; keep selection': '候选检测接口异常，保持当前选择',
	'cannot revalidate selection': '暂时无法确认选择，保持原节点',
	'switch not confirmed': '等待确认切换结果',
	'switch request failed; not confirmed': '切换请求未确认，将继续检测'
};

function txt(tag, value, attrs) {
	return E(tag, attrs || {}, [String(value == null ? '' : value)]);
}

function list(value) { return Array.isArray(value) ? value : []; }

function parseResponse(res) {
	var data;
	try { data = res.json(); }
	catch (e) { throw new Error('无法读取响应，请确认 LuCI 登录仍然有效'); }
	if (!res.ok || data.error) throw new Error(data.error || ('操作失败，HTTP ' + res.status));
	return data;
}

function getState(settings) {
	return request.get(L.url.apply(L, base.concat('node_guard_state')), {
		query: settings ? { settings: 1 } : {}, cache: false, timeout: 8000
	}).then(parseResponse);
}

function post(endpoint, data) {
	var form = new URLSearchParams();
	form.set('token', L.env.token);
	Object.keys(data).forEach(function(key) { form.set(key, data[key]); });
	return request.post(L.url.apply(L, base.concat(endpoint)), form.toString(), {
		headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, timeout: 15000
	}).then(parseResponse);
}

function timeLabel(value) {
	var date = new Date(value);
	return isNaN(date.getTime()) ? '尚无记录' : date.toLocaleString('zh-CN', { hour12: false });
}

function eventCells(event) {
	var match = String(event).match(/^(\S+) switch group=("(?:\\.|[^"\\])*") from=("(?:\\.|[^"\\])*") to=("(?:\\.|[^"\\])*") node=("(?:\\.|[^"\\])*")$/);
	if (match) {
		try {
			return [timeLabel(match[1]), JSON.parse(match[2]), JSON.parse(match[3]) + ' → ' + JSON.parse(match[4]), JSON.parse(match[5])];
		} catch (e) {}
	}
	return ['', '服务记录', String(event), ''];
}

return view.extend({
	load: function() { return getState(true); },

	field: function(label, input, description) {
		return E('div', { 'class': 'cbi-value' }, [
			txt('label', label, { 'class': 'cbi-value-title', 'for': input.id }),
			E('div', { 'class': 'cbi-value-field' }, [input, txt('div', description, { 'class': 'cbi-value-description' })])
		]);
	},

	button: function(label, className, handler) {
		var button = txt('button', label, { type: 'button', 'class': 'btn ' + className,
			click: ui.createHandlerFn(this, handler) });
		return button;
	},

	setDisabled: function() {
		var disabled = this.readonly || this.busy;
		this.root.querySelectorAll('input, select, button').forEach(function(input) { input.disabled = disabled; });
		var selectedOnly = this.mode.value === 'selected';
		this.groupInputs.forEach(function(input) {
			if (!selectedOnly) input.checked = input.dataset.excluded !== '1';
			input.disabled = disabled || !selectedOnly || input.dataset.excluded === '1';
		});
		this.startButton.disabled = disabled || !!this.running;
		this.stopButton.disabled = disabled || !this.running;
		this.restartButton.disabled = disabled || !this.running;
	},

	render: function(data) {
		if (!data.config) throw new Error('服务配置不可用');
		var config = data.config;
		this.readonly = !L.hasViewPermission();
		this.busy = false;
		this.running = data.running;
		this.groupInputs = [];
		this.enabled = E('input', { id: 'ng-enabled', type: 'checkbox' });
		this.enabled.checked = !!data.enabled;
		this.interval = E('input', { id: 'ng-interval', type: 'number', min: '0.1', max: '60', step: '0.1', value: config.interval_ms / 1000 });
		this.timeout = E('input', { id: 'ng-timeout', type: 'number', min: '100', max: '30000', step: '1', value: config.timeout_ms });
		this.url = E('input', { id: 'ng-url', type: 'url', value: config.probe_url, style: 'width:100%;max-width:42em' });
		this.mode = E('select', { id: 'ng-mode', change: L.bind(this.setDisabled, this) }, [
			txt('option', '全部支持的策略组', { value: 'all' }),
			txt('option', '仅选择下列策略组', { value: 'selected' })
		]);
		this.mode.value = list(config.groups).length ? 'selected' : 'all';
		var selected = list(config.groups), excluded = list(config.exclude_groups);
		var choices = list(data.choices).map(L.bind(function(group) {
			var input = E('input', { type: 'checkbox', value: group.name, 'data-excluded': excluded.indexOf(group.name) >= 0 ? '1' : '0' });
			input.checked = selected.indexOf(group.name) >= 0;
			this.groupInputs.push(input);
			return E('label', { 'class': 'ng-group-choice' }, [input, txt('span', group.name),
				txt('small', excluded.indexOf(group.name) >= 0 ? '已由高级配置排除' : group.kind)]);
		}, this));
		this.startButton = this.button('启动', 'cbi-button-positive', 'handleStart');
		this.stopButton = this.button('暂停', 'cbi-button-negative', 'handleStop');
		this.restartButton = this.button('重启小服务', 'cbi-button-action', 'handleRestart');
		this.saveButton = this.button('保存并应用', 'cbi-button-apply important', 'handleApply');
		this.summary = E('div', { 'class': 'ng-summary' });
		this.updated = E('p', { 'class': 'cbi-value-description' });
		this.error = E('div', { 'class': 'alert-message warning', style: 'display:none' });
		this.statusTable = E('div', { 'class': 'ng-table-wrap' });
		this.eventTable = E('div', { 'class': 'ng-table-wrap' });
		this.root = E('div', { id: 'ng-page' }, [
			E('style', {}, ['#ng-page .ng-summary{display:grid;grid-template-columns:repeat(auto-fit,minmax(135px,1fr));gap:12px;margin:18px 0}#ng-page .ng-stat{border:1px solid var(--border-color-medium,#ddd);border-radius:6px;padding:14px}#ng-page .ng-stat strong{display:block;font-size:1.4em;margin:6px 0}#ng-page .ng-groups{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:10px;padding:10px 0}#ng-page .ng-group-choice{display:flex;align-items:center;gap:8px;padding:6px}#ng-page .ng-group-choice small{opacity:.65;margin-left:auto}#ng-page .ng-table-wrap{overflow:auto}#ng-page td{overflow-wrap:anywhere;min-width:90px}#ng-page .ng-actions{display:flex;gap:8px;flex-wrap:wrap;margin:12px 0}#ng-page .ng-health{display:inline-block;border-radius:4px;padding:3px 7px;white-space:nowrap}#ng-page .ng-healthy{background:#e1f3e5;color:#206734}#ng-page .ng-unavailable,#ng-page .ng-control_error{background:#fbe8e5;color:#9b3025}#ng-page .ng-skipped{background:#eee;color:#555}']),
			E('h2', {}, [E('img', { src: L.resource('view/node_guard/icon.svg'), alt: 'OpenClash FF', width: '32', height: '32', style: 'vertical-align:middle;margin-right:10px' }), '节点自动切换']),
			txt('p', '跟随每个策略组当前选中的节点，定时检查连通性；失败后按组内顺序查找下一个可用节点。'),
			this.summary, this.error,
			E('div', { 'class': 'cbi-section' }, [
				txt('h3', '服务与检测设置'),
				this.field('启用并开机启动', this.enabled, '保存后应用启用状态。下方“暂停”只暂停当前运行，不改变开机设置。'),
				E('div', { 'class': 'ng-actions' }, [this.startButton, this.stopButton, this.restartButton]),
				this.field('检测间隔（秒）', this.interval, '默认每 1 秒一轮，只检查当前节点；相同节点合并检查。'),
				this.field('检测超时（毫秒）', this.timeout, '超过此时间按不可用处理。超时过短可能把短时抖动判为失败。'),
				this.field('检测地址', this.url, '使用小响应的 HTTP/HTTPS 地址。已有自动组会沿用组内设置的检测地址。'),
				this.field('监控范围', this.mode, '自动跟随手动或自动选择。直连、拒绝等策略动作会跳过。'),
				E('div', { 'class': 'ng-groups' }, choices.length ? choices : [txt('p', '暂未读取到策略组，请确认 OpenClash 正在运行后刷新页面。')]),
				data.choices_error ? txt('p', data.choices_error, { 'class': 'cbi-value-description' }) : null,
				E('div', { 'class': 'cbi-page-actions' }, [this.saveButton])
			]),
			E('div', { 'class': 'cbi-section' }, [txt('h3', '策略组状态'), this.updated, this.statusTable]),
			E('div', { 'class': 'cbi-section' }, [txt('h3', '最近切换记录'), this.eventTable])
		]);
		this.update(data);
		poll.add(L.bind(function() {
			return getState(false).then(L.bind(this.update, this)).catch(L.bind(function() {
				this.error.style.display = '';
				dom.content(this.error, [document.createTextNode('状态刷新失败，请确认 LuCI 登录仍然有效。')]);
			}, this));
		}, this), 3);
		return this.root;
	},

	update: function(data) {
		this.running = !!data.running;
		var status = data.status || {}, groups = list(status.groups);
		var normal = groups.filter(function(group) { return group.health === 'healthy'; }).length;
		var cards = [
			['服务状态', data.running ? '运行中' : '已暂停', data.enabled ? '已设置开机启动' : '未设置开机启动'],
			['当前策略组', normal + ' / ' + groups.length, '健康 / 已监控'],
			['累计切换', status.switches || 0, '自本次服务启动起'],
			['最近一轮', status.cycle_ms == null ? '—' : status.cycle_ms + ' ms', '检测轮数：' + (status.rounds || 0)]
		];
		dom.content(this.summary, cards.map(function(card) { return E('div', { 'class': 'ng-stat' }, [txt('span', card[0]), txt('strong', card[1]), txt('small', card[2])]); }));
		dom.content(this.updated, [document.createTextNode('上次更新：' + timeLabel(status.updated_at) + ' · 页面每 3 秒刷新' + (data.running ? '' : ' · 当前显示上次运行记录'))]);
		this.error.style.display = status.last_error ? '' : 'none';
		if (status.last_error) dom.content(this.error, [document.createTextNode('控制接口异常，服务正在等待恢复：' + status.last_error)]);
		var rows = groups.map(function(group) {
			return [group.group, group.selected, group.node || '—',
				txt('span', healthLabels[group.health] || '等待检测', { 'class': 'ng-health ng-' + (healthLabels[group.health] ? group.health : 'skipped') }),
				group.delay_ms ? group.delay_ms + ' ms' : '—', notes[group.note] || group.note || '—'];
		});
		this.table(this.statusTable, ['策略组', '选中成员', '实际节点', '连通性', '延迟', '说明'], rows, '服务运行后会在这里显示检测状态。');
		this.table(this.eventTable, ['时间', '策略组', '切换', '实际节点'], list(status.events).slice().reverse().map(eventCells), '暂无切换记录。');
		this.setDisabled();
	},

	table: function(target, titles, rows, empty) {
		var table = E('table', { 'class': 'table' }, [E('tr', { 'class': 'tr table-titles' }, titles.map(function(title) { return txt('th', title, { 'class': 'th' }); }))]);
		rows.forEach(function(row) {
			table.appendChild(E('tr', { 'class': 'tr' }, row.map(function(cell, i) { return E('td', { 'class': 'td', 'data-title': titles[i] }, [cell == null ? '—' : cell]); })));
		});
		if (!rows.length) table.appendChild(E('tr', { 'class': 'tr' }, [txt('td', empty, { 'class': 'td', colspan: titles.length })]));
		dom.content(target, [table]);
	},

	runAction: function(task, message) {
		if (this.readonly || this.busy) return Promise.resolve();
		this.busy = true;
		this.setDisabled();
		return task().then(function() {
			ui.addNotification(null, txt('p', message), 'info');
			return getState(false);
		}).then(L.bind(this.update, this)).catch(function(err) {
			ui.addNotification(null, txt('p', err.message), 'error');
		}).finally(L.bind(function() { this.busy = false; this.setDisabled(); }, this));
	},

	handleApply: function() {
		if (![this.interval, this.timeout, this.url].every(function(input) { return input.reportValidity(); })) return;
		var groups = this.groupInputs.filter(function(input) { return input.checked && input.dataset.excluded !== '1'; }).map(function(input) { return input.value; });
		if (this.mode.value === 'selected' && !groups.length) {
			ui.addNotification(null, txt('p', '请至少选择一个策略组'), 'error');
			return;
		}
		var data = { enabled: this.enabled.checked, interval_ms: Math.round(Number(this.interval.value) * 1000),
			timeout_ms: Number(this.timeout.value), probe_url: this.url.value.trim(), mode: this.mode.value, groups: groups };
		return this.runAction(function() { return post('node_guard_save', { data: JSON.stringify(data) }); }, '配置已保存并应用。');
	},

	handleStart: function() { return this.runAction(function() { return post('node_guard_control', { action: 'start' }); }, '已启动节点自动切换服务。'); },
	handleStop: function() { return this.runAction(function() { return post('node_guard_control', { action: 'stop' }); }, '服务已暂停，开机设置保持不变。'); },
	handleRestart: function() { return this.runAction(function() { return post('node_guard_control', { action: 'restart' }); }, '已重启节点自动切换服务。'); },
	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
