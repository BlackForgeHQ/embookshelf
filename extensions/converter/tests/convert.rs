use axum::body::Body;
use axum::http::{Request, StatusCode};
use http_body_util::BodyExt;
use tower::util::ServiceExt;

use embookshelf_converter::app;

async fn post_convert(bytes: &[u8]) -> (StatusCode, Option<String>, String) {
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
    let version = response
        .headers()
        .get("x-converter-version")
        .map(|v| v.to_str().unwrap().to_string());
    let body = response.into_body().collect().await.unwrap().to_bytes();
    (status, version, String::from_utf8_lossy(&body).into_owned())
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
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::OK);
    assert!(
        body.contains("Hello converter fixture from DOCX"),
        "got: {body}"
    );
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

// --- the PDF gate (ADR-0036) -----------------------------------------

// A scanned PDF is refused before conversion, with the class a future
// OCR stage routes on and the per-page evidence in the sentence — never
// converted into an empty document that silently feeds the guide and
// the generated EPUB.
#[tokio::test]
async fn refuses_a_scanned_pdf_with_class_and_evidence() {
    let bytes = include_bytes!("fixtures/scanned.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "got: {body}");
    let json: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(json["class"], "scanned", "got: {body}");
    let msg = json["error"].as_str().unwrap();
    assert!(
        msg.contains("3 of 3 pages") && msg.contains("OCR"),
        "the sentence must carry the page evidence and name OCR, got: {msg}"
    );
}

// The sparse-output gate: a typed title page ahead of nine no-text
// pages reads TextBased to the sampling classifier, and used to convert
// to a few bytes of markdown — the silent-degrade case the gate exists
// for. The output is the proof, whatever the classifier said.
#[tokio::test]
async fn refuses_a_pdf_whose_conversion_is_effectively_empty() {
    let bytes = include_bytes!("fixtures/sparse.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "got: {body}");
    let json: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(json["class"], "sparse", "got: {body}");
    assert!(
        json["error"].as_str().unwrap().contains("10 pages"),
        "got: {body}"
    );
}

// An ImageBased verdict is refused with its class. The engine refuses
// these bytes on every version — the shipped 0.1.7 answered the same
// file "no extractable text … OCR is required" as a prose-only 415 —
// so the gate's contribution here is the class, the evidence and one
// consistent status, never a rejection anydoc would not have made.
#[tokio::test]
async fn refuses_an_image_based_pdf_with_class_and_evidence() {
    let bytes = include_bytes!("fixtures/image-cover.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "got: {body}");
    let json: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(json["class"], "image", "got: {body}");
    assert!(
        json["error"].as_str().unwrap().contains("10 pages"),
        "got: {body}"
    );
}

// A text PDF of ordinary prose density sails through both gate halves.
#[tokio::test]
async fn converts_a_multipage_text_pdf_through_the_gate() {
    let bytes = include_bytes!("fixtures/prose.pdf");
    let (status, _, body) = post_convert(bytes).await;
    assert_eq!(status, StatusCode::OK, "got: {body}");
    assert!(
        body.contains("clocks were striking thirteen"),
        "got: {body}"
    );
}
