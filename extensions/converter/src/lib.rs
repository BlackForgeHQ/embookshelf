//! embookshelf converter extension (ADR-0033).
//!
//! Bytes in, GitHub-Flavored Markdown out. The wire contract is fixed by the
//! ADR: `POST /convert` takes raw file bytes and answers a raw `text/markdown`
//! body with the converter version in `X-Converter-Version`; errors are JSON
//! `{"error": "..."}` — 415 when the format cannot be detected or converted at
//! all, 422 when a detected document is structurally unusable.

use anydoc::{ConvertError, Format};
use axum::body::Bytes;
use axum::extract::DefaultBodyLimit;
use axum::http::{header, HeaderMap, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::Deserialize;

mod epub;
mod pdf;

pub const VERSION: &str = env!("CARGO_PKG_VERSION");

/// Books are tens of megabytes; axum's 2 MB default would reject them.
const MAX_BODY_BYTES: usize = 256 * 1024 * 1024;

pub fn app() -> Router {
    Router::new()
        .route("/convert", post(convert))
        .route("/render/epub", post(render_epub))
        .route("/healthz", get(healthz))
        .layer(DefaultBodyLimit::max(MAX_BODY_BYTES))
}

/// The second stage ADR-0033 deferred (ADR-0034 §3): markdown in,
/// EPUB3 out. JSON rather than raw bytes because the OPF needs
/// metadata, and headers mangle non-ASCII titles.
#[derive(Deserialize)]
struct RenderEpubRequest {
    markdown: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    author: String,
    #[serde(default)]
    language: String,
}

async fn render_epub(Json(req): Json<RenderEpubRequest>) -> Response {
    let request = epub::EpubRequest {
        markdown: req.markdown,
        title: req.title,
        author: req.author,
        language: req.language,
    };
    // CPU work like /convert, and off the async workers for the same
    // reason.
    let result = tokio::task::spawn_blocking(move || epub::render(&request)).await;
    match result {
        Ok(Ok(bytes)) => {
            let mut headers = HeaderMap::new();
            headers.insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/epub+zip"),
            );
            headers.insert("x-converter-version", HeaderValue::from_static(VERSION));
            (StatusCode::OK, headers, bytes).into_response()
        }
        Ok(Err(msg)) => error_response(StatusCode::UNPROCESSABLE_ENTITY, &msg),
        Err(join_err) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            &format!("render task failed: {join_err}"),
        ),
    }
}

async fn healthz() -> &'static str {
    "ok"
}

async fn convert(body: Bytes) -> Response {
    let Some(format) = Format::from_bytes(&body) else {
        return error_response(
            StatusCode::UNSUPPORTED_MEDIA_TYPE,
            "unrecognized file content: no supported format signature detected",
        );
    };

    // anydoc (and the PDF gate's inspection) is synchronous CPU work;
    // keep it off the async workers.
    let result = tokio::task::spawn_blocking(move || convert_bytes(&body, format)).await;

    match result {
        Ok(Ok(markdown)) => markdown_response(markdown),
        Ok(Err(Reject::Anydoc(err))) => convert_error_response(err),
        Ok(Err(Reject::Pdf(refusal))) => refusal_response(&refusal),
        Err(join_err) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            &format!("conversion task failed: {join_err}"),
        ),
    }
}

/// Reject separates the two refusal vocabularies: anydoc's own errors
/// keep their historical status mapping and plain body, the PDF gate's
/// verdicts carry a machine-readable class (ADR-0036).
enum Reject {
    Anydoc(ConvertError),
    Pdf(pdf::Refusal),
}

/// convert_bytes is the whole conversion pass: the PDF gate ahead of
/// anydoc, anydoc, the sparse-output gate behind it (PDF only). The
/// detection is done once and shared by both gate halves.
fn convert_bytes(body: &[u8], format: Format) -> Result<String, Reject> {
    let detection = match format {
        Format::Pdf => pdf::detect(body),
        _ => None,
    };
    if let Some(d) = &detection {
        if let Some(refusal) = pdf::refuse_before(d) {
            return Err(Reject::Pdf(refusal));
        }
    }
    let markdown = anydoc::to_markdown_bytes(body, format).map_err(Reject::Anydoc)?;
    if let Some(d) = &detection {
        if let Some(refusal) = pdf::refuse_sparse(d, &markdown) {
            return Err(Reject::Pdf(refusal));
        }
    }
    Ok(markdown)
}

/// A gate refusal is always 422: the format is supported and detected —
/// the *document* cannot give what conversion needs (ADR-0033 §4's
/// split). The class rides beside the error so a caller can route on it
/// without parsing prose (ADR-0036 §3).
fn refusal_response(refusal: &pdf::Refusal) -> Response {
    let body = serde_json::json!({ "error": refusal.message, "class": refusal.class }).to_string();
    let mut headers = HeaderMap::new();
    headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    (StatusCode::UNPROCESSABLE_ENTITY, headers, body).into_response()
}

fn markdown_response(markdown: String) -> Response {
    let mut headers = HeaderMap::new();
    headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/markdown; charset=utf-8"),
    );
    headers.insert("x-converter-version", HeaderValue::from_static(VERSION));
    (StatusCode::OK, headers, markdown).into_response()
}

fn convert_error_response(err: ConvertError) -> Response {
    let status = match err {
        ConvertError::Unsupported(_) => StatusCode::UNSUPPORTED_MEDIA_TYPE,
        _ => StatusCode::UNPROCESSABLE_ENTITY,
    };
    error_response(status, &err.to_string())
}

fn error_response(status: StatusCode, message: &str) -> Response {
    let body = serde_json::json!({ "error": message }).to_string();
    let mut headers = HeaderMap::new();
    headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    (status, headers, body).into_response()
}
