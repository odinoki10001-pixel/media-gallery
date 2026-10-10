const api = () => window.go.main.App;
const $ = (id) => document.getElementById(id);
const main = $('main');

const state = {
  view: 'all',
  albumId: null,
  smartFilter: null,
  type: '',
  query: '',
  mode: 'sections',
  items: [],
  currentIndex: -1,
  selected: new Set(),
  lastClickedHash: null,
  loadingFlat: false,
  _fetchPage: null,
};

// ---------- старт ----------

window.addEventListener('DOMContentLoaded', async () => {
  await waitForBindings();
  await refreshAlbums();
  await refreshSmart();
  await refreshSidebar();
  await showAll();

  window.runtime.EventsOn('progress', onProgress);
  api().GetProgress().then(onProgress).catch(()=>{});

  setupRubberBand();
  setupExternalDrop();
  setupModalDelegation();
});

function waitForBindings(){
  return new Promise(res => {
    const t = setInterval(() => {
      if (window.go && window.go.main && window.go.main.App) { clearInterval(t); res(); }
    }, 40);
  });
}

function onProgress(p){
  const host = $('scanStatus');
  const lines = [];
  if (p.scanning){
    lines.push('Индексирую: ' + p.processed.toLocaleString('ru') + ' файлов…');
  } else if (p.processed > 0){
    lines.push('Проиндексировано: ' + p.processed.toLocaleString('ru') + ' файлов');
  }
  if (p.thumbDone > 0) lines.push('Превью готово: ' + p.thumbDone.toLocaleString('ru'));
  if (p.depsMessage){
    const color = p.depsStatus === 'partial' ? '#ff8080' : '#ffb060';
    lines.push('<span style="color:' + color + '">⚙ ' + escapeHtml(p.depsMessage) + '</span>');
  }
  host.innerHTML = lines.join('<br>');
}

// ---------- Модалки ----------

function setupModalDelegation(){
  const safeModal = $('safeModal');
  const albumModal = $('albumModal');
  const smartModal = $('smartModal');

  safeModal.addEventListener('click', (e) => {
    if (e.target.closest('[data-safemodal-close]')) safeModal.classList.remove('open');
  });
  albumModal.addEventListener('click', (e) => {
    if (e.target.closest('[data-albummodal-close]')) albumModal.classList.remove('open');
  });
  smartModal.addEventListener('click', (e) => {
    if (e.target.closest('[data-smartmodal-close]')) smartModal.classList.remove('open');
  });
}

// ---------- Sidebar ----------

async function refreshAlbums(){
  const list = (await api().ListAlbums()) || [];
  const host = $('albumList'); host.innerHTML = '';
  for (const a of list){
    if (a.protected) continue;
    const btn = document.createElement('button');
    btn.className = 'sb-item';
    btn.dataset.albumId = a.id;
    btn.innerHTML = '<span class="ico">📁</span><span class="label"></span>' +
                    '<span class="cnt">' + a.count + '</span>' +
                    '<span class="del" title="Удалить">✕</span>';
    btn.querySelector('.label').textContent = a.name;
    btn.addEventListener('click', (e) => {
      if (e.target.classList.contains('del')) { e.stopPropagation(); deleteAlbum(a.id); return; }
      selectAlbum(a.id, a.name);
    });
    host.appendChild(btn);
  }
}

async function refreshSmart(){
  const list = (await api().ListSmartAlbums()) || [];
  const host = $('smartList'); host.innerHTML = '';
  for (const s of list){
    const btn = document.createElement('button');
    btn.className = 'sb-item';
    btn.dataset.smartId = s.id;
    btn.innerHTML = '<span class="ico">✨</span><span class="label"></span>' +
                    '<span class="del" title="Удалить">✕</span>';
    btn.querySelector('.label').textContent = s.name;
    btn.addEventListener('click', (e) => {
      if (e.target.classList.contains('del')) { e.stopPropagation(); deleteSmart(s.id); return; }
      selectSmart(s.id, s.name, s.filter);
    });
    host.appendChild(btn);
  }
}

async function refreshSidebar(){
  try {
    const info = await api().SafeInfo();
    const b = document.querySelector('.sb-item[data-view="safe"]');
    if (b) b.querySelector('.ico').textContent = (info && info.unlocked) ? '🔓' : '🔒';
    $('btnDeleteSafe').hidden = !(info && info.exists);
  } catch (e){ console.warn('[refreshSidebar] SafeInfo:', e); }
  try {
    const favN = await api().FavoritesCount('');
    $('favCnt').textContent = favN ? favN.toLocaleString('ru') : '';
  } catch (e){ console.warn('[refreshSidebar] FavoritesCount:', e); }
}

function setActive(view, id){
  document.querySelectorAll('.sb-item').forEach(b => b.classList.remove('active'));
  if (view === 'album'){
    const b = document.querySelector('.sb-item[data-album-id="'+id+'"]'); if (b) b.classList.add('active');
  } else if (view === 'smart'){
    const b = document.querySelector('.sb-item[data-smart-id="'+id+'"]'); if (b) b.classList.add('active');
  } else {
    const b = document.querySelector('.sb-item[data-view="'+view+'"]'); if (b) b.classList.add('active');
  }
}

document.querySelector('.sb-item[data-view="all"]').addEventListener('click', showAll);
document.querySelector('.sb-item[data-view="fav"]').addEventListener('click', showFavorites);
document.querySelector('.sb-item[data-view="top"]').addEventListener('click', showTopRated);
document.querySelector('.sb-item[data-view="safe"]').addEventListener('click', showSafe);

$('btnDeleteSafe').addEventListener('click', async () => {
  const info = await api().SafeInfo();
  if (!info.exists) { toast('Сейф не создан'); return; }
  if (!info.unlocked) { toast('Сначала разблокируйте сейф'); openSafeUnlock(info); return; }
  if (!confirm('Удалить сейф? Пароль будет сброшен, файлы вернутся в основную галерею.')) return;
  try {
    await api().DeleteSafe();
    toast('Сейф удалён');
    await refreshSidebar();
    await showAll();
  } catch (e){ toast('Ошибка: ' + e); }
});

