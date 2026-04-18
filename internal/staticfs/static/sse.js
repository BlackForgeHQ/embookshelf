// embookshelf SSE client.
//
// Opens a single EventSource to /events and fans server-side events out to
// HTMX swap targets. The mapping is intentionally tiny: each event has a
// `Name` (matches EventSource addEventListener name) and a JSON `data`
// payload whose `id` + event-specific rules decide what to refresh.
(function () {
  if (!('EventSource' in window)) return;

  function connect() {
    const es = new EventSource('/events', { withCredentials: true });

    es.addEventListener('bookdrop.updated', function (e) {
      try {
        const { id } = JSON.parse(e.data);
        if (!id) return;
        const row = document.getElementById('bdrop-row-' + id);
        if (!row || !window.htmx) return;
        window.htmx.ajax('GET', '/app/bookdrop/row/' + encodeURIComponent(id), {
          target: '#bdrop-row-' + id,
          swap: 'outerHTML',
        });
      } catch (_) { /* swallow malformed payloads */ }
    });

    es.onerror = function () {
      // Browser retries automatically; close-and-reopen gives us a clean backoff.
      es.close();
      setTimeout(connect, 3000);
    };
  }

  connect();
})();
