// embookshelf PDF reader.
//
// Uses Mozilla's PDF.js to render one <canvas> per page inside the reader
// shell. Pages render on demand (IntersectionObserver) so opening a 500-page
// book is instant; we only decode pages the user is near. Progress is saved
// as percent + "page:<n>" token, reusing the user_book_progress.resume_cfi
// column shared with the EPUB reader (the formats are unambiguous: EPUB
// stores `epubcfi(...)` strings).

(function () {
  if (typeof window.pdfjsLib === 'undefined') return;

  const body = document.body;
  const bookID = body.dataset.bookId;
  if (!bookID) return;

  pdfjsLib.GlobalWorkerOptions.workerSrc = '/static/pdfjs/pdf.worker.min.js';

  const area = document.getElementById('reader-area');
  const percentLabel = document.getElementById('reader-percent');
  const bar = document.getElementById('reader-progress-bar');

  const fileURL = '/app/read/' + encodeURIComponent(bookID) + '/file';
  const saveURL = '/app/book/' + encodeURIComponent(bookID) + '/progress';
  const resumeToken = body.dataset.resumeCfi || '';

  const scale = 1.5;
  let numPages = 0;
  let currentPage = 1;
  let saveTimer = null;
  const pageEls = []; // index 0 → page 1

  init().catch(function (err) {
    console.error('PDF load failed', err);
    area.innerHTML = '<div class="reader-empty"><div class="t-h2 italic">Couldn\'t open PDF</div><p class="t-small mt-2">' + escapeHTML(String(err && err.message || err)) + '</p></div>';
  });

  async function init() {
    const loadingTask = pdfjsLib.getDocument({ url: fileURL, withCredentials: true });
    const pdf = await loadingTask.promise;
    numPages = pdf.numPages;

    // Pre-create one placeholder div per page; canvases get appended on demand.
    for (let i = 1; i <= numPages; i++) {
      const wrap = document.createElement('div');
      wrap.className = 'pdf-page';
      wrap.dataset.pageNum = String(i);
      area.appendChild(wrap);
      pageEls.push(wrap);
    }

    // Lazy render — only decode pages that are scrolling near the viewport.
    const renderObserver = new IntersectionObserver(async function (entries) {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const wrap = entry.target;
        if (wrap.dataset.rendered) continue;
        wrap.dataset.rendered = '1';
        const pageNum = parseInt(wrap.dataset.pageNum, 10);
        try {
          const page = await pdf.getPage(pageNum);
          const viewport = page.getViewport({ scale });
          const canvas = document.createElement('canvas');
          canvas.width = viewport.width;
          canvas.height = viewport.height;
          wrap.appendChild(canvas);
          await page.render({ canvasContext: canvas.getContext('2d'), viewport }).promise;
        } catch (err) {
          wrap.textContent = 'Failed to render page ' + pageNum;
        }
      }
    }, { root: area, rootMargin: '800px' });

    // Current-page tracking — the most-visible page becomes "current".
    const currentObserver = new IntersectionObserver(function (entries) {
      let best = null;
      let bestRatio = 0;
      entries.forEach(function (e) {
        if (e.intersectionRatio > bestRatio) {
          bestRatio = e.intersectionRatio;
          best = e.target;
        }
      });
      if (best) {
        const n = parseInt(best.dataset.pageNum, 10);
        if (n && n !== currentPage) {
          currentPage = n;
          updateHud();
          scheduleSave();
        }
      }
    }, { root: area, threshold: [0.25, 0.5, 0.75] });

    pageEls.forEach(function (el) {
      renderObserver.observe(el);
      currentObserver.observe(el);
    });

    // Resume from last-seen page if we have one.
    const resumePage = parsePageToken(resumeToken);
    if (resumePage > 1 && resumePage <= numPages) {
      // Wait a tick so the placeholder has layout size before scrollIntoView.
      setTimeout(function () { pageEls[resumePage - 1].scrollIntoView({ block: 'start' }); }, 50);
    }

    updateHud();
  }

  function parsePageToken(s) {
    const m = /^page:(\d+)$/.exec(s || '');
    return m ? parseInt(m[1], 10) : 1;
  }

  function updateHud() {
    const pct = percent();
    if (percentLabel) percentLabel.textContent = pct + '%';
    if (bar) bar.style.width = pct + '%';
  }

  function percent() {
    if (!numPages) return 0;
    return Math.max(0, Math.min(100, Math.round((currentPage / numPages) * 100)));
  }

  function scheduleSave() {
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(sendProgress, 2500);
  }

  function currentPayload() {
    return { percent: percent(), cfi: 'page:' + currentPage };
  }

  function sendProgress() {
    fetch(saveURL, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(currentPayload()),
    }).catch(function () { /* retry on next scroll */ });
  }

  // Flush on close/navigate.
  window.addEventListener('pagehide', function () {
    if (!navigator.sendBeacon) return;
    const blob = new Blob([JSON.stringify(currentPayload())], { type: 'application/json' });
    navigator.sendBeacon(saveURL, blob);
  });

  // Prev/next buttons — jump to the previous/next page's scroll position.
  document.getElementById('reader-prev').addEventListener('click', function () {
    if (currentPage > 1) pageEls[currentPage - 2].scrollIntoView({ block: 'start', behavior: 'smooth' });
  });
  document.getElementById('reader-next').addEventListener('click', function () {
    if (currentPage < numPages) pageEls[currentPage].scrollIntoView({ block: 'start', behavior: 'smooth' });
  });

  document.addEventListener('keydown', function (e) {
    if (e.target && 'value' in e.target) return;
    if (e.key === 'Escape') { window.location.href = '/app/book/' + encodeURIComponent(bookID); }
    else if (e.key === 'ArrowLeft' || e.key === 'k') {
      if (currentPage > 1) pageEls[currentPage - 2].scrollIntoView({ block: 'start', behavior: 'smooth' });
    } else if (e.key === 'ArrowRight' || e.key === 'j' || e.key === ' ') {
      e.preventDefault();
      if (currentPage < numPages) pageEls[currentPage].scrollIntoView({ block: 'start', behavior: 'smooth' });
    }
  });

  function escapeHTML(s) {
    return s.replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', '\'': '&#39;' }[c];
    });
  }
})();