$('btnAddAlbum').addEventListener('click', async () => {
  const name = prompt('Имя альбома:'); if (!name) return;
  await api().CreateAlbum(name);
  await refreshAlbums(); toast('Альбом создан');
});

$('btnAddSmart').addEventListener('click', () => $('smartModal').classList.add('open'));

$('smartCreate').addEventListener('click', async () => {
  const name = $('smartName').value.trim(); if (!name){ toast('Введите название'); return; }
  const filter = JSON.stringify({
    favorite: $('smartFav').checked,
    onlyGPS: $('smartGPS').checked,
    minRating: Number($('smartRating').value) || 0,
    type: $('smartType').value,
    q: $('smartQ').value.trim(),
  });
  await api().CreateSmartAlbum(name, filter);
  $('smartModal').classList.remove('open');
  await refreshSmart(); toast('Умный альбом создан');
});

async function deleteAlbum(id){
  if (!confirm('Удалить альбом? Файлы останутся на диске.')) return;
  await api().DeleteAlbum(id);
  await refreshAlbums();
  if (state.view === 'album' && state.albumId === id) await showAll();
}

async function deleteSmart(id){
  if (!confirm('Удалить умный альбом?')) return;
  await api().DeleteSmartAlbum(id);
  await refreshSmart();
  if (state.view === 'smart' && state.albumId === id) await showAll();
}

// ---------- Views ----------

async function showAll(){
  state.view = 'all'; state.albumId = null;
  setActive('all');
  state.mode = 'sections';
  await renderSections();
}

async function showFavorites(){
  state.view = 'fav'; setActive('fav');
  state.mode = 'flat';
  await renderFlat(() => api().Favorites(state.type, state.items.length, 200),
                   () => api().FavoritesCount(state.type));
}

async function showTopRated(){
  state.view = 'top'; setActive('top');
  state.mode = 'flat';
  await renderFlat(() => api().RatedItems(4, state.type, state.items.length, 200),
                   () => api().RatedCount(4, state.type));
}

async function selectAlbum(id, name){
  state.view = 'album'; state.albumId = id;
  setActive('album', id);
  state.mode = 'flat';
  await renderFlat(() => api().AlbumItems(id, state.items.length, 200),
                   () => api().AlbumCount(id));
}

async function selectSmart(id, name, filter){
  state.view = 'smart'; state.albumId = id; state.smartFilter = filter;
  setActive('smart', id);
  state.mode = 'flat';
  await renderFlat(() => api().SmartAlbumItems(filter, state.items.length, 200),
                   () => api().SmartAlbumCount(filter));
}

async function showSafe(){
  try {
    const info = await api().SafeInfo();
    if (!info){ toast('Не удалось получить статус сейфа'); return; }
    if (!info.exists){ openSafeCreate(); return; }
    if (!info.unlocked){ openSafeUnlock(info); return; }
    state.view = 'safe'; state.albumId = null;
    setActive('safe'); state.mode = 'flat';
    await renderFlat(() => api().SafeItems(state.items.length, 200),
                     () => api().SafeCount());
  } catch (e){
    console.error('[showSafe] error', e);
    toast('Ошибка сейфа: ' + e);
  }
}

// ---------- Секции ----------

async function renderSections(){
  clearMain();
  $('sections').hidden = false;
  state.items = [];
  state.lastClickedHash = null;

  const list = (await api().GetSections(state.type)) || [];
  const total = list.reduce((s,x) => s + x.count, 0);
  updateCount(total);

  if (!list.length){
    $('empty').hidden = false;
    $('empty').textContent = 'Пока пусто. Идёт сканирование — файлы появятся автоматически.';
    return;
  }

  const frag = document.createDocumentFragment();
  for (const sec of list){
    const el = document.createElement('section');
    el.className = 'section';
    el.dataset.from = sec.from; el.dataset.to = sec.to;
    el.dataset.count = sec.count; el.dataset.offset = '0'; el.dataset.busy = '0';
    const head = document.createElement('div');
    head.className = 'section-header';
    head.innerHTML = '<div class="section-title"></div><div class="section-count">'+sec.count+'</div>';
    head.querySelector('.section-title').textContent = sec.label;
    el.appendChild(head);
    const g = document.createElement('div'); g.className = 'grid';
    el.appendChild(g);
    frag.appendChild(el);
  }
  $('sections').appendChild(frag);

  const secs = $('sections').querySelectorAll('.section');
  secs.forEach((s,i) => { if (i < 2) loadSection(s); });
  requestAnimationFrame(checkSections);
}

async function loadSection(el){
  if (el.dataset.busy === '1') return;
  if (Number(el.dataset.offset) >= Number(el.dataset.count)) return;
  el.dataset.busy = '1';
  try {
    const from = Number(el.dataset.from), to = Number(el.dataset.to);
    const offset = Number(el.dataset.offset);
    const items = (await api().GetSectionItems(from, to, state.type, offset, 100)) || [];
    if (!items.length){ el.dataset.offset = el.dataset.count; return; }
    el.dataset.offset = String(offset + items.length);
    appendItems(el.querySelector('.grid'), items);
  } finally { el.dataset.busy = '0'; }
}

let rafPending = false;
function checkSections(){
  if (state.mode !== 'sections') return;
  if (rafPending) return;
  rafPending = true;
  requestAnimationFrame(() => {
    rafPending = false;
    const mr = main.getBoundingClientRect();
    for (const sec of $('sections').querySelectorAll('.section')){
      const r = sec.getBoundingClientRect();
      if (r.top > mr.bottom + 900) break;
      if (Number(sec.dataset.offset) < Number(sec.dataset.count)) loadSection(sec);
    }
  });
}

main.addEventListener('scroll', () => {
  if (state.mode === 'sections') checkSections();
}, { passive: true });

// ---------- Плоская сетка ----------

async function renderFlat(fetchPage, fetchCount){
  clearMain();
  $('gridFlat').hidden = false;
  state.items = [];
  state.lastClickedHash = null;
  state.loadingFlat = false;
  state._fetchPage = fetchPage;
  let total = 0;
  try { total = await fetchCount(); } catch (e){ console.error(e); }
  updateCount(total || 0);
  if (!total){
    $('empty').hidden = false;
    $('empty').textContent = 'Пусто.';
    return;
  }
  await loadFlatPage();
}

