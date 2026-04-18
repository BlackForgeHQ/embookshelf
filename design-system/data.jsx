// Book data. Covers use "palette" keys mapped in Cover.jsx.
// All titles/authors are ORIGINAL, not real books.

const BOOKS = [
  // Currently reading
  { id: 'b1', title: 'The Cartographers of Dusk', author: 'Mira Halden', format: 'EPUB', pages: 412, progress: 0.62, rating: 4.5, year: 2023, series: null, tags: ['Fiction', 'Literary'], palette: 'navy', style: 'centered-line', shelf: ['Reading Now', 'To Finish'], addedAt: '2026-03-12', description: 'A widowed surveyor inherits her grandfather\'s half-finished maps and follows them through the disappearing villages of the northern coast. Halden\'s language is quiet and exact; her landscapes threaten to dissolve at every turn.' },
  { id: 'b2', title: 'Slow Weather', author: 'P. Okafor', format: 'EPUB', pages: 288, progress: 0.18, rating: 4.2, year: 2024, series: null, tags: ['Essays'], palette: 'ochre', style: 'minimal-top', shelf: ['Reading Now'], addedAt: '2026-04-02', description: 'Fourteen essays on patience, tide charts, and the private weather of long relationships.' },
  { id: 'b3', title: 'The Unmade Bed', author: 'Harriet Vale', format: 'PDF', pages: 336, progress: 0.91, rating: 4.0, year: 2022, series: null, tags: ['Memoir'], palette: 'plum', style: 'stacked-serif', shelf: ['Reading Now'], addedAt: '2026-02-18', description: 'Vale\'s account of a year spent in her mother\'s empty house.' },

  // Recently added
  { id: 'b4', title: 'Instruments of the Quiet Country', author: 'J. Eriksen', format: 'EPUB', pages: 520, rating: 4.6, year: 2025, series: null, tags: ['Fiction'], palette: 'forest', style: 'block', shelf: ['New'], addedAt: '2026-04-15', description: 'A nine-year saga of three families binding brass instruments in a shuttering mill town.' },
  { id: 'b5', title: 'Field Notes on Loneliness', author: 'Tam Aoki', format: 'EPUB', pages: 196, rating: 4.3, year: 2024, series: null, tags: ['Essays', 'Philosophy'], palette: 'cream', style: 'typographic', shelf: ['New'], addedAt: '2026-04-10', description: null },
  { id: 'b6', title: 'A History of the Footnote', author: 'R. Delahaye', format: 'PDF', pages: 624, rating: 4.1, year: 2019, series: null, tags: ['Nonfiction', 'History'], palette: 'olive', style: 'centered-line', shelf: ['New'], addedAt: '2026-04-08', description: null },
  { id: 'b7', title: 'Seventeen Winters', author: 'Ludo Barenas', format: 'EPUB', pages: 348, rating: 3.9, year: 2024, series: 'The Barenas Chronicles', seriesNum: 2, tags: ['Fiction'], palette: 'teal', style: 'stacked-serif', shelf: ['New'], addedAt: '2026-04-06', description: null },
  { id: 'b8', title: 'The Pigeon Courier', author: 'N. Sagara', format: 'EPUB', pages: 240, rating: 4.4, year: 2023, series: null, tags: ['Fiction', 'Historical'], palette: 'rust', style: 'minimal-top', shelf: [], addedAt: '2026-04-01', description: null },
  { id: 'b9', title: 'Unbound', author: 'Imogen Flores', format: 'EPUB', pages: 302, rating: 4.0, year: 2022, series: null, tags: ['Fiction'], palette: 'brick', style: 'block', shelf: [], addedAt: '2026-03-28', description: null },
  { id: 'b10', title: 'Orchard of Small Hours', author: 'T. Greene', format: 'EPUB', pages: 278, rating: 4.2, year: 2023, series: null, tags: ['Poetry'], palette: 'olive', style: 'typographic', shelf: [], addedAt: '2026-03-20', description: null },
  { id: 'b11', title: 'The Bookbinder\'s Complaint', author: 'F. Okereke', format: 'PDF', pages: 180, rating: 3.8, year: 2021, series: null, tags: ['Essays'], palette: 'navy', style: 'minimal-top', shelf: [], addedAt: '2026-03-14' },
  { id: 'b12', title: 'Radio Static for Sleepers', author: 'A. Lund', format: 'EPUB', pages: 420, rating: 4.5, year: 2024, series: null, tags: ['Fiction', 'Sci-Fi'], palette: 'plum', style: 'block', shelf: [], addedAt: '2026-03-04' },

  // Finished shelf
  { id: 'b13', title: 'Below the Watermark', author: 'Carys Meredith', format: 'EPUB', pages: 364, rating: 4.7, year: 2020, series: null, tags: ['Fiction'], palette: 'teal', style: 'centered-line', shelf: ['Finished'], addedAt: '2025-11-10', progress: 1.0 },
  { id: 'b14', title: 'Household Gods', author: 'Edwin Pryce', format: 'EPUB', pages: 298, rating: 4.0, year: 2022, series: null, tags: ['Fiction'], palette: 'forest', style: 'stacked-serif', shelf: ['Finished'], addedAt: '2025-10-02', progress: 1.0 },
  { id: 'b15', title: 'The Salt Ledger', author: 'N. Halloran', format: 'EPUB', pages: 412, rating: 4.3, year: 2023, series: null, tags: ['Historical'], palette: 'ochre', style: 'typographic', shelf: ['Finished'], addedAt: '2025-09-15', progress: 1.0 },
  { id: 'b16', title: 'Almanac for the Sleepless', author: 'Verity Ng', format: 'PDF', pages: 220, rating: 4.1, year: 2021, series: null, tags: ['Nonfiction'], palette: 'rust', style: 'minimal-top', shelf: ['Finished'], addedAt: '2025-08-20', progress: 1.0 },

  // Placeholders (wider catalogue, no custom cover design)
  { id: 'b17', title: 'Distant Provinces', author: 'K. Sato', format: 'EPUB', pages: 310, rating: 3.9, year: 2023, tags: ['Fiction'], placeholder: true, shelf: [], addedAt: '2026-02-10' },
  { id: 'b18', title: 'Letters to a Younger Cartographer', author: 'Mira Halden', format: 'EPUB', pages: 142, rating: 4.2, year: 2019, series: null, tags: ['Essays'], placeholder: true, shelf: [], addedAt: '2026-02-02' },
  { id: 'b19', title: 'The Glass Apiary', author: 'B. Chen', format: 'EPUB', pages: 380, rating: 4.4, year: 2024, tags: ['Fiction'], placeholder: true, shelf: [], addedAt: '2026-01-28' },
  { id: 'b20', title: 'Tidewater', author: 'E. Marsh', format: 'EPUB', pages: 268, rating: 3.7, year: 2022, tags: ['Fiction'], placeholder: true, shelf: [], addedAt: '2026-01-12' },
];

