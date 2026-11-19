use axum::body::Body;
use axum::http::{HeaderMap, Request, StatusCode};
use http_body_util::BodyExt;
use tower::util::ServiceExt;

use embookshelf_converter::app;

async fn post_convert(bytes: &[u8]) -> (StatusCode, Option<String>, String) {
    let (status, headers, body) = post_convert_full(bytes).await;
    let version = header(&headers, "x-converter-version");
    (status, version, body)
}

async fn post_convert_full(bytes: &[u8]) -> (StatusCode, HeaderMap, String) {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/convert")
                .body(Body::from(bytes.to_vec()))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    (status, headers, String::from_utf8_lossy(&body).into_owned())
}

fn header(headers: &HeaderMap, name: &str) -> Option<String> {
    headers.get(name).map(|v| v.to_str().unwrap().to_string())
}

/// The page record a PDF answer carries (ADR-0040).
fn pages(headers: &HeaderMap) -> (Option<String>, Option<String>) {
    (
        header(headers, "x-converter-page-count"),
        header(headers, "x-converter-missing-pages"),
    )
}

#[tokio::test]
async fn converts_rtf_to_markdown() {
    let bytes = include_bytes!("fixtures/sample.rtf");
    let (status, version, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::OK);
    assert!(
        body.contains("Hello"),
        "markdown should carry the fixture text, got: {body}"
    );
    assert_eq!(version.as_deref(), Some(env!("CARGO_PKG_VERSION")));
}

#[tokio::test]
async fn converts_docx_to_markdown() {
    let bytes = include_bytes!("fixtures/sample.docx");
    let (status, headers, body) = post_convert_full(bytes).await;
    assert_eq!(status, StatusCode::OK);
    assert!(
        body.contains("Hello converter fixture from DOCX"),
        "got: {body}"
    );
    // Only paged formats carry a page record.
    assert_eq!(pages(&headers), (None, None));
}

#[tokio::test]
async fn converts_pdf_to_markdown() {
    let bytes = include_bytes!("fixtures/sample.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::OK);
    assert!(!body.is_empty());
}

#[tokio::test]
async fn undetectable_bytes_answer_415_json() {
    let (status, _, body) = post_convert(b"not a document at all, just prose").await;
    assert_eq!(status, StatusCode::UNSUPPORTED_MEDIA_TYPE);
    let parsed: serde_json::Value = serde_json::from_str(&body).expect("error body must be JSON");
    assert!(parsed["error"].is_string());
}

#[tokio::test]
async fn corrupt_but_detected_file_answers_422_json() {
    // A valid PDF signature followed by garbage: detected as PDF, unparseable.
    let mut bytes = b"%PDF-1.7\n".to_vec();
    bytes.extend_from_slice(&[0xFF; 64]);
    let (status, _, body) = post_convert(&bytes).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    let parsed: serde_json::Value = serde_json::from_str(&body).expect("error body must be JSON");
    assert!(parsed["error"].is_string());
}

#[tokio::test]
async fn empty_body_answers_415() {
    let (status, _, _) = post_convert(b"").await;
    assert_eq!(status, StatusCode::UNSUPPORTED_MEDIA_TYPE);
}

#[tokio::test]
async fn healthz_answers_ok() {
    let response = app()
        .oneshot(
            Request::builder()
                .uri("/healthz")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
}

// --- PDFs: per-page conversion (ADR-0040) ----------------------------

// A PDF with no text on any page is refused with the class a future OCR
// stage routes on — never converted into an empty document that would
// silently feed the guide and the generated EPUB.
#[tokio::test]
async fn refuses_a_pdf_with_no_text_on_any_page() {
    let bytes = include_bytes!("fixtures/scanned.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "got: {body}");
    let json: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(json["class"], "scanned", "got: {body}");
    let msg = json["error"].as_str().unwrap();
    assert!(
        msg.contains("3 pages") && msg.contains("OCR"),
        "the sentence must carry the page count and name OCR, got: {msg}"
    );
}

// An image cover ahead of text pages is the ordinary book shape. The
// whole-document classifier used to call it ImageBased and refuse the
// book; it converts now, with the cover recorded as missing.
#[tokio::test]
async fn converts_an_image_cover_book_and_records_the_cover() {
    let bytes = include_bytes!("fixtures/image-cover.pdf");
    let (status, headers, body) = post_convert_full(bytes).await;
    assert_eq!(status, StatusCode::OK, "got: {body}");
    assert!(
        body.contains("clocks were striking thirteen"),
        "got: {body}"
    );
    assert_eq!(
        pages(&headers),
        (Some("10".to_string()), Some("1".to_string()))
    );
}

// A title page ahead of nine text-less pages converts what is there and
// says exactly what is not — the case ADR-0036 refused as "sparse",
// recorded instead of refused (ADR-0040 §2).
#[tokio::test]
async fn converts_a_mostly_textless_pdf_and_records_the_gaps() {
    let bytes = include_bytes!("fixtures/sparse.pdf");
    let (status, headers, body) = post_convert_full(bytes).await;
    assert_eq!(status, StatusCode::OK, "got: {body}");
    assert_eq!(
        pages(&headers),
        (Some("10".to_string()), Some("2-10".to_string()))
    );
}

// A text PDF converts with an empty missing-pages record: the header is
// present and empty, so "nothing missing" is distinguishable from "not
// recorded".
#[tokio::test]
async fn converts_a_text_pdf_with_nothing_missing() {
    let bytes = include_bytes!("fixtures/prose.pdf");
    let (status, headers, body) = post_convert_full(bytes).await;
    assert_eq!(status, StatusCode::OK, "got: {body}");
    assert!(
        body.contains("clocks were striking thirteen"),
        "got: {body}"
    );
    assert_eq!(
        pages(&headers),
        (Some("10".to_string()), Some(String::new()))
    );
}

// Top-level bookmarks become the chapter headings, at the page each one
// points to, so the EPUB stage splits on the author's chapters.
#[tokio::test]
async fn bookmarks_become_chapter_headings() {
    let bytes = include_bytes!("fixtures/outlined.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::OK, "got: {body}");
    let one = body.find("# Part One").expect("first bookmark heading");
    let two = body.find("# Part Two").expect("second bookmark heading");
    let page2 = body.find("second page").expect("page 2 text");
    let page3 = body.find("lights go out").expect("page 3 text");
    assert!(
        one < page2 && page2 < two && two < page3,
        "headings must sit ahead of the pages they point to, got: {body}"
    );
}