async function loadFlatPage(){
  if (state.mode !== 'flat') return;
  if (state.loadingFlat) return;
  if (typeof state._fetchPage !== 'function') return;
  state.loadingFlat = true;
  try {
    const items = await state._fetchPage();
    if (!items || !Array.isArray(items) || !items.length) return;
    appendItems($('gridFlat'), items);
  } catch (e){
    console.error('[loadFlatPage] error', e);
  } finally {
    state.loadingFlat = false;
  }
}

new IntersectionObserver((entries) => {
  if (!entries[0].isIntersecting) return;
  if (state.mode === 'flat') loadFlatPage();
  else if (state.mode === 'sections') checkSections();
}, { root: main, rootMargin: '800px 0px' }).observe($('sentinel'));

// ---------- Поиск ----------

$('q').addEventListener('input', debounce(onSearch, 250));

async function onSearch(){
  const q = $('q').value.trim();
  if (!q){ await showAll(); return; }
  state.query = q;
  state.mode = 'flat';
  state.view = 'search';
  setActive('search');
  await renderFlat(() => api().Search(state.query, state.type, 200, state.items.length),
                   () => api().TotalCount(state.query, state.type));
}

// ---------- Сегмент типа ----------

$('seg').addEventListener('click', async (e) => {
  const btn = e.target.closest('button'); if (!btn) return;
  $('seg').querySelectorAll('button').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
  state.type = btn.dataset.type;
  if (state.view === 'all') await renderSections();
  else if (state.view === 'fav') await showFavorites();
  else if (state.view === 'top') await showTopRated();
  else if (state.view === 'album') await selectAlbum(state.albumId);
  else if (state.view === 'smart') await selectSmart(state.albumId, '', state.smartFilter);
  else if (state.view === 'safe') await showSafe();
  else if (state.view === 'search') await onSearch();
});

// ---------- Карточки ----------

function appendItems(grid, items){
  if (!Array.isArray(items)) return;
  const seen = new Set(state.items.map(m => m.hash));
  const frag = document.createDocumentFragment();
  for (const m of items){
    if (!m || !m.hash) continue;
    if (seen.has(m.hash)) continue;
    seen.add(m.hash);
    const card = createCard(m);
    frag.appendChild(card);
    state.items.push(m);
  }
  grid.appendChild(frag);
  grid.querySelectorAll('.card:not(.observed)').forEach(c => {
    c.classList.add('observed');
    viewportIO.observe(c);
  });
  updateCardSelection();
}

function createCard(m){
  const el = document.createElement('div');
  el.className = 'card';
  el.dataset.hash = m.hash;
  el.dataset.ext = (m.ext || '').replace('.', '');
  el.draggable = true;

  const img = document.createElement('img');
  img.alt = ''; img.decoding = 'async'; img.loading = 'lazy';
  img.dataset.src = '/thumb/' + m.hash + '.jpg';
  img.addEventListener('load', () => img.classList.add('loaded'));
  img.addEventListener('error', () => { el.remove(); });
  el.appendChild(img);

  if (m.type === 'video'){
    const b = document.createElement('div'); b.className = 'badge'; b.textContent = '▶'; el.appendChild(b);
  }
  if (m.favorite){
    const f = document.createElement('div'); f.className = 'fav'; f.textContent = '❤'; el.appendChild(f);
  }
  if (m.rating > 0){
    const r = document.createElement('div'); r.className = 'rating-mini'; r.textContent = '★'.repeat(m.rating); el.appendChild(r);
  }
  if (m.inSafe){
    const s = document.createElement('div'); s.className = 'safe-mark'; s.textContent = '🔒'; el.appendChild(s);
  }

  const name = document.createElement('div');
  name.className = 'name'; name.textContent = m.name;
  el.appendChild(name);

  el.addEventListener('click', (e) => {
    const hash = m.hash;

    if (e.ctrlKey || e.metaKey){
      e.preventDefault();
      toggleSelect(hash);
      state.lastClickedHash = hash;
      return;
    }
    if (e.shiftKey && state.lastClickedHash){
      e.preventDefault();
      selectRangeByHash(state.lastClickedHash, hash);
      return;
    }
    const idx = state.items.findIndex(x => x.hash === hash);
    if (idx < 0) return;
    state.lastClickedHash = hash;
    openLightbox(idx);
  });

  el.addEventListener('dragstart', (e) => {
    const url = location.origin + '/file/' + m.hash;
    e.dataTransfer.setData('DownloadURL', guessMime(m.ext) + ':' + m.name + ':' + url);
    e.dataTransfer.setData('text/plain', m.path);
    e.dataTransfer.effectAllowed = 'copy';
  });

  return el;
}

function guessMime(ext){
  const e = (ext||'').toLowerCase();
  if (['.jpg','.jpeg'].includes(e)) return 'image/jpeg';
  if (e === '.png') return 'image/png';
  if (e === '.gif') return 'image/gif';
  if (e === '.webp') return 'image/webp';
  if (e === '.mp4') return 'video/mp4';
  if (e === '.mov') return 'video/quicktime';
  return 'application/octet-stream';
}

// viewportIO — подгружает превью при подходе к вьюпорту и выгружает при уходе.
// img.complete && naturalWidth > 0 — на случай, если браузер отдал картинку из кэша
// без повторного события load (иначе она бы навсегда осталась с opacity:0).
const viewportIO = new IntersectionObserver((entries) => {
  for (const e of entries){
    const img = e.target.querySelector('img'); if (!img) continue;
    if (e.isIntersecting){
      if (!img.src && img.dataset.src){
        img.src = img.dataset.src;
        if (img.complete && img.naturalWidth > 0){
          img.classList.add('loaded');
        }
      }
    } else {
      if (img.src){
        img.removeAttribute('src');
        img.classList.remove('loaded');
      }
    }
  }
}, { root: main, rootMargin: '3000px 0px' });

// ---------- Выделение ----------

function toggleSelect(hash){
  if (state.selected.has(hash)) state.selected.delete(hash);
  else state.selected.add(hash);
  updateCardSelection();
  updateSelBar();
}

