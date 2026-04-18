// embookshelf metadata-enrichment helper.
//
// Listens for clicks on [data-apply-match] inside a match card and copies
// each data-* attribute into the matching metadata form input. Keeps the
// server out of a round-trip when the user has already decided which
// provider hit to accept — they still hit Save to persist.
(function () {
  // Field name → data-attribute name. The form inputs use these names and
  // each MatchCard exposes the same shape via data-*.
  const MAP = {
    title: 'title',
    author: 'author',
    description: 'description',
    publisher: 'publisher',
    year: 'year',
    isbn: 'isbn',
    series: 'series',
    tags: 'tags',
  };

  document.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-apply-match]');
    if (!btn) return;
    const card = btn.closest('[data-match]');
    const form = document.querySelector('form.book-edit');
    if (!card || !form) return;

    for (const [field, dataKey] of Object.entries(MAP)) {
      const val = card.dataset[dataKey];
      if (val == null) continue;
      const el = form.querySelector('[name="' + field + '"]');
      if (!el) continue;
      // Don't trample user input for fields the match didn't fill.
      if (val === '' && el.value !== '') continue;
      el.value = val;
      // Notify listeners (e.g. custom validation, char counters).
      el.dispatchEvent(new Event('input', { bubbles: true }));
      el.dispatchEvent(new Event('change', { bubbles: true }));
    }

    // Subtle visual ack so the user knows the Apply click registered.
    btn.classList.add('applied');
    setTimeout(function () { btn.classList.remove('applied'); }, 900);
  });
})();
