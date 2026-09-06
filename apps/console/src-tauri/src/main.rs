// Prevents an additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

// Matrix Console — Tauri desktop shell.
//
// The Rust side is intentionally thin: it hosts the WebView that loads the Vite
// dev server (in `tauri dev`) or the built `dist/` bundle (in `tauri build`),
// as configured in tauri.conf.json. All application logic lives in the
// React/TypeScript frontend, which talks to a local matrixd node over HTTP
// (Connect protocol). A single command is exposed so the frontend can query the
// shell version if it wants to surface it.

#[tauri::command]
fn console_version() -> String {
    env!("CARGO_PKG_VERSION").to_string()
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![console_version])
        .run(tauri::generate_context!())
        .expect("error while running Matrix Console");
}