function selectRangeByHash(fromHash, toHash){
  const a = document.querySelector('.card[data-hash="' + cssEsc(fromHash) + '"]');
  const b = document.querySelector('.card[data-hash="' + cssEsc(toHash) + '"]');
  if (!a || !b){ toggleSelect(toHash); return; }

  const ga = a.parentElement;
  const gb = b.parentElement;

  if (ga !== gb){
    state.selected.add(toHash);
    state.lastClickedHash = toHash;
    updateCardSelection();
    updateSelBar();
    return;
  }

  const cards = Array.from(ga.children);
  const ia = cards.indexOf(a);
  const ib = cards.indexOf(b);
  const [lo, hi] = ia < ib ? [ia, ib] : [ib, ia];
  for (let i = lo; i <= hi; i++){
    const h = cards[i] && cards[i].dataset && cards[i].dataset.hash;
    if (h) state.selected.add(h);
  }
  state.lastClickedHash = toHash;
  updateCardSelection();
  updateSelBar();
}

function cssEsc(s){
  if (window.CSS && CSS.escape) return CSS.escape(s);
  return String(s).replace(/["\\]/g, '\\$&');
}

function updateCardSelection(){
  document.querySelectorAll('.card').forEach(c => {
    c.classList.toggle('selected', state.selected.has(c.dataset.hash));
  });
}

function updateSelBar(){
  const bar = $('selBar');
  if (state.selected.size === 0){ bar.hidden = true; return; }
  bar.hidden = false;
  $('selCount').textContent = 'Выбрано: ' + state.selected.size;
}

function clearSelection(){
  state.selected.clear();
  state.lastClickedHash = null;
  updateCardSelection(); updateSelBar();
}

function dropHashesFromSelection(hashes){
  const set = new Set(hashes);
  for (const h of hashes) state.selected.delete(h);
  if (state.lastClickedHash && set.has(state.lastClickedHash)) state.lastClickedHash = null;
  updateCardSelection();
  updateSelBar();
}

$('selClear').addEventListener('click', clearSelection);

$('selFav').addEventListener('click', async () => {
  for (const h of state.selected){
    const m = state.items.find(x => x.hash === h);
    if (m && !m.favorite){ await api().ToggleFavorite(h); m.favorite = true; }
  }
  toast('Добавлено в избранное');
  refreshSidebar();
  updateCardSelection();
});

$('selSafe').addEventListener('click', async () => {
  const info = await api().SafeInfo();
  if (!info.exists){ toast('Сначала создайте сейф'); openSafeCreate(); return; }
  if (!info.unlocked){ toast('Разблокируйте сейф'); openSafeUnlock(info); return; }
  await api().AddManyToSafe([...state.selected]);
  toast('Скрыто в сейф: ' + state.selected.size);
  clearSelection();
  if (state.view === 'all') await renderSections();
  else if (state.view === 'fav') await showFavorites();
});

$('selAlbum').addEventListener('click', async () => {
  const albums = ((await api().ListAlbums()) || []).filter(a => !a.protected);
  const host = $('albumPickList'); host.innerHTML = '';
  if (!albums.length){
    host.innerHTML = '<p style="opacity:.6;font-size:13px;padding:8px 4px">Нет альбомов.</p>';
  } else {
    for (const a of albums){
      const b = document.createElement('button');
      b.innerHTML = '<span></span><span class="cnt">'+a.count+'</span>';
      b.querySelector('span').textContent = a.name;
      b.addEventListener('click', async () => {
        await api().AddManyToAlbum(a.id, [...state.selected]);
        toast('Добавлено в «'+a.name+'»: ' + state.selected.size);
        $('albumModal').classList.remove('open');
        await refreshAlbums();
        clearSelection();
      });
      host.appendChild(b);
    }
  }
  $('albumModal').classList.add('open');
});

$('selCopyFiles').addEventListener('click', async () => {
  try {
    await api().CopyFilesToClipboard([...state.selected]);
    toast('Файлы в буфере — вставьте в проводник или чат');
  } catch (e){ toast('Не удалось: ' + e); }
});

$('selCopyPaths').addEventListener('click', async () => {
  try {
    await api().CopyPathsToClipboard([...state.selected]);
    toast('Пути скопированы');
  } catch (e){ toast('Не удалось: ' + e); }
});

$('selDelete').addEventListener('click', async () => {
  const hashes = [...state.selected];
  if (!hashes.length) return;
  if (!confirm('Удалить ' + hashes.length + ' файл(ов)? Они попадут в корзину.')) return;
  try {
    await api().DeleteFiles(hashes);
    dropHashesFromSelection(hashes);
    removeHashesFromUI(hashes);
    toast('Удалено: ' + hashes.length);
  } catch (e){
    toast('Ошибка: ' + e);
  }
});

function removeHashesFromUI(hashes){
  const set = new Set(hashes);
  state.items = state.items.filter(m => !set.has(m.hash));
  document.querySelectorAll('.card').forEach(c => {
    if (set.has(c.dataset.hash)) c.remove();
  });
  if (state.currentIndex >= 0){
    const cur = state.items[state.currentIndex];
    if (!cur) closeLightbox();
  }
}

// ---------- Rubber band (переписан) ----------
// Все координаты — в системе main (включая scroll). #rubber — ребёнок main,
// absolute. Рамка не съезжает при скролле и не цепляет лишние карточки.

function setupRubberBand(){
  const rb = document.createElement('div');
  rb.id = 'rubber';
  main.appendChild(rb); // <-- именно main, не parentElement

  let startX = null, startY = null;
  let baseSelection = null;

  main.addEventListener('mousedown', (e) => {
    if (e.button !== 0) return;
    if (e.target.closest('.card')) return;
    if (e.target.closest('.sel-bar')) return;
    if (e.target.closest('.section-header')) return;
    if (e.target.closest('button')) return;

    const rect = main.getBoundingClientRect();
    // координаты относительно контента main (с учётом текущего скролла)
    startX = e.clientX - rect.left + main.scrollLeft;
    startY = e.clientY - rect.top + main.scrollTop;

    if (!e.ctrlKey && !e.metaKey) clearSelection();
    baseSelection = new Set(state.selected);

    rb.style.display = 'block';
    rb.style.left = startX + 'px';
    rb.style.top = startY + 'px';
    rb.style.width = '0px';
    rb.style.height = '0px';
    e.preventDefault();
  });

  main.addEventListener('mousemove', (e) => {
    if (startX == null) return;
    const rect = main.getBoundingClientRect();
    const curX = e.clientX - rect.left + main.scrollLeft;
    const curY = e.clientY - rect.top + main.scrollTop;

    const x1 = Math.min(startX, curX), y1 = Math.min(startY, curY);
    const x2 = Math.max(startX, curX), y2 = Math.max(startY, curY);

    rb.style.left = x1 + 'px';
    rb.style.top = y1 + 'px';
    rb.style.width = (x2 - x1) + 'px';
    rb.style.height = (y2 - y1) + 'px';

    updateRubberSelection(x1, y1, x2, y2, baseSelection);
  });

  window.addEventListener('mouseup', () => {
    if (startX == null) return;
    startX = null; startY = null;
    baseSelection = null;
    rb.style.display = 'none';
    updateSelBar();
  });
}

function updateRubberSelection(x1, y1, x2, y2, baseSelection){
  state.selected = new Set(baseSelection);

  const rect = main.getBoundingClientRect();
  const scrollTop = main.scrollTop;
  const scrollLeft = main.scrollLeft;

  document.querySelectorAll('.card').forEach(c => {
    const hash = c.dataset.hash;
    if (!hash) return;
    const r = c.getBoundingClientRect();
    // координаты карточки в системе main (как startX/startY)
    const cx1 = r.left - rect.left + scrollLeft;
    const cy1 = r.top - rect.top + scrollTop;
    const cx2 = cx1 + r.width, cy2 = cy1 + r.height;

    const hit = !(cx2 < x1 || cx1 > x2 || cy2 < y1 || cy1 > y2);
    if (hit) state.selected.add(hash);
  });
  updateCardSelection();
}

function setupExternalDrop(){
  window.addEventListener('dragover', (e) => {
    if (e.dataTransfer.types.includes('Files')) e.preventDefault();
  });
  window.addEventListener('drop', async (e) => {
    if (!e.dataTransfer.files || !e.dataTransfer.files.length) return;
    e.preventDefault();
    toast('Найдено файлов: ' + e.dataTransfer.files.length);
  });
}

// ---------- Лайтбокс ----------

const lb = $('lb');
const lbStage = $('lbStage');
const lbViewport = $('lbViewport');
let zoom = 1, panX = 0, panY = 0, dragging = false, dragStart = null;

function openLightbox(index){
  if (index < 0 || index >= state.items.length) return;
  state.currentIndex = index;
  const m = state.items[index];

  lbStage.innerHTML = '';
  zoom = 1; panX = 0; panY = 0; applyTransform(false);

  const url = '/file/' + m.hash;
  if (m.type === 'video'){
    const v = document.createElement('video');
    v.src = url; v.controls = true; v.autoplay = true; v.playsInline = true;
    lbStage.appendChild(v);
  } else {
    const img = document.createElement('img');
    img.src = url; img.alt = '';
    img.addEventListener('error', () => { closeLightbox(); });
    lbStage.appendChild(img);
  }
  $('lbName').textContent = m.name;
  $('lbPath').textContent = m.path;
  updateFavBtn(m);
  updateRating(m);
  updateSafeBtn(m);
  lb.classList.add('open');
  updateNav();
}

function closeLightbox(){
  lb.classList.remove('open');
  lbStage.innerHTML = '';
  state.currentIndex = -1;
}

function updateFavBtn(m){ $('lbFav').classList.toggle('on', m.favorite); $('lbFav').textContent = m.favorite ? '❤' : '♡'; }
function updateRating(m){
  const host = $('lbRating');
  host.querySelectorAll('span').forEach(s => s.classList.toggle('on', Number(s.dataset.r) <= m.rating));
}
function updateSafeBtn(m){ $('lbSafe').textContent = m.inSafe ? '🔓' : '🔒'; }

function nav(delta){
  if (state.currentIndex < 0) return;
  const n = state.currentIndex + delta;
  if (n < 0 || n >= state.items.length) return;
  openLightbox(n);
}

function updateNav(){
  $('lbPrev').style.opacity = state.currentIndex > 0 ? '1' : '.3';
  $('lbNext').style.opacity = state.currentIndex < state.items.length - 1 ? '1' : '.3';
}

function applyTransform(animated = true){
  lbStage.classList.toggle('animated', animated);
  lbStage.style.transform = 'translate('+panX+'px,'+panY+'px) scale('+zoom+')';
  $('lbZoomReset').textContent = Math.round(zoom*100) + '%';
  if (animated) setTimeout(() => lbStage.classList.remove('animated'), 220);
}

function setZoom(z, cx, cy){
  z = Math.max(0.25, Math.min(12, z));
  if (cx != null){
    const rect = lbViewport.getBoundingClientRect();
    const dx = cx - (rect.left + rect.width/2);
    const dy = cy - (rect.top + rect.height/2);
    const k = z / zoom;
    panX = panX * k + dx * (1 - k);
    panY = panY * k + dy * (1 - k);
  }
  zoom = z;
  applyTransform(false);
}

$('lbPrev').addEventListener('click', e => { e.stopPropagation(); nav(-1); });
$('lbNext').addEventListener('click', e => { e.stopPropagation(); nav(1); });
lb.querySelectorAll('[data-close]').forEach(el => el.addEventListener('click', closeLightbox));

$('lbZoomIn').addEventListener('click', () => setZoom(zoom * 1.25));
$('lbZoomOut').addEventListener('click', () => setZoom(zoom / 1.25));
$('lbZoomReset').addEventListener('click', () => { zoom = 1; panX = 0; panY = 0; applyTransform(true); });

lbViewport.addEventListener('wheel', (e) => {
  if (!lb.classList.contains('open')) return;
  e.preventDefault();
  if (e.ctrlKey || e.metaKey || zoom > 1.05){
    const factor = Math.exp(-e.deltaY * 0.0015);
    setZoom(zoom * factor, e.clientX, e.clientY);
  } else {
    if (e.deltaY > 20) nav(1);
    else if (e.deltaY < -20) nav(-1);
  }
}, { passive: false });

// Drag. Пропускаем клики по video (чтобы работали controls), по кнопкам и по img лайтбокса.
lbViewport.addEventListener('mousedown', (e) => {
  if (e.button !== 0) return;
  if (e.target.closest('button')) return;
  if (e.target.tagName === 'VIDEO') return; // не начинаем drag на видео — иначе ломаются controls
  dragging = true;
  dragStart = { x: e.clientX - panX, y: e.clientY - panY };
});
window.addEventListener('mousemove', (e) => {
  if (!dragging) return;
  panX = e.clientX - dragStart.x;
  panY = e.clientY - dragStart.y;
  applyTransform(false);
});
window.addEventListener('mouseup', () => { dragging = false; });
lbViewport.addEventListener('dblclick', (e) => {
  if (e.target.tagName === 'VIDEO') return;
  e.preventDefault();
  if (zoom !== 1){ zoom = 1; panX = 0; panY = 0; applyTransform(true); }
  else setZoom(2.5, e.clientX, e.clientY);
});

$('lbReveal').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  try { await api().RevealInFileManager(state.items[state.currentIndex].hash); }
  catch (e){ toast('Не удалось: ' + e); }
});
$('lbOpen').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  try { await api().OpenFile(state.items[state.currentIndex].hash); }
  catch (e){ toast('Не удалось: ' + e); }
});
$('lbCopy').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  await api().CopyPathsToClipboard([state.items[state.currentIndex].hash]);
  toast('Путь скопирован');
});
$('lbFav').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  const m = state.items[state.currentIndex];
  m.favorite = await api().ToggleFavorite(m.hash);
  updateFavBtn(m);
  updateCardSelection();
  refreshSidebar();
});
$('lbRating').addEventListener('click', async (e) => {
  if (!e.target.dataset.r) return;
  if (state.currentIndex < 0) return;
  const m = state.items[state.currentIndex];
  const r = Number(e.target.dataset.r);
  const newR = m.rating === r ? 0 : r;
  await api().SetRating(m.hash, newR);
  m.rating = newR;
  updateRating(m);
});
$('lbSafe').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  const info = await api().SafeInfo();
  if (!info.exists){ toast('Сначала создайте сейф'); openSafeCreate(); return; }
  if (!info.unlocked){ toast('Разблокируйте сейф'); openSafeUnlock(info); return; }
  const m = state.items[state.currentIndex];
  try {
    if (m.inSafe){ await api().RemoveFromSafe(m.hash); m.inSafe = false; toast('Убрано из сейфа'); }
    else { await api().AddToSafe(m.hash); m.inSafe = true; toast('Добавлено в сейф'); }
    updateSafeBtn(m);
  } catch (e){ toast('Ошибка: ' + e); }
});
$('lbDelete').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  const m = state.items[state.currentIndex];
  if (!m) return;
  if (!confirm('Удалить «' + m.name + '»? Файл попадёт в корзину.')) return;
  try {
    await api().DeleteFiles([m.hash]);
    const i = state.currentIndex;
    dropHashesFromSelection([m.hash]);
    removeHashesFromUI([m.hash]);
    if (state.items.length === 0){ closeLightbox(); return; }
    const next = Math.min(i, state.items.length - 1);
    openLightbox(next);
    toast('Удалено');
  } catch (e){
    toast('Ошибка: ' + e);
  }
});
$('lbAlbum').addEventListener('click', async () => {
  if (state.currentIndex < 0) return;
  const albums = ((await api().ListAlbums()) || []).filter(a => !a.protected);
  const host = $('albumPickList'); host.innerHTML = '';
  if (!albums.length){
    host.innerHTML = '<p style="opacity:.6;font-size:13px;padding:8px 4px">Нет альбомов.</p>';
  } else {
    for (const a of albums){
      const b = document.createElement('button');
      b.innerHTML = '<span></span><span class="cnt">'+a.count+'</span>';
      b.querySelector('span').textContent = a.name;
      b.addEventListener('click', async () => {
        await api().AddToAlbum(a.id, state.items[state.currentIndex].hash);
        toast('Добавлено в «'+a.name+'»');
        $('albumModal').classList.remove('open');
        await refreshAlbums();
      });
      host.appendChild(b);
    }
  }
  $('albumModal').classList.add('open');
});

