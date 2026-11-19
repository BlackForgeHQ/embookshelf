//! PDF → markdown (ADR-0040, superseding the gate of ADR-0036).
//!
//! One engine — pdf-inspector, anydoc's own PDF engine — asked twice:
//!
//!   - the **whole-document pass** writes the markdown. Tagged headings,
//!     ruled tables and cross-page flow are its alone, so it is the text
//!     every page gets when it has any. It runs with a page marker
//!     between pages so its output can be attributed to pages.
//!   - the **per-page pass** fills in the pages the whole-document pass
//!     left empty. Its verdict is made page by page: the whole-document
//!     classifier stops at the first page without text, which is how an
//!     image cover in front of a text book used to refuse the book.
//!
//! A page neither pass finds text on is *missing*. The conversion goes
//! ahead and names the missing pages, to be recorded on the rendition
//! row; only a PDF with no text on any page is refused. "Missing" means
//! no extractable text — a scanned page and a deliberately blank one
//! look the same from here (ADR-0040 §4).
//!
//! When the PDF has two or more top-level bookmarks, they become the
//! document's `#` headings at the pages they point to, and the engine's
//! own `#` headings step down to `##`: the EPUB stage then splits on the
//! author's chapters rather than on font-size guesses.

use std::collections::BTreeMap;

use pdf_inspector::{MarkdownOptions, PdfError, PdfOptions};

/// Conversion is a converted PDF: the markdown, and what the page
/// record needs — how many pages the document has and which of them
/// (1-based, ascending) gave no text.
pub struct Conversion {
    pub markdown: String,
    pub page_count: u32,
    pub missing: Vec<u32>,
}

/// Refusal is the one verdict that stops a conversion: no page has text.
/// The class is the machine-readable seam a future OCR stage routes on
/// (ADR-0036 §3); the message lands verbatim on the rendition row.
pub struct Refusal {
    pub class: &'static str,
    pub message: String,
}

pub enum Failure {
    Refused(Refusal),
    /// The engine could not read the file at all — corrupt, or encrypted
    /// behind a password we do not have.
    Unreadable(PdfError),
}

pub fn convert(body: &[u8]) -> Result<Conversion, Failure> {
    let options = PdfOptions::new().markdown(MarkdownOptions {
        include_page_numbers: true,
        ..MarkdownOptions::default()
    });
    let whole =
        pdf_inspector::process_pdf_mem_with_options(body, options).map_err(Failure::Unreadable)?;
    let page_count = whole.page_count;

    let mut pages = split_pages(whole.markdown.as_deref().unwrap_or(""), page_count);
    fill_from_per_page(body, &mut pages);

    let missing: Vec<u32> = pages
        .iter()
        .enumerate()
        .filter(|(_, p)| p.is_none())
        .map(|(i, _)| i as u32 + 1)
        .collect();
    if missing.len() as u32 == page_count {
        return Err(Failure::Refused(Refusal {
            class: "scanned",
            message: if page_count == 0 {
                "PDF has no pages".to_string()
            } else {
                format!(
                    "PDF has no extractable text on any of its {page_count} pages \
                     (scanned or image-only); OCR is not available"
                )
            },
        }));
    }
    if !missing.is_empty() {
        eprintln!(
            "pdf: {} of {page_count} pages have no extractable text: {}",
            missing.len(),
            page_ranges(&missing)
        );
    }

    let chapters = chapters(body, page_count);
    Ok(Conversion {
        markdown: assemble(&pages, &chapters),
        page_count,
        missing,
    })
}

/// page_ranges renders ascending 1-based pages compactly — "1,10-15" —
/// the form the missing-pages header carries and the row stores.
pub fn page_ranges(pages: &[u32]) -> String {
    let mut out: Vec<String> = Vec::new();
    let mut i = 0;
    while i < pages.len() {
        let start = pages[i];
        let mut end = start;
        while i + 1 < pages.len() && pages[i + 1] == end + 1 {
            i += 1;
            end = pages[i];
        }
        out.push(if start == end {
            start.to_string()
        } else {
            format!("{start}-{end}")
        });
        i += 1;
    }
    out.join(",")
}

