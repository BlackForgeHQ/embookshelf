// embookshelf EPUB reader.
//
// Wires epub.js to the /app/read/:id shell: loads the book via the server's
// file endpoint, restores the last-read CFI, renders in paginated mode, and
// reports progress back on each relocation (debounced) plus a final flush
// via sendBeacon on page hide. The server stores CFI + percent per user.

(function () {
  if (typeof window.ePub !== 'function') return;

  const body = document.body;
  const bookID = body.dataset.bookId;
  if (!bookID) return;

  const resumeCFI = body.dataset.resumeCfi || '';
  const fileURL = '/app/read/' + encodeURIComponent(bookID) + '/file';
  const saveURL = '/app/book/' + encodeURIComponent(bookID) + '/progress';

  const book = ePub(fileURL);
  const rendition = book.renderTo('reader-area', {
    width: '100%',
    height: '100%',
    flow: 'paginated',
    spread: 'auto',
    allowScriptedContent: false,
    manager: 'default',
  });

  // Persist font-size choice across sessions.
  const storedFontSize = localStorage.getItem('embookshelf.reader.fontSize') || '100%';
  rendition.themes.fontSize(storedFontSize);

  // Display: resume from CFI if we have one; otherwise open at the start.
  rendition.display(resumeCFI || undefined).catch(function () {
    // If the stored CFI became stale (book was replaced), fall back to start.
    rendition.display();
  });

  // Finer-grained percentage requires pre-generated locations. Do it in the
  // background so the first page is usable immediately.
  book.ready.then(function () {
    return book.locations.generate(1600);
  }).catch(function () { /* locations are a nice-to-have */ });

  // --- Progress tracking -------------------------------------------------

  let lastState = { percent: readIntAttr('progress'), cfi: resumeCFI };
  let saveTimer = null;

  rendition.on('relocated', function (location) {
    const raw = location && location.start ? location.start.percentage : 0;
    const pct = Math.max(0, Math.min(100, Math.round((raw || 0) * 100)));
    lastState = {
      percent: pct,
      cfi: (location && location.start && location.start.cfi) || '',
    };
    updateHud(pct);
    scheduleSave();
  });

  function scheduleSave() {
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(sendState, 2500);
  }

  function sendState() {
    if (!lastState.cfi) return;
    fetch(saveURL, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(lastState),
    }).catch(function () { /* retry on next relocation */ });
  }

  // Flush on hide/close. pagehide is more reliable than beforeunload on mobile.
  window.addEventListener('pagehide', function () {
    if (!lastState.cfi || !navigator.sendBeacon) return;
    navigator.sendBeacon(
      saveURL,
      new Blob([JSON.stringify(lastState)], { type: 'application/json' }),
    );
  });

  // --- Navigation --------------------------------------------------------

  document.getElementById('reader-prev').addEventListener('click', function () { rendition.prev(); });
  document.getElementById('reader-next').addEventListener('click', function () { rendition.next(); });

  document.addEventListener('keydown', function (e) {
    if (e.target && ('value' in e.target)) return; // skip when typing in inputs
    if (e.key === 'ArrowLeft' || e.key === 'k') { rendition.prev(); }
    else if (e.key === 'ArrowRight' || e.key === 'j' || e.key === ' ') { rendition.next(); e.preventDefault(); }
    else if (e.key === 'Escape') { window.location.href = '/app/book/' + encodeURIComponent(bookID); }
  });

  // --- Typography buttons ------------------------------------------------

  document.querySelectorAll('[data-font-size]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      const size = btn.dataset.fontSize;
      rendition.themes.fontSize(size);
      localStorage.setItem('embookshelf.reader.fontSize', size);
    });
  });

  // --- Helpers -----------------------------------------------------------

  function updateHud(pct) {
    const label = document.getElementById('reader-percent');
    const bar = document.getElementById('reader-progress-bar');
    if (label) label.textContent = pct + '%';
    if (bar) bar.style.width = pct + '%';
  }

  function readIntAttr(name) {
    const v = parseInt(body.dataset[name], 10);
    return Number.isFinite(v) ? v : 0;
  }
})();