// ---------- Сейф: модалка ----------

const safeModal = $('safeModal');
const safeCard = $('safeCard');

function openSafeCreate(){
  try {
    safeCard.innerHTML = `
      <h3>Создать сейф</h3>
      <p>Файлы в сейфе будут скрыты из основной галереи. Доступ — только по паролю.</p>
      <label style="display:block;font-size:12px;color:var(--text-dim);margin-bottom:4px">Тип пароля</label>
      <select id="safeType">
        <option value="pin">PIN (цифры)</option>
        <option value="text">Текстовый</option>
        <option value="pattern">Графический (паттерн)</option>
      </select>
      <form onsubmit="return false" style="margin:0">
        <div id="safePwdHost"></div>
        <input id="safeHint" placeholder="Подсказка (необязательно)" maxlength="80">
      </form>
      <div class="modal-actions">
        <button data-safemodal-close>Отмена</button>
        <button class="primary" id="safeCreateBtn">Создать</button>
      </div>`;
    const sel = $('safeType'); const host = $('safePwdHost');
    const renderPwd = () => {
      if (sel.value === 'pattern'){
        host.innerHTML = `
          <p>Нарисуйте паттерн на сетке 3×3 (минимум 4 точки):</p>
          <div class="pattern-wrap" id="patWrap">
            <svg class="pattern-svg" id="patSvg"></svg>
            <div class="pattern-grid" id="patGrid"></div>
          </div>
          <div class="pattern-status" id="patStatus">Точки: 0</div>`;
        setupPattern();
      } else if (sel.value === 'pin'){
        host.innerHTML = '<input id="safePwd" type="password" inputmode="numeric" placeholder="PIN (мин. 4 цифры)" maxlength="20">';
      } else {
        host.innerHTML = '<input id="safePwd" type="password" placeholder="Пароль (мин. 4 символа)" maxlength="80">';
      }
    };
    sel.addEventListener('change', renderPwd); renderPwd();

    $('safeCreateBtn').onclick = async () => {
      const btn = $('safeCreateBtn');
      if (btn.disabled) return;
      const pwd = sel.value === 'pattern' ? getPatternValue() : ($('safePwd').value || '').trim();
      if (sel.value === 'pin' && !/^\d{4,}$/.test(pwd)){ toast('PIN: минимум 4 цифры'); return; }
      if (sel.value === 'text' && pwd.length < 4){ toast('Пароль: минимум 4 символа'); return; }
      if (sel.value === 'pattern' && pwd.split('-').length < 4){ toast('Минимум 4 точки'); return; }
      btn.disabled = true;
      btn.textContent = 'Создаю…';
      try {
        await api().CreateSafe(pwd, sel.value, $('safeHint').value || '');
        safeModal.classList.remove('open');
        await refreshSidebar();
        await showSafe();
        toast('Сейф создан — добавьте файлы через 🔒');
      } catch (e){
        console.error('[safe create] error', e);
        toast('Ошибка: ' + e);
        btn.disabled = false;
        btn.textContent = 'Создать';
      }
    };
    safeModal.classList.add('open');
  } catch (e){
    console.error('[openSafeCreate] error', e);
    toast('Ошибка модалки: ' + e);
  }
}

