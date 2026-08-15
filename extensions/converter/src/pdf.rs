//! The PDF gate (ADR-0036): classify before converting, refuse loudly
//! after, never silently hand a near-empty markdown downstream.
//!
//! anydoc's PDF engine *is* pdf-inspector, so this module is not a
//! second parser bolted on — it is the same engine asked the
//! classification question directly, so the refusal can carry a
//! machine-readable `class` and the per-page evidence instead of a
//! prose-only error. Three refusal classes:
//!
//!   scanned — the classifier says the document has no text layer
//!   mixed   — the classifier says Mixed and too few pages carry text
//!   sparse  — the classifier passed it, but conversion yielded almost
//!             nothing: the sampling-based verdict missed a mostly
//!             image book with a typed title page, and the output is
//!             the proof
//!
//! The gate never *blocks* a conversion on its own failure: a PDF the
//! inspector cannot read at all falls through to anydoc, which answers
//! with its own error. Refusing is the gate's only power.

use pdf_inspector::{PdfProcessResult, PdfType};

/// Refusal is one gate verdict: the machine-readable class a future
/// OCR stage routes on (ADR-0036 §3), and the human sentence that
/// lands verbatim on the rendition row.
pub struct Refusal {
    pub class: &'static str,
    pub message: String,
}

/// mixedRefusePastOcrShare: a Mixed PDF is refused when at least this
/// share of its pages need OCR. A book-shaped Mixed — image cover,
/// plates, text everywhere else — sits far below it; a scan with a
/// typed preface sits far above. A constant, not config: a dial nobody
/// can reason about is not a setting (ADR-0036 §4).
const MIXED_REFUSE_PAST_OCR_SHARE: f64 = 0.5;

/// Sparse-output floor: a conversion this thin over this many pages is
/// a text layer that is effectively absent, whatever the classifier
/// said. Real prose runs hundreds of bytes a page; 25 is an order of
/// magnitude below anything a readable book produces, so only the
/// pathological case trips it.
const SPARSE_MIN_PAGES: u32 = 5;
const SPARSE_MIN_BYTES_PER_PAGE: usize = 25;

/// detect classifies the bytes, swallowing inspector failures: a PDF
/// the inspector cannot read is anydoc's to refuse in its own words.
pub fn detect(body: &[u8]) -> Option<PdfProcessResult> {
    pdf_inspector::detect_pdf_mem(body).ok()
}

/// refuse_before is the pre-conversion half of the gate.
pub fn refuse_before(d: &PdfProcessResult) -> Option<Refusal> {
    let no_text = d.pages_needing_ocr.len();
    match d.pdf_type {
        PdfType::Scanned => Some(Refusal {
            class: "scanned",
            message: format!(
                "scanned PDF: {no_text} of {} pages have no text layer; \
                 OCR is not available, and converting would produce an empty document",
                d.page_count
            ),
        }),
        PdfType::Mixed => {
            if d.page_count > 0
                && (no_text as f64) / (d.page_count as f64) >= MIXED_REFUSE_PAST_OCR_SHARE
            {
                Some(Refusal {
                    class: "mixed",
                    message: format!(
                        "mostly-scanned PDF: {no_text} of {} pages have no text layer; \
                         OCR is not available, and converting would drop those pages silently",
                        d.page_count
                    ),
                })
            } else {
                None
            }
        }
        _ => None,
    }
}

/// refuse_sparse is the post-conversion half: the classifier's verdict
/// was sampled, the output is measured. A multi-page PDF whose whole
/// conversion is a few bytes is a text layer that does not exist —
/// letting it through is how a near-empty markdown silently fed the
/// reading guide and the generated EPUB.
pub fn refuse_sparse(d: &PdfProcessResult, markdown: &str) -> Option<Refusal> {
    let bytes = markdown.trim().len();
    if d.page_count >= SPARSE_MIN_PAGES && bytes < d.page_count as usize * SPARSE_MIN_BYTES_PER_PAGE {
        return Some(Refusal {
            class: "sparse",
            message: format!(
                "PDF text layer is effectively absent: {} pages converted to {bytes} bytes \
                 of markdown; OCR is not available",
                d.page_count
            ),
        });
    }
    None
}