/// split_pages attributes the whole-document markdown to pages by its
/// `<!-- Page N -->` markers, one slot per page, None where a page has
/// no text. Text ahead of the first marker belongs to that marker's
/// page; output with no markers at all is attributed to page 1 rather
/// than dropped.
fn split_pages(markdown: &str, page_count: u32) -> Vec<Option<String>> {
    let mut pages = vec![String::new(); page_count as usize];
    let mut current: Option<usize> = None;
    let mut preamble = String::new();
    for line in markdown.lines() {
        if let Some(n) = page_marker(line).filter(|n| (1..=page_count).contains(n)) {
            let idx = (n - 1) as usize;
            if current.is_none() {
                pages[idx].push_str(&preamble);
            }
            current = Some(idx);
            continue;
        }
        let buf = match current {
            Some(idx) => &mut pages[idx],
            None => &mut preamble,
        };
        buf.push_str(line);
        buf.push('\n');
    }
    if current.is_none() && page_count > 0 {
        pages[0] = preamble;
    }
    pages
        .into_iter()
        .map(|p| {
            let t = p.trim();
            (!t.is_empty()).then(|| t.to_string())
        })
        .collect()
}

fn page_marker(line: &str) -> Option<u32> {
    line.trim()
        .strip_prefix("<!-- Page ")?
        .strip_suffix(" -->")?
        .parse()
        .ok()
}

/// fill_from_per_page asks the per-page pass for exactly the pages the
/// whole-document pass left empty. Its failure is logged and leaves
/// those pages missing — the per-page pass can only add text, never
/// block a conversion the whole-document pass already made.
fn fill_from_per_page(body: &[u8], pages: &mut [Option<String>]) {
    let wanted: Vec<u32> = pages
        .iter()
        .enumerate()
        .filter(|(_, p)| p.is_none())
        .map(|(i, _)| i as u32)
        .collect();
    if wanted.is_empty() {
        return;
    }
    match pdf_inspector::extract_pages_markdown_mem(body, Some(&wanted)) {
        Ok(result) => {
            for page in result.pages {
                let text = page.markdown.trim();
                if let Some(slot) = pages.get_mut(page.page as usize) {
                    if slot.is_none() && !text.is_empty() {
                        *slot = Some(text.to_string());
                    }
                }
            }
        }
        Err(err) => eprintln!("pdf: per-page pass failed, its pages stay missing: {err}"),
    }
}

/// chapters reads the PDF's top-level bookmarks into page → titles. A
/// PDF the bookmark reader cannot open, or with fewer than two top-level
/// entries, has none: a lone bookmark is a title, not a chapter list.
fn chapters(body: &[u8], page_count: u32) -> BTreeMap<u32, Vec<String>> {
    match lopdf::Document::load_mem(body).and_then(|doc| doc.get_toc()) {
        Ok(toc) => chapters_from(
            toc.toc.into_iter().map(|e| (e.level, e.title, e.page)),
            page_count,
        ),
        Err(_) => BTreeMap::new(),
    }
}

fn chapters_from(
    entries: impl IntoIterator<Item = (usize, String, usize)>,
    page_count: u32,
) -> BTreeMap<u32, Vec<String>> {
    let mut out: BTreeMap<u32, Vec<String>> = BTreeMap::new();
    let mut n = 0;
    for (level, title, page) in entries {
        let title = title.split_whitespace().collect::<Vec<_>>().join(" ");
        if level != 1 || title.is_empty() || page == 0 || page > page_count as usize {
            continue;
        }
        out.entry(page as u32).or_default().push(title);
        n += 1;
    }
    if n < 2 {
        BTreeMap::new()
    } else {
        out
    }
}