function openSafeUnlock(info){
  try {
    const usePattern = info.type === 'pattern';
    safeCard.innerHTML = `
      <h3>Разблокировать сейф</h3>
      ${info.hint ? '<p>Подсказка: '+escapeHtml(info.hint)+'</p>' : ''}
      ${usePattern
        ? `<div class="pattern-wrap" id="patWrap">
             <svg class="pattern-svg" id="patSvg"></svg>
             <div class="pattern-grid" id="patGrid"></div>
           </div>
           <div class="pattern-status" id="patStatus">Нарисуйте паттерн</div>`
        : `<form onsubmit="return false" style="margin:0">
             <input id="safePwd" type="password" ${info.type==='pin'?'inputmode="numeric"':''} placeholder="Пароль">
           </form>`}
      <div class="modal-actions">
        <button data-safemodal-close>Отмена</button>
        <button class="primary" id="safeUnlockBtn">Открыть</button>
      </div>`;
    if (usePattern) setupPattern();
    $('safeUnlockBtn').onclick = async () => {
      const pwd = usePattern ? getPatternValue() : ($('safePwd').value || '');
      const ok = await api().UnlockSafe(pwd);
      if (!ok){ toast('Неверный пароль'); resetPattern(); return; }
      safeModal.classList.remove('open');
      await refreshSidebar();
      await showSafe();
    };
    safeModal.classList.add('open');
  } catch (e){
    console.error('[openSafeUnlock] error', e);
    toast('Ошибка модалки: ' + e);
  }
}

