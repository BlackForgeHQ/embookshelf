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
