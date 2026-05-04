// Optional, removable backup feed UI. All globals here are namespaced
// with `tm` / `telemirror` so removing this file (plus the markup block
// in index.html) drops the feature without touching anything else.
(function () {
  var tmChannels = [];
  var tmActive = '';
  var tmAvatarCache = {}; // username (lower) -> photo URL once we've fetched it

  function tmI18n(key, fallback) {
    try {
      var v = (typeof t === 'function') ? t(key) : '';
      return v && v !== key ? v : (fallback || '');
    } catch (e) { return fallback || ''; }
  }

  function tmEsc(s) {
    return (typeof esc === 'function') ? esc(s) : String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
  function tmEscAttr(s) {
    return (typeof escAttr === 'function') ? escAttr(s) :
      tmEsc(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  function tmToast(msg) {
    if (typeof showToast === 'function') showToast(msg);
  }

  function tmInitial(name) {
    if (!name) return '?';
    var ch = name.replace(/^@/, '').charAt(0);
    return ch ? ch.toUpperCase() : '?';
  }

  // Deterministic colour-from-name so the placeholder avatars don't all
  // look identical. Mirrors what Telegram's web client does.
  function tmAvatarColor(name) {
    var palette = ['#e57373', '#f06292', '#ba68c8', '#9575cd', '#7986cb',
                   '#64b5f6', '#4fc3f7', '#4dd0e1', '#4db6ac', '#81c784',
                   '#aed581', '#dce775', '#ffd54f', '#ffb74d', '#ff8a65'];
    var h = 0;
    for (var i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
    return palette[h % palette.length];
  }

  function tmAvatarHTML(username, name, size) {
    size = size || 40;
    var disp = name || username || '';
    var initial = '<span class="tm-avatar-initial">' + tmEsc(tmInitial(disp)) + '</span>';
    var bg = tmAvatarColor(disp || '?');
    var photo = tmAvatarCache[(username || '').toLowerCase()];
    var inner = initial;
    if (photo) {
      // Img falls back to the initial-letter span on load failure.
      inner = '<img src="' + tmEscAttr(photo) + '" loading="lazy" alt=""'
        + ' onerror="this.parentNode.classList.add(\'tm-avatar-fallback\');this.remove()">'
        + initial;
    }
    return '<div class="tm-avatar" style="width:' + size + 'px;height:' + size + 'px;background:' + bg + '">'
      + inner + '</div>';
  }

  // ===== open / close =====
  window.openTelemirror = function () {
    document.getElementById('telemirrorModal').classList.add('active');
    document.body.classList.add('tm-no-scroll');
    tmLoadChannels();
  };
  window.closeTelemirror = function () {
    document.getElementById('telemirrorModal').classList.remove('active');
    document.body.classList.remove('tm-no-scroll');
  };
  window.toggleTmSidebar = function () {
    var sb = document.getElementById('tmSidebar');
    if (sb) sb.classList.toggle('open');
  };

  // ===== channel list =====
  async function tmLoadChannels() {
    try {
      var r = await fetch('/api/telemirror/channels');
      var d = await r.json();
      tmChannels = (d.channels || []).slice();
    } catch (e) { tmChannels = []; }
    tmRenderChannels();
    if (!tmActive && tmChannels.length > 0) {
      tmSelect(tmChannels[0].username);
    } else if (tmActive) {
      tmSelect(tmActive);
    } else {
      document.getElementById('tmContent').innerHTML =
        '<div class="tm-empty">' + tmEsc(tmI18n('telemirror_pick_channel', 'Pick a channel')) + '</div>';
    }
  }

  function tmRenderChannels() {
    var box = document.getElementById('tmChannelsList');
    var html = '';
    for (var i = 0; i < tmChannels.length; i++) {
      var c = tmChannels[i];
      var active = (c.username.toLowerCase() === tmActive.toLowerCase()) ? ' active' : '';
      html += '<div class="tm-channel-item' + active + '" data-u="' + tmEscAttr(c.username) + '" onclick="tmSelectFromClick(this.dataset.u)">'
        + tmAvatarHTML(c.username, c.username, 40)
        + '<div class="tm-channel-item-meta">'
        +   '<div class="tm-channel-item-name">' + tmEsc(c.username) + (c.pinned ? ' <span class="tm-pin">📌</span>' : '') + '</div>'
        + '</div>';
      if (!c.pinned) {
        html += '<button class="tm-x" data-u="' + tmEscAttr(c.username) + '" onclick="event.stopPropagation();tmRemove(this.dataset.u)">&times;</button>';
      }
      html += '</div>';
    }
    box.innerHTML = html || '<div class="tm-empty">' + tmEsc(tmI18n('telemirror_pick_channel', 'Pick a channel')) + '</div>';
  }

  window.tmSelectFromClick = function (username) {
    tmSelect(username);
    // Mobile: collapse the sidebar drawer after picking.
    var sb = document.getElementById('tmSidebar');
    if (sb) sb.classList.remove('open');
  };

  function tmShowError(msg) {
    var content = document.getElementById('tmContent');
    content.innerHTML =
      '<div class="tm-empty"><p>' + tmEsc(tmI18n('telemirror_load_failed', 'Failed to load')) + '</p>'
      + '<pre style="white-space:pre-wrap;margin-top:10px;padding:10px;background:var(--bg-elevated,var(--bg));border:1px solid var(--border);border-radius:6px;color:var(--text-dim);font-size:11px;text-align:start;max-width:600px;direction:ltr">'
      + tmEsc(String(msg).slice(0, 2000))
      + '</pre></div>';
  }

  async function tmSelect(username) {
    tmActive = username;
    tmRenderChannels();
    tmRenderTopbar(null, username);
    var content = document.getElementById('tmContent');
    content.innerHTML = '<div class="tm-empty">' + tmEsc(tmI18n('telemirror_loading', 'Loading...')) + '</div>';
    try {
      var r = await fetch('/api/telemirror/channel/' + encodeURIComponent(username));
      if (!r.ok) {
        var errBody = '';
        try { errBody = await r.text(); } catch (e2) { }
        tmShowError(errBody || ('HTTP ' + r.status));
        return;
      }
      var d = await r.json();
      if (d && d.channel && d.channel.photo) {
        tmAvatarCache[username.toLowerCase()] = d.channel.photo;
        tmRenderChannels();
      }
      tmRenderTopbar(d && d.channel, username);
      tmRenderPosts(d);
    } catch (e) {
      tmShowError((e && e.message) || String(e));
    }
  }
  window.tmSelect = tmSelect;

  function tmRenderTopbar(channel, username) {
    var name = (channel && channel.title) || username;
    var sub = '';
    if (channel) {
      if (channel.subscribers) sub = channel.subscribers;
      else if (username) sub = '@' + username;
    } else if (username) {
      sub = '@' + username;
    }
    document.getElementById('tmTopbarAvatar').innerHTML = tmAvatarHTML(username, name, 38);
    document.getElementById('tmTopbarName').textContent = name || '';
    document.getElementById('tmTopbarSub').textContent = sub || '';
  }

  function tmRenderPosts(data) {
    var content = document.getElementById('tmContent');
    var posts = (data && data.posts) || [];
    var ch = (data && data.channel) || {};
    if (!posts.length) {
      content.innerHTML = '<div class="tm-empty">' + tmEsc(tmI18n('telemirror_no_posts', 'No posts')) + '</div>';
      return;
    }
    // Telegram order: oldest first, newest at the bottom (chat-like).
    posts.sort(function (a, b) { return (a.time || '').localeCompare(b.time || ''); });

    var html = '';
    if (ch.description) {
      html += '<div class="tm-channel-desc">' + ch.description + '</div>';
    }
    for (var i = 0; i < posts.length; i++) {
      var p = posts[i];
      var when = p.time ? new Date(p.time).toLocaleString() : '';
      html += '<div class="tm-post">';
      html += '<div class="tm-post-head">';
      if (ch.title) html += '<span class="tm-post-author">' + tmEsc(ch.title) + '</span>';
      html += '<span class="tm-post-time">' + tmEsc(when) + '</span>';
      if (p.edited) html += '<span class="tm-post-edited">' + tmEsc(tmI18n('telemirror_edited', 'edited')) + '</span>';
      html += '</div>';

      if (p.text) html += '<div class="tm-post-text">' + p.text + '</div>';

      if (p.media && p.media.length) {
        // Album-aware grid: 1 photo → fullwidth, 2 → 2 cols, 3+ → 3 cols.
        var gridClass = 'tm-post-media tm-album-' + Math.min(p.media.length, 3);
        html += '<div class="' + gridClass + '">';
        for (var j = 0; j < p.media.length; j++) {
          var m = p.media[j];
          if (m.type === 'photo' && m.thumb) {
            // No link wrapping — clicking a Translate-proxied permalink
            // just returns useless bytes via /api/telemirror/img.
            html += '<div class="tm-photo">'
              + '<img src="' + tmEscAttr(m.thumb) + '" loading="lazy" alt=""></div>';
          } else if (m.type === 'video') {
            var bg = m.thumb ? 'background-image:url(\'' + tmEscAttr(m.thumb) + '\')' : '';
            var dur = m.duration ? '<span class="tm-vid-dur">' + tmEsc(m.duration) + '</span>' : '';
            html += '<div class="tm-vid" style="' + bg + '">'
              + '<span class="tm-vid-play">&#9654;</span>' + dur + '</div>';
          }
        }
        html += '</div>';
      }

      html += '<div class="tm-post-foot">';
      if (p.views) html += '<span class="tm-views">👁 ' + tmEsc(p.views) + '</span>';
      html += '</div>';
      html += '</div>';
    }
    content.innerHTML = html;
    // Jump to the bottom (newest message), like Telegram does on load.
    requestAnimationFrame(function () {
      content.scrollTop = content.scrollHeight;
    });
  }

  // ===== add / remove =====
  window.telemirrorAdd = async function () {
    var input = document.getElementById('tmAddInput');
    var u = (input.value || '').trim();
    if (!u) return;
    try {
      var r = await fetch('/api/telemirror/channels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'add', username: u })
      });
      if (!r.ok) { tmToast(tmI18n('telemirror_invalid_user', 'Invalid username')); return; }
      input.value = '';
      await tmLoadChannels();
    } catch (e) { tmToast((e && e.message) || 'failed'); }
  };

  window.tmRemove = async function (username) {
    try {
      var r = await fetch('/api/telemirror/channels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'remove', username: username })
      });
      if (!r.ok) { tmToast(tmI18n('telemirror_remove_pinned', 'Cannot remove pinned')); return; }
      if (tmActive.toLowerCase() === username.toLowerCase()) tmActive = '';
      await tmLoadChannels();
    } catch (e) { tmToast((e && e.message) || 'failed'); }
  };

  // Allow Enter in the add input.
  document.addEventListener('DOMContentLoaded', function () {
    var inp = document.getElementById('tmAddInput');
    if (inp) inp.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); window.telemirrorAdd(); }
    });
  });
})();