// ---------- Паттерн 3×3 ----------

let patPoints = [];
let patDragging = false;
let patLastPos = null;

function setupPattern(){
  const grid = $('patGrid');
  const svg = $('patSvg');
  const wrap = $('patWrap');
  if (!grid || !svg || !wrap) return;

  patPoints = [];
  patDragging = false;
  patLastPos = null;

  grid.innerHTML = '';
  for (let i = 0; i < 9; i++){
    const d = document.createElement('div');
    d.className = 'pattern-dot';
    d.dataset.i = i;
    grid.appendChild(d);
  }

  function centerOf(idx){
    const d = grid.querySelector('.pattern-dot[data-i="'+idx+'"]');
    if (!d) return null;
    const r = d.getBoundingClientRect();
    const wr = wrap.getBoundingClientRect();
    return { x: r.left + r.width/2 - wr.left, y: r.top + r.height/2 - wr.top };
  }

  function paint(){
    grid.querySelectorAll('.pattern-dot').forEach(d => {
      const idx = Number(d.dataset.i);
      d.classList.toggle('on', patPoints.includes(idx));
    });
    drawLines();
    const st = $('patStatus');
    if (st) st.textContent = patPoints.length ? ('Точек: ' + patPoints.length) : 'Нарисуйте паттерн';
  }

  function drawLines(){
    svg.innerHTML = '';
    if (patPoints.length < 2) return;
    const defs = document.createElementNS('http://www.w3.org/2000/svg','defs');
    defs.innerHTML = '<linearGradient id="patGrad" x1="0" y1="0" x2="1" y2="1">' +
      '<stop offset="0" stop-color="#7a6cff"/>' +
      '<stop offset="1" stop-color="#00b4ff"/></linearGradient>';
    svg.appendChild(defs);
    for (let i = 0; i < patPoints.length - 1; i++){
      const a = centerOf(patPoints[i]);
      const b = centerOf(patPoints[i+1]);
      if (!a || !b) continue;
      const l = document.createElementNS('http://www.w3.org/2000/svg','line');
      l.setAttribute('x1', a.x); l.setAttribute('y1', a.y);
      l.setAttribute('x2', b.x); l.setAttribute('y2', b.y);
      l.setAttribute('stroke', 'url(#patGrad)');
      l.setAttribute('stroke-width', '4');
      l.setAttribute('stroke-linecap', 'round');
      svg.appendChild(l);
    }
  }

  function hit(px, py){
    for (let i = 0; i < 9; i++){
      const c = centerOf(i);
      if (!c) continue;
      if (Math.hypot(c.x - px, c.y - py) < 28){
        if (!patPoints.includes(i)){
          patPoints.push(i);
          paint();
        }
      }
    }
  }

  function moveAt(clientX, clientY){
    const wr = wrap.getBoundingClientRect();
    const x = clientX - wr.left;
    const y = clientY - wr.top;
    if (patLastPos){
      const dist = Math.hypot(x - patLastPos.x, y - patLastPos.y);
      const steps = Math.max(1, Math.ceil(dist / 4));
      for (let s = 1; s <= steps; s++){
        const px = patLastPos.x + (x - patLastPos.x) * (s / steps);
        const py = patLastPos.y + (y - patLastPos.y) * (s / steps);
        hit(px, py);
      }
    } else {
      hit(x, y);
    }
    patLastPos = { x, y };
  }

  wrap._getPattern = () => patPoints.join('-');
  wrap._resetPattern = () => { patPoints = []; patLastPos = null; paint(); };

  wrap.addEventListener('mousedown', (e) => {
    e.preventDefault();
    patDragging = true;
    patPoints = [];
    patLastPos = null;
    paint();
    const wr = wrap.getBoundingClientRect();
    patLastPos = { x: e.clientX - wr.left, y: e.clientY - wr.top };
    hit(patLastPos.x, patLastPos.y);
  });
  wrap.addEventListener('mousemove', (e) => {
    if (!patDragging) return;
    e.preventDefault();
    moveAt(e.clientX, e.clientY);
  });
  wrap.addEventListener('mouseleave', () => { patDragging = false; patLastPos = null; });
  window.addEventListener('mouseup', () => { patDragging = false; patLastPos = null; });

  wrap.addEventListener('touchstart', (e) => {
    e.preventDefault();
    patDragging = true;
    patPoints = [];
    patLastPos = null;
    paint();
    const t = e.touches[0];
    const wr = wrap.getBoundingClientRect();
    patLastPos = { x: t.clientX - wr.left, y: t.clientY - wr.top };
    hit(patLastPos.x, patLastPos.y);
  }, { passive: false });
  wrap.addEventListener('touchmove', (e) => {
    if (!patDragging) return;
    e.preventDefault();
    const t = e.touches[0];
    moveAt(t.clientX, t.clientY);
  }, { passive: false });
  wrap.addEventListener('touchend', () => { patDragging = false; patLastPos = null; });

  paint();
}