const SHELVES = [
  { id: 'reading', name: 'Reading Now', count: 3, icon: 'book-open' },
  { id: 'new', name: 'Recently Added', count: 9, icon: 'sparkle' },
  { id: 'finished', name: 'Finished', count: 24, icon: 'check' },
  { id: 'tofinish', name: 'To Finish', count: 6, icon: 'flag' },
];

const MAGIC_SHELVES = [
  { id: 'm1', name: 'Halden & Aoki', count: 5, rule: 'author in {Mira Halden, Tam Aoki}' },
  { id: 'm2', name: 'Essays, 4★+', count: 12, rule: 'tags contains Essays AND rating ≥ 4' },
  { id: 'm3', name: 'Unread 2024+', count: 17, rule: 'year ≥ 2024 AND progress = 0' },
];

const LIBRARIES = [
  { id: 'main', name: 'Main Library', path: '/books/main', count: 847, color: 'oklch(0.48 0.09 35)' },
  { id: 'academic', name: 'Academic', path: '/books/academic', count: 213, color: 'oklch(0.42 0.06 110)' },
  { id: 'comics', name: 'Comics', path: '/books/comics', count: 142, color: 'oklch(0.38 0.05 200)' },
];

const CURRENT_USER = { name: 'Rowan Ashby', handle: 'rowan', initials: 'RA', role: 'Admin' };

