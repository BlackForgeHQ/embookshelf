//! The PDF gate (ADR-0036): classify before converting, measure after,
//! refuse loudly — never silently hand a near-empty markdown
//! downstream.
//!
//! anydoc's PDF engine *is* pdf-inspector, so this module is not a
//! second parser bolted on — it is the same engine asked the
//! classification question directly, so a refusal can carry a
//! machine-readable `class` and the page evidence instead of a
//! prose-only error.
//!
//! Two halves, shaped by what probing showed about the engine:
//!
//!   - Pre-conversion, the **Scanned** and **ImageBased** verdicts
//!     refuse with their class. This adds no refusal the engine was
//!     not already making: anydoc's own converter short-circuits on
//!     exactly these verdicts ("PDF has no extractable text … OCR is
//!     required" — in the shipped 0.1.7 and in 1.x alike), so the
//!     gate's contribution is the machine-readable class and the page
//!     evidence, not a new rejection.
//!   - Post-conversion, the gate **measures the output**: the shipped
//!     engine happily served 6 bytes of markdown for a 10-page PDF
//!     whose only text was a title page (classified TextBased — the
//!     sampled verdict misses this shape). A multi-page PDF whose
//!     whole conversion is a few bytes per page is a text layer that
//!     does not exist; the refusal is labelled `mixed` when the
//!     classifier saw the mixture, `sparse` when it was fooled
//!     outright.
//!
//! Known limit, on the record: a partially-scanned book whose text
//! pages clear the floor converts with its scanned pages silently
//! absent. Catching that needs per-page truth the classifier does not
//! reliably give (probing found it flags dense text pages as needing
//! OCR when an image object merely exists in the file); OCR routing is
//! the eventual answer (ADR-0036 §3).
//!
//! The gate never blocks a conversion on its own failure: a PDF the
//! inspector cannot read falls through to anydoc, which answers with
//! its own error. Refusing is the gate's only power.

use pdf_inspector::PdfType;

/// Refusal is one gate verdict: the machine-readable class a future
/// OCR stage routes on (ADR-0036 §3), and the human sentence that
/// lands verbatim on the rendition row.
pub struct Refusal {
    pub class: &'static str,
    pub message: String,
}

/// Detection is the slice of the inspector's answer the gate needs —
/// carried as plain values so both halves are pure functions a test
/// drives without crafting PDF bytes.
pub struct Detection {
    pub pdf_type: PdfType,
    pub page_count: u32,
    pub pages_without_text: usize,
}

/// Sparse-output floor: a conversion this thin over this many pages is
/// a text layer that is effectively absent, whatever the classifier
/// said. Real prose runs hundreds of bytes a page; 25 is an order of
/// magnitude below anything a readable book produces, so only the
/// pathological case trips it.
const SPARSE_MIN_PAGES: u32 = 5;
const SPARSE_MIN_BYTES_PER_PAGE: usize = 25;

/// detect classifies the bytes, swallowing inspector failures after a
/// log line: a PDF the inspector cannot read is anydoc's to refuse in
/// its own words, and the gate must never block on its own defect —
/// but the swallow is on the record, not silent.
pub fn detect(body: &[u8]) -> Option<Detection> {
    match pdf_inspector::detect_pdf_mem(body) {
        Ok(d) => Some(Detection {
            pdf_type: d.pdf_type,
            page_count: d.page_count,
            pages_without_text: d.pages_needing_ocr.len(),
        }),
        Err(err) => {
            eprintln!("pdf gate: inspection failed, conversion proceeds ungated: {err}");
            None
        }
    }
}

/// refuse_before is the pre-conversion half: the two verdicts the
/// engine itself refuses to convert, re-answered with a class and the
/// page evidence instead of prose alone (module docs — this arm cannot
/// refuse anything the engine would have converted).
pub fn refuse_before(d: &Detection) -> Option<Refusal> {
    let class = match d.pdf_type {
        PdfType::Scanned => "scanned",
        PdfType::ImageBased => "image",
        _ => return None,
    };
    Some(Refusal {
        class,
        message: format!(
            "{} PDF: {} of {} pages have no text layer; \
             OCR is not available, and converting would produce an empty document",
            if class == "image" {
                "image-only"
            } else {
                "scanned"
            },
            d.pages_without_text,
            d.page_count
        ),
    })
}

/// refuse_sparse is the post-conversion half: the verdict was sampled,
/// the output is measured. The class carries what the classifier
/// thought — `mixed` routes the same as `scanned` for a future OCR
/// stage, `sparse` says even the classifier was fooled.
pub fn refuse_sparse(d: &Detection, markdown: &str) -> Option<Refusal> {
    let bytes = markdown.trim().len();
    if d.page_count < SPARSE_MIN_PAGES || bytes >= d.page_count as usize * SPARSE_MIN_BYTES_PER_PAGE
    {
        return None;
    }
    let class = match d.pdf_type {
        PdfType::Mixed => "mixed",
        _ => "sparse",
    };
    Some(Refusal {
        class,
        message: format!(
            "PDF text layer is effectively absent: {} pages converted to {bytes} bytes \
             of markdown; OCR is not available",
            d.page_count
        ),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn det(t: PdfType, pages: u32, no_text: usize) -> Detection {
        Detection {
            pdf_type: t,
            page_count: pages,
            pages_without_text: no_text,
        }
    }

    // Scanned refuses before a byte is converted, with the evidence.
    #[test]
    fn scanned_refuses_up_front() {
        let r = refuse_before(&det(PdfType::Scanned, 300, 300)).expect("must refuse");
        assert_eq!(r.class, "scanned");
        assert!(r.message.contains("300 of 300 pages"), "{}", r.message);
    }

    // ImageBased carries its own class — the engine refuses these bytes
    // regardless, so the class is pure gain, never a new rejection.
    #[test]
    fn image_based_refuses_up_front_with_its_class() {
        let r = refuse_before(&det(PdfType::ImageBased, 10, 10)).expect("must refuse");
        assert_eq!(r.class, "image");
        assert!(r.message.contains("image-only"), "{}", r.message);
    }

    // The verdicts the engine converts do not refuse up front.
    #[test]
    fn convertible_verdicts_pass_the_pre_gate() {
        for t in [PdfType::TextBased, PdfType::Mixed] {
            assert!(
                refuse_before(&det(t, 10, 10)).is_none(),
                "{t:?} refused pre-conversion"
            );
        }
    }

    // The measured gate: thin output over enough pages refuses, and the
    // class carries the classifier's verdict for the future OCR router.
    #[test]
    fn sparse_output_refuses_with_the_classifiers_label() {
        for (t, class) in [(PdfType::TextBased, "sparse"), (PdfType::Mixed, "mixed")] {
            let r = refuse_sparse(&det(t, 10, 9), "a few words").expect("must refuse");
            assert_eq!(r.class, class);
        }
    }

    // Ordinary prose density passes; so does a short document however
    // thin — four pages is a pamphlet, not a book-shaped scan.
    #[test]
    fn dense_or_short_output_passes() {
        let page = "It was a bright cold day in April, and the clocks were striking thirteen. ";
        let dense = page.repeat(40);
        assert!(refuse_sparse(&det(PdfType::TextBased, 10, 0), &dense).is_none());
        assert!(refuse_sparse(&det(PdfType::TextBased, 4, 3), "tiny").is_none());
    }
}
