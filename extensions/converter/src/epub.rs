//! Markdown → EPUB3 (ADR-0034 §3).
//!
//! An EPUB is a zip of XHTML plus an OPF manifest; pulldown-cmark
//! renders the markdown and this module hand-builds the container —
//! the "heavy dependency" ADR-0033 feared for this stage turned out to
//! be unnecessary. Chapters split on H1 headings (anydoc emits them
//! from PDF structure), with a single-chapter fallback for headingless
//! text.

use std::io::Write;

use pulldown_cmark::{html, Options, Parser};
use zip::write::SimpleFileOptions;
use zip::{CompressionMethod, ZipWriter};

const CONTAINER_XML: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
"#;

pub struct EpubRequest {
    pub markdown: String,
    pub title: String,
    pub author: String,
    pub language: String,
}

/// Render a complete EPUB3 container. Deterministic for a given input —
/// no timestamps, no random identifiers — so regeneration of unchanged
/// markdown produces identical bytes.
pub fn render(req: &EpubRequest) -> Result<Vec<u8>, String> {
    if req.markdown.trim().is_empty() {
        return Err("markdown is empty: nothing to render".to_string());
    }
    let title = non_empty(&req.title, "Untitled");
    let language = non_empty(&req.language, "en");

    let chapters = split_chapters(&req.markdown);

    let mut zip = ZipWriter::new(std::io::Cursor::new(Vec::new()));
    let stored = SimpleFileOptions::default().compression_method(CompressionMethod::Stored);
    let deflated = SimpleFileOptions::default().compression_method(CompressionMethod::Deflated);

    // The OCF spec: `mimetype` first, uncompressed, no extra field.
    zip.start_file("mimetype", stored).map_err(zip_err)?;
    zip.write_all(b"application/epub+zip").map_err(io_err)?;

    zip.start_file("META-INF/container.xml", deflated)
        .map_err(zip_err)?;
    zip.write_all(CONTAINER_XML.as_bytes()).map_err(io_err)?;

    zip.start_file("OEBPS/content.opf", deflated)
        .map_err(zip_err)?;
    zip.write_all(opf(title, &req.author, language, chapters.len()).as_bytes())
        .map_err(io_err)?;

    zip.start_file("OEBPS/nav.xhtml", deflated)
        .map_err(zip_err)?;
    zip.write_all(nav(title, &chapters).as_bytes())
        .map_err(io_err)?;

    for (i, ch) in chapters.iter().enumerate() {
        zip.start_file(format!("OEBPS/chapter-{}.xhtml", i + 1), deflated)
            .map_err(zip_err)?;
        zip.write_all(chapter_xhtml(&ch.title, &ch.markdown).as_bytes())
            .map_err(io_err)?;
    }

    let cursor = zip.finish().map_err(zip_err)?;
    Ok(cursor.into_inner())
}

struct Chapter {
    title: String,
    markdown: String,
}

/// Split on top-level `# ` headings, keeping each heading with its body.
/// Fenced code blocks are respected — a `# ` inside one is code, not a
/// chapter boundary.
fn split_chapters(markdown: &str) -> Vec<Chapter> {
    let mut chapters: Vec<Chapter> = Vec::new();
    let mut current = String::new();
    let mut current_title = String::new();
    let mut in_fence = false;

    for line in markdown.lines() {
        let trimmed = line.trim_start();
        if trimmed.starts_with("```") || trimmed.starts_with("~~~") {
            in_fence = !in_fence;
        }
        if !in_fence && line.starts_with("# ") {
            if !current.trim().is_empty() {
                chapters.push(Chapter {
                    title: current_title.clone(),
                    markdown: current.clone(),
                });
            }
            current_title = line[2..].trim().to_string();
            current = String::new();
        }
        current.push_str(line);
        current.push('\n');
    }
    if !current.trim().is_empty() {
        chapters.push(Chapter {
            title: current_title,
            markdown: current,
        });
    }
    if chapters.is_empty() {
        chapters.push(Chapter {
            title: String::new(),
            markdown: markdown.to_string(),
        });
    }
    chapters
}

fn chapter_xhtml(title: &str, markdown: &str) -> String {
    let parser = Parser::new_ext(
        markdown,
        Options::ENABLE_TABLES | Options::ENABLE_STRIKETHROUGH,
    );
    let mut body = String::new();
    html::push_html(&mut body, parser);
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>{}</title></head>
<body>
{}
</body>
</html>
"#,
        escape(title),
        xhtmlize(&body)
    )
}

/// pulldown-cmark emits HTML5; EPUB wants XHTML. The three void
/// elements it actually produces are self-closed here — a full
/// serializer for three tags would be the speculative generality.
fn xhtmlize(html: &str) -> String {
    html.replace("<br>", "<br/>")
        .replace("<hr>", "<hr/>")
        .replace("<hr >", "<hr/>")
}

fn opf(title: &str, author: &str, language: &str, chapter_count: usize) -> String {
    let mut manifest = String::from(
        r#"    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
"#,
    );
    let mut spine = String::new();
    for i in 1..=chapter_count {
        manifest.push_str(&format!(
            "    <item id=\"chapter-{i}\" href=\"chapter-{i}.xhtml\" media-type=\"application/xhtml+xml\"/>\n"
        ));
        spine.push_str(&format!("    <itemref idref=\"chapter-{i}\"/>\n"));
    }
    let creator = if author.trim().is_empty() {
        String::new()
    } else {
        format!("    <dc:creator>{}</dc:creator>\n", escape(author))
    };
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:embookshelf:generated:{}</dc:identifier>
    <dc:title>{}</dc:title>
{}    <dc:language>{}</dc:language>
    <meta property="dcterms:modified">1970-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
{}  </manifest>
  <spine>
{}  </spine>
</package>
"#,
        escape(title),
        escape(title),
        creator,
        escape(language),
        manifest,
        spine
    )
}

fn nav(title: &str, chapters: &[Chapter]) -> String {
    let mut items = String::new();
    for (i, ch) in chapters.iter().enumerate() {
        let label = if ch.title.is_empty() {
            format!("Chapter {}", i + 1)
        } else {
            ch.title.clone()
        };
        items.push_str(&format!(
            "      <li><a href=\"chapter-{}.xhtml\">{}</a></li>\n",
            i + 1,
            escape(&label)
        ));
    }
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>{}</title></head>
<body>
  <nav epub:type="toc">
    <ol>
{}    </ol>
  </nav>
</body>
</html>
"#,
        escape(title),
        items
    )
}

fn non_empty<'a>(s: &'a str, fallback: &'a str) -> &'a str {
    if s.trim().is_empty() {
        fallback
    } else {
        s.trim()
    }
}

fn escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}

fn zip_err(e: zip::result::ZipError) -> String {
    format!("build epub container: {e}")
}

fn io_err(e: std::io::Error) -> String {
    format!("write epub entry: {e}")
}