/// assemble joins the pages in order, with each bookmark's `#` heading
/// ahead of the page it points to — including a missing page, so the
/// chapter is still there to say its text is not.
fn assemble(pages: &[Option<String>], chapters: &BTreeMap<u32, Vec<String>>) -> String {
    let mut parts: Vec<String> = Vec::new();
    let mut in_fence = false;
    for (i, page) in pages.iter().enumerate() {
        if let Some(titles) = chapters.get(&(i as u32 + 1)) {
            parts.extend(titles.iter().map(|t| format!("# {t}")));
        }
        if let Some(text) = page {
            parts.push(if chapters.is_empty() {
                text.clone()
            } else {
                demote_h1(text, &mut in_fence)
            });
        }
    }
    let mut markdown = parts.join("\n\n");
    markdown.push('\n');
    markdown
}

/// demote_h1 steps `# ` headings down to `## ` outside fenced code, the
/// fence state carried across pages.
fn demote_h1(text: &str, in_fence: &mut bool) -> String {
    let mut out = String::with_capacity(text.len() + 8);
    for (i, line) in text.lines().enumerate() {
        if i > 0 {
            out.push('\n');
        }
        let trimmed = line.trim_start();
        if trimmed.starts_with("```") || trimmed.starts_with("~~~") {
            *in_fence = !*in_fence;
        } else if !*in_fence && line.starts_with("# ") {
            out.push('#');
        }
        out.push_str(line);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn page_ranges_compacts_runs() {
        assert_eq!(page_ranges(&[]), "");
        assert_eq!(page_ranges(&[1]), "1");
        assert_eq!(page_ranges(&[1, 10, 11, 12, 13, 14, 15]), "1,10-15");
        assert_eq!(page_ranges(&[2, 4, 5, 9]), "2,4-5,9");
    }

    // A page the markers skip is a page with no text; text ahead of the
    // first marker stays with that marker's page.
    #[test]
    fn split_pages_attributes_text_by_marker() {
        let md = "front\n\n<!-- Page 2 -->\n\ntwo\n\n<!-- Page 4 -->\n\nfour a\n\nfour b\n";
        let pages = split_pages(md, 4);
        assert_eq!(pages[0], None);
        assert_eq!(pages[1].as_deref(), Some("front\n\n\ntwo"));
        assert_eq!(pages[2], None);
        assert_eq!(pages[3].as_deref(), Some("four a\n\nfour b"));
    }

    #[test]
    fn split_pages_without_markers_keeps_the_text_on_page_one() {
        let pages = split_pages("just text\n", 3);
        assert_eq!(pages[0].as_deref(), Some("just text"));
        assert_eq!(pages[1], None);
    }

    // Only top-level entries count, and one alone is a title, not a
    // chapter list.
    #[test]
    fn chapters_need_two_top_level_bookmarks() {
        let one = chapters_from([(1, "Only".into(), 1), (2, "Sub".into(), 2)], 5);
        assert!(one.is_empty());

        let two = chapters_from(
            [
                (1, "Cover".into(), 1),
                (2, "Section".into(), 2),
                (1, "Chapter  1:\nStart".into(), 3),
                (1, "Out of range".into(), 9),
            ],
            5,
        );
        assert_eq!(two.get(&1), Some(&vec!["Cover".to_string()]));
        assert_eq!(two.get(&3), Some(&vec!["Chapter 1: Start".to_string()]));
        assert_eq!(two.len(), 2);
    }

    // Bookmarks become the only H1s: the engine's own step down, fenced
    // code is left alone, and a missing page keeps its chapter heading.
    #[test]
    fn assemble_puts_bookmarks_ahead_of_their_pages() {
        let pages = vec![
            Some("# Big Title\n\nintro".to_string()),
            None,
            Some("```\n# not a heading\n```\n# Section".to_string()),
        ];
        let chapters = chapters_from([(1, "One".into(), 1), (1, "Two".into(), 2)], 3);
        assert_eq!(
            assemble(&pages, &chapters),
            "# One\n\n## Big Title\n\nintro\n\n# Two\n\n```\n# not a heading\n```\n## Section\n"
        );
    }

    #[test]
    fn assemble_without_bookmarks_keeps_the_engines_headings() {
        let pages = vec![Some("# Title".to_string()), Some("text".to_string())];
        assert_eq!(assemble(&pages, &BTreeMap::new()), "# Title\n\ntext\n");
    }
}
