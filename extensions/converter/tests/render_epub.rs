use std::io::{Cursor, Read};

use axum::body::Body;
use axum::http::{Request, StatusCode};
use http_body_util::BodyExt;
use tower::util::ServiceExt;

use embookshelf_converter::app;

async fn post_render(body: serde_json::Value) -> (StatusCode, Vec<u8>, Option<String>) {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/render/epub")
                .header("content-type", "application/json")
                .body(Body::from(body.to_string()))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let ct = response
        .headers()
        .get("content-type")
        .map(|v| v.to_str().unwrap().to_string());
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (status, bytes.to_vec(), ct)
}

fn zip_names(bytes: &[u8]) -> Vec<String> {
    let mut archive = zip::ZipArchive::new(Cursor::new(bytes.to_vec())).expect("valid zip");
    (0..archive.len())
        .map(|i| archive.by_index(i).unwrap().name().to_string())
        .collect()
}

fn zip_entry(bytes: &[u8], name: &str) -> String {
    let mut archive = zip::ZipArchive::new(Cursor::new(bytes.to_vec())).expect("valid zip");
    let mut f = archive.by_name(name).expect(name);
    let mut s = String::new();
    f.read_to_string(&mut s).unwrap();
    s
}

#[tokio::test]
async fn renders_a_valid_epub_container() {
    let (status, bytes, ct) = post_render(serde_json::json!({
        "markdown": "# One\n\nfirst chapter text\n\n# Two\n\nsecond chapter text\n",
        "title": "Dune",
        "author": "Frank Herbert",
        "language": "en",
    }))
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(ct.as_deref(), Some("application/epub+zip"));

    let names = zip_names(&bytes);
    assert_eq!(names[0], "mimetype", "mimetype must be the first entry");
    assert!(names.iter().any(|n| n == "META-INF/container.xml"));
    assert!(names.iter().any(|n| n == "OEBPS/content.opf"));
    assert!(names.iter().any(|n| n == "OEBPS/nav.xhtml"));

    // mimetype must be stored uncompressed per the EPUB OCF spec.
    let mut archive = zip::ZipArchive::new(Cursor::new(bytes.clone())).unwrap();
    let mimetype = archive.by_index(0).unwrap();
    assert_eq!(mimetype.compression(), zip::CompressionMethod::Stored);
    drop(mimetype);

    assert_eq!(zip_entry(&bytes, "mimetype"), "application/epub+zip");
    let opf = zip_entry(&bytes, "OEBPS/content.opf");
    assert!(opf.contains("Dune"), "OPF carries the title");
    assert!(opf.contains("Frank Herbert"), "OPF carries the author");

    // H1s split chapters: two headings, two chapter documents.
    let chapters: Vec<_> = zip_names(&bytes)
        .into_iter()
        .filter(|n| n.starts_with("OEBPS/chapter-"))
        .collect();
    assert_eq!(chapters.len(), 2, "chapters = {chapters:?}");
    assert!(zip_entry(&bytes, "OEBPS/chapter-1.xhtml").contains("first chapter text"));
    assert!(zip_entry(&bytes, "OEBPS/chapter-2.xhtml").contains("second chapter text"));
}

#[tokio::test]
async fn headingless_markdown_becomes_one_chapter() {
    let (status, bytes, _) = post_render(serde_json::json!({
        "markdown": "just prose, no headings at all",
        "title": "T",
        "author": "",
        "language": "en",
    }))
    .await;
    assert_eq!(status, StatusCode::OK);
    let chapters: Vec<_> = zip_names(&bytes)
        .into_iter()
        .filter(|n| n.starts_with("OEBPS/chapter-"))
        .collect();
    assert_eq!(chapters.len(), 1);
}

#[tokio::test]
async fn version_header_travels() {
    let (_, _, _) = post_render(serde_json::json!({
        "markdown": "x", "title": "T", "author": "", "language": "en",
    }))
    .await;
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/render/epub")
                .header("content-type", "application/json")
                .body(Body::from(
                    serde_json::json!({"markdown":"x","title":"T","author":"","language":"en"})
                        .to_string(),
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(
        response
            .headers()
            .get("x-converter-version")
            .map(|v| v.to_str().unwrap()),
        Some(env!("CARGO_PKG_VERSION"))
    );
}

#[tokio::test]
async fn empty_markdown_answers_422_json() {
    let (status, bytes, _) = post_render(serde_json::json!({
        "markdown": "", "title": "T", "author": "", "language": "en",
    }))
    .await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    let parsed: serde_json::Value = serde_json::from_slice(&bytes).expect("JSON error body");
    assert!(parsed["error"].is_string());
}

#[tokio::test]
async fn malformed_json_answers_400() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/render/epub")
                .header("content-type", "application/json")
                .body(Body::from("not json"))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
}
