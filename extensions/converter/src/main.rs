use embookshelf_converter::{app, VERSION};

#[tokio::main]
async fn main() {
    let addr = std::env::var("CONVERTER_ADDR").unwrap_or_else(|_| "0.0.0.0:6070".to_string());
    let listener = tokio::net::TcpListener::bind(&addr)
        .await
        .unwrap_or_else(|err| panic!("bind {addr}: {err}"));
    eprintln!("embookshelf-converter {VERSION} listening on {addr}");
    axum::serve(listener, app())
        .with_graceful_shutdown(async {
            let _ = tokio::signal::ctrl_c().await;
        })
        .await
        .expect("server error");
}
