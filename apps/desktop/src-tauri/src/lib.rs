use serde::Serialize;
use serde_json::Value;
use std::time::Duration;
use tauri_plugin_shell::{
    process::{CommandChild, CommandEvent},
    ShellExt,
};

#[derive(Debug, Serialize, PartialEq)]
struct HelloResult {
    engine_version: String,
    contract_version: u8,
}

fn decode_hello(line: &[u8]) -> Result<HelloResult, String> {
    let packet: Value = serde_json::from_slice(line).map_err(|_| "Respuesta inválida del motor")?;
    if packet["v"] != 1 || packet["id"] != "desktop-hello" || packet["ok"] != true {
        return Err("El motor no confirmó el contrato v1".into());
    }
    let result = &packet["result"];
    let version = result["engine_version"]
        .as_str()
        .filter(|s| !s.is_empty())
        .ok_or("El motor no informó su versión")?;
    if result["contract_version"] != 1 {
        return Err("Versión de contrato no compatible".into());
    }
    Ok(HelloResult {
        engine_version: version.into(),
        contract_version: 1,
    })
}

struct ChildGuard(Option<CommandChild>);
impl Drop for ChildGuard {
    fn drop(&mut self) {
        if let Some(child) = self.0.take() {
            let _ = child.kill();
        }
    }
}

#[tauri::command]
async fn engine_hello(app: tauri::AppHandle) -> Result<HelloResult, String> {
    let command = app
        .shell()
        .sidecar("agents-bridge")
        .map_err(|_| "No se encontró el motor incluido en la app")?
        .args(["api"]);
    let (mut events, child) = command.spawn().map_err(|_| "No se pudo iniciar el motor")?;
    let mut guard = ChildGuard(Some(child));
    guard
        .0
        .as_mut()
        .unwrap()
        .write(b"{\"v\":1,\"id\":\"desktop-hello\",\"op\":\"hello\",\"args\":{}}\n")
        .map_err(|_| "No se pudo contactar con el motor")?;
    let response = tokio::time::timeout(Duration::from_secs(5), async {
        while let Some(event) = events.recv().await {
            match event {
                CommandEvent::Stdout(line) => return decode_hello(&line),
                CommandEvent::Terminated(_) | CommandEvent::Error(_) => {
                    return Err("El motor terminó antes de responder".into());
                }
                _ => {}
            }
        }
        Err("El canal del motor se cerró".into())
    })
    .await
    .map_err(|_| "El motor no respondió a tiempo")?;
    if let Ok(ref hello) = response {
        eprintln!(
            "Motor conectado: {} · API v{}",
            hello.engine_version, hello.contract_version
        );
    }
    // G0 comprueba el handshake; G2 mantendrá el proxy de la sesión.
    response
}

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![engine_hello])
        .run(tauri::generate_context!())
        .expect("No se pudo iniciar agents-bridge");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hello_accepts_only_matching_v1_success() {
        let result = decode_hello(br#"{"v":1,"id":"desktop-hello","ok":true,"result":{"engine_version":"v0.4.0","contract_version":1}}"#).unwrap();
        assert_eq!(
            result,
            HelloResult {
                engine_version: "v0.4.0".into(),
                contract_version: 1
            }
        );
        for line in [
            br#"{"v":2}"#.as_slice(),
            br#"{"v":1,"id":"other","ok":true}"#,
            b"invalid",
            br#"{"v":1,"id":"desktop-hello","ok":false,"error":{"message":"private"}}"#,
        ] {
            let error = decode_hello(line).unwrap_err();
            assert!(!error.contains("private"));
        }
    }
}