function getPatternValue(){
  const w = $('patWrap');
  return (w && w._getPattern) ? w._getPattern() : '';
}
function resetPattern(){
  const w = $('patWrap');
  if (w && w._resetPattern) w._resetPattern();
}

// ---------- Горячие клавиши ----------

document.addEventListener('keydown', (e) => {
  if ($('smartModal').classList.contains('open')) { if (e.key === 'Escape') $('smartModal').classList.remove('open'); return; }
  if ($('albumModal').classList.contains('open')) { if (e.key === 'Escape') $('albumModal').classList.remove('open'); return; }
  if (safeModal.classList.contains('open')) { if (e.key === 'Escape') safeModal.classList.remove('open'); return; }

  if (lb.classList.contains('open')){
    switch (e.key){
      case 'Escape': e.preventDefault(); closeLightbox(); return;
      case 'ArrowLeft': e.preventDefault(); nav(-1); return;
      case 'ArrowRight': e.preventDefault(); nav(1); return;
      case 'PageUp': e.preventDefault(); nav(-10); return;
      case 'PageDown': e.preventDefault(); nav(10); return;
      case 'Home': e.preventDefault(); openLightbox(0); return;
      case 'End': e.preventDefault(); openLightbox(state.items.length-1); return;
      case '+': case '=': e.preventDefault(); setZoom(zoom * 1.25); return;
      case '-': case '_': e.preventDefault(); setZoom(zoom / 1.25); return;
      case '0': e.preventDefault(); zoom=1; panX=0; panY=0; applyTransform(true); return;
      case 'Enter': if (state.currentIndex >= 0) api().OpenFile(state.items[state.currentIndex].hash).catch(()=>{}); return;
      case 'f': case 'F': $('lbFav').click(); return;
      case 'Delete': e.preventDefault(); $('lbDelete').click(); return;
      case ' ': {
        const v = lbStage.querySelector('video');
        if (v){ e.preventDefault(); v.paused ? v.play() : v.pause(); }
        return;
      }
    }
    return;
  }

  if (e.key === 'a' && (e.ctrlKey || e.metaKey)){
    e.preventDefault();
    state.selected = new Set(state.items.map(m => m.hash));
    updateCardSelection(); updateSelBar();
    return;
  }
  if (e.key === 'Delete' && state.selected.size > 0){ e.preventDefault(); $('selDelete').click(); return; }
  if (e.key === 'Escape' && state.selected.size > 0){ e.preventDefault(); clearSelection(); return; }
});

// ---------- Утилиты ----------

function clearMain(){
  $('sections').innerHTML = '';
  $('gridFlat').innerHTML = '';
  $('sections').hidden = true;
  $('gridFlat').hidden = true;
  $('empty').hidden = true;
}

function updateCount(n){
  $('count').textContent = n ? n.toLocaleString('ru') + ' файлов' : '';
}
function escapeHtml(s){
  return String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
}
function debounce(fn, ms){ let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; }
let toastT;
function toast(msg){
  const el = $('toast'); el.textContent = msg; el.classList.add('show');
  clearTimeout(toastT); toastT = setTimeout(() => el.classList.remove('show'), 2200);
}
// ---------- Проверка обновлений ----------

(function initUpdates(){
  const bar = document.getElementById('updateBar');
  const verEl = document.getElementById('updateVersion');
  const curEl = document.getElementById('updateCurrent');
  const btnOpen = document.getElementById('updateOpen');
  const btnSkip = document.getElementById('updateSkip');
  const versionLine = document.getElementById('versionLine');

  let currentInfo = null;

  function show(info){
    if (!info || !info.available) return;
    currentInfo = info;
    verEl.textContent = info.version || '';
    curEl.textContent = 'У вас: ' + (info.current || 'dev');
    bar.hidden = false;
  }

  function hide(){
    bar.hidden = true;
  }

  btnSkip.addEventListener('click', hide);
  btnOpen.addEventListener('click', async () => {
    if (!currentInfo) return;
    try {
      await window.go.main.App.OpenURL(currentInfo.url);
      hide();
    } catch (e){
      console.error('open url:', e);
    }
  });

  // событие из Go
  window.runtime.EventsOn('update-available', (info) => {
    console.log('[updates] available:', info);
    show(info);
  });

  // подстраховка: если событие пришло до подписки — запросим состояние
  window.addEventListener('DOMContentLoaded', async () => {
    try {
      if (!window.go || !window.go.main) return;
      const v = await window.go.main.App.GetVersion();
      if (versionLine && v) versionLine.textContent = 'версия ' + v;

      const info = await window.go.main.App.GetUpdateInfo();
      if (info && info.available) show(info);
    } catch (e){
      // тихо игнорируем — приложение может работать без обновлений
    }
  });
})();