// Reading content for the reader view
const READER_CONTENT = {
  chapter: 'Chapter Seven',
  title: 'The Mapmaker\'s Return',
  paragraphs: [
    'The village had not been on any of her grandfather\'s maps. Ellen could not at first decide whether this was because he had missed it, or because it had not yet existed when he last passed through — though neither explanation quite held: the postmaster\'s office bore a date of 1927, and the stones in the churchyard went back further than that.',
    'She walked down what she took to be the main street, pulling her coat around her. It was not yet evening but the light had already begun to fail in the way it did only this far north, the sky bleaching out rather than dimming. Three doors opened onto the street; none had numbers, none had names. At the far end the road gave up and turned into a path, and the path vanished into the slow grey water of the inlet.',
    '"You\'ll be the one looking for the house," a woman said from behind her.',
    'Ellen turned. The woman was perhaps sixty, dressed for weather that had not yet arrived, and she was smiling at Ellen with an expression that contained no surprise at all.',
    '"I don\'t know," Ellen said. "I suppose I might be."',
    '"The map is in the back room of the Institute. They won\'t have told you, because they don\'t have to. It\'s been waiting there for you since the summer your grandfather died."',
    'Ellen opened her mouth to ask how the woman knew any of this, and then shut it again, because in this place asking seemed like the least useful thing she could do. She had come six hundred miles north to stand on a dying street in a village that, until that morning, she had not been sure was real. Whatever she had expected, it was not to be expected in return.',
    'The woman extended a hand. "Ingrid Vaal. I have your grandfather\'s last notebook. He told me you\'d come for it in your own time, and I have learned not to argue with him, even now."',
  ]
};

const BOOKDROP_FILES = [
  { id: 'd1', filename: 'halden_mira_-_north_light_drafts.epub', size: '2.1 MB', format: 'EPUB', status: 'ready', detected: { title: 'North Light: Drafts & Fragments', author: 'Mira Halden', year: 2020, cover: true } },
  { id: 'd2', filename: 'the-footnote-vol-II.pdf', size: '18.4 MB', format: 'PDF', status: 'ready', detected: { title: 'A History of the Footnote, Vol. II', author: 'R. Delahaye', year: 2024, cover: true } },
  { id: 'd3', filename: 'unknown_manuscript_final.epub', size: '0.9 MB', format: 'EPUB', status: 'needs-review', detected: { title: null, author: null, year: null, cover: false } },
  { id: 'd4', filename: 'comic_issue_042.cbz', size: '44.2 MB', format: 'CBZ', status: 'processing', detected: { title: 'The Quiet Commission #42', author: 'Studio Greensleeve', year: 2025, cover: true } },
  { id: 'd5', filename: 'sagara_courier_audiobook.m4b', size: '312 MB', format: 'M4B', status: 'ready', detected: { title: 'The Pigeon Courier (Audio)', author: 'N. Sagara, read by A. Weir', year: 2024, cover: false } },
];

const NOTES = [
  { id: 'n1', bookId: 'b1', page: 138, text: 'The quiet inversion here — the woman expects Ellen rather than the other way around. Echoes the cartography motif: who is mapping whom.', date: '2026-04-15' },
  { id: 'n2', bookId: 'b1', page: 142, text: 'Highlighted: "she had come six hundred miles north…"', date: '2026-04-15', highlight: true },
  { id: 'n3', bookId: 'b3', page: 84, text: 'Vale\'s refusal of closure in this chapter is the whole argument of the book.', date: '2026-04-11' },
  { id: 'n4', bookId: 'b2', page: 12, text: 'Essay one reads more like a poem. Keep returning to the tide-chart paragraph.', date: '2026-04-03' },
];

// Reading activity (last 12 weeks) — minutes per day
const ACTIVITY = (() => {
  const seed = [23,0,41,55,22,0,12, 34,27,0,0,48,51,15, 0,22,34,28,0,0,41, 18,30,42,0,22,36,44, 28,38,0,55,41,29,0, 33,22,0,31,49,38,20, 44,30,52,0,27,41,33, 0,38,45,22,18,0,36, 41,33,28,55,42,0,38, 22,48,31,0,26,44,35, 38,55,29,42,33,0,47, 51,28,36,44,52,0,38];
  return seed;
})();

Object.assign(window, { BOOKS, SHELVES, MAGIC_SHELVES, LIBRARIES, CURRENT_USER, READER_CONTENT, BOOKDROP_FILES, NOTES, ACTIVITY });
