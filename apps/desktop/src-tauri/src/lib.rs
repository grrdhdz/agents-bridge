mod proxy;
use proxy::Proxy;
use serde_json::Value;
use std::{collections::HashMap, sync::Mutex, time::Duration};
use tauri::{Emitter, Manager};

#[derive(Default)]
struct Windows(Mutex<HashMap<String, Proxy>>);

async fn request(
    window: tauri::WebviewWindow,
    state: tauri::State<'_, Windows>,
    op: &'static str,
    args: Option<Value>,
) -> Result<Value, String> {
    let proxy = {
        let mut windows = state.0.lock().unwrap();
        let label = window.label().to_string();
        windows
            .entry(label.clone())
            .or_insert_with(|| {
                let exe = std::env::current_exe().expect("No se encontró el ejecutable de la app");
                let sidecar = exe.parent().unwrap().join(if cfg!(windows) {
                    "agents-bridge.exe"
                } else {
                    "agents-bridge"
                });
                let app = window.app_handle().clone();
                Proxy::start(sidecar, vec!["api".into()], move |packet| {
                    let name = if packet.get("status").is_some() {
                        "agents-bridge-status"
                    } else {
                        "agents-bridge-event"
                    };
                    let _ = app.emit_to(&label, name, packet);
                })
            })
            .clone()
    };
    let timeout = if op == "hello" { 5 } else { 30 };
    let result = tauri::async_runtime::spawn_blocking(move || {
        proxy.request(
            op,
            args.unwrap_or_else(|| serde_json::json!({})),
            Duration::from_secs(timeout),
        )
    })
    .await
    .map_err(|_| "El canal del motor se cerró")??;
    if op == "hello" {
        if result["contract_version"] != 1 || result["engine_version"].as_str().is_none() {
            return Err("Versión de contrato no compatible".into());
        }
        eprintln!(
            "Motor conectado: {} · API v1",
            result["engine_version"].as_str().unwrap()
        );
    }
    if op == "integration_ensure" {
        eprintln!("Integración de hooks comprobada");
    }
    Ok(result)
}
macro_rules! operation {
    ($name:ident, $op:literal) => {
        #[tauri::command]
        async fn $name(
            window: tauri::WebviewWindow,
            state: tauri::State<'_, Windows>,
            args: Option<Value>,
        ) -> Result<Value, String> {
            request(window, state, $op, args).await
        }
    };
}
operation!(engine_hello, "hello");
operation!(engine_integration_status, "integration_status");
operation!(engine_integration_ensure, "integration_ensure");
operation!(engine_integration_set, "integration_set");
operation!(engine_list, "list");
operation!(engine_subscribe, "subscribe");
operation!(engine_unsubscribe, "unsubscribe");
operation!(engine_send, "send");
operation!(engine_stop, "stop");
operation!(engine_create_local, "create_local");
operation!(engine_health, "health");
operation!(engine_export, "export");

pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .manage(Windows::default())
        .invoke_handler(tauri::generate_handler![
            engine_hello,
            engine_integration_status,
            engine_integration_ensure,
            engine_integration_set,
            engine_list,
            engine_subscribe,
            engine_unsubscribe,
            engine_send,
            engine_stop,
            engine_create_local,
            engine_health,
            engine_export
        ])
        .on_window_event(|window, event| {
            if matches!(event, tauri::WindowEvent::Destroyed) {
                if let Some(proxy) = window
                    .state::<Windows>()
                    .0
                    .lock()
                    .unwrap()
                    .remove(window.label())
                {
                    proxy.shutdown();
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("No se pudo iniciar agents-bridge");
    app.run(|handle, event| {
        if matches!(event, tauri::RunEvent::Exit) {
            for (_, proxy) in handle.state::<Windows>().0.lock().unwrap().drain() {
                proxy.shutdown();
            }
        }
    });
}
