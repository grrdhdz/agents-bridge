use serde_json::{json, Value};
use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, Command, Stdio},
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc, Arc, Mutex,
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

type Reply = mpsc::Sender<Result<Value, String>>;
enum Input {
    Request {
        op: String,
        args: Value,
        deadline: Instant,
        reply: Reply,
    },
    Packet(u64, Value),
    Dead(u64),
    Shutdown,
}
struct Handle {
    tx: mpsc::Sender<Input>,
    stopped: AtomicBool,
    join: Mutex<Option<JoinHandle<()>>>,
}
impl Handle {
    fn close(&self) {
        if !self.stopped.swap(true, Ordering::SeqCst) {
            let _ = self.tx.send(Input::Shutdown);
            if let Some(join) = self.join.lock().unwrap().take() {
                let _ = join.join();
            }
        }
    }
}
impl Drop for Handle {
    fn drop(&mut self) {
        self.close();
    }
}
#[derive(Clone)]
pub struct Proxy(Arc<Handle>);
impl Proxy {
    pub fn start(path: PathBuf, args: Vec<String>, emit: impl Fn(Value) + Send + 'static) -> Self {
        let (tx, rx) = mpsc::channel();
        let source = tx.clone();
        let join = thread::spawn(move || actor(path, args, source, rx, emit));
        Self(Arc::new(Handle {
            tx,
            stopped: AtomicBool::new(false),
            join: Mutex::new(Some(join)),
        }))
    }
    pub fn request(&self, op: &str, args: Value, timeout: Duration) -> Result<Value, String> {
        if self.0.stopped.load(Ordering::SeqCst) {
            return Err("El motor está cerrado".into());
        }
        let (tx, rx) = mpsc::channel();
        self.0
            .tx
            .send(Input::Request {
                op: op.into(),
                args,
                deadline: Instant::now() + timeout,
                reply: tx,
            })
            .map_err(|_| "El motor está cerrado")?;
        rx.recv_timeout(timeout + Duration::from_millis(100))
            .unwrap_or_else(|_| Err("El motor no respondió a tiempo".into()))
    }
    pub fn shutdown(&self) {
        self.0.close();
    }
}
struct Process {
    child: Child,
    writer: mpsc::Sender<Vec<u8>>,
    threads: Vec<JoinHandle<()>>,
}
impl Process {
    fn launch(
        path: &PathBuf,
        args: &[String],
        gen: u64,
        tx: &mpsc::Sender<Input>,
    ) -> std::io::Result<Self> {
        let mut command = Command::new(path);
        command
            .args(args)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000);
        }
        let mut child = command.spawn()?;
        let mut stdin = child.stdin.take().unwrap();
        let stdout = child.stdout.take().unwrap();
        let (writer, writes) = mpsc::channel::<Vec<u8>>();
        let t = tx.clone();
        let write = thread::spawn(move || {
            while let Ok(bytes) = writes.recv() {
                if stdin.write_all(&bytes).is_err() {
                    let _ = t.send(Input::Dead(gen));
                    break;
                }
            }
        });
        let t = tx.clone();
        let read = thread::spawn(move || {
            let mut reader = BufReader::new(stdout);
            loop {
                let mut bytes = Vec::new();
                match reader.read_until(b'\n', &mut bytes) {
                    Ok(0) | Err(_) => break,
                    _ if bytes.len() > 2 * 1024 * 1024 => break,
                    _ => match serde_json::from_slice::<Value>(&bytes) {
                        Ok(packet) if packet["v"] == 1 => {
                            if t.send(Input::Packet(gen, packet)).is_err() {
                                break;
                            }
                        }
                        _ => break,
                    },
                }
            }
            let _ = t.send(Input::Dead(gen));
        });
        Ok(Self {
            child,
            writer,
            threads: vec![write, read],
        })
    }
    fn close(mut self) {
        drop(self.writer);
        // EOF primero; un proceso que no coopera se termina sin tocar puentes.
        for _ in 0..20 {
            if matches!(self.child.try_wait(), Ok(Some(_))) {
                break;
            }
            thread::sleep(Duration::from_millis(10));
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
        for thread in self.threads {
            let _ = thread.join();
        }
    }
}
enum Action {
    Normal,
    Unsubscribe(String),
    Subscribe(String, String),
    Restore(String),
}
struct Pending {
    reply: Option<Reply>,
    deadline: Instant,
    action: Action,
}
struct Subscription {
    instance: String,
    wire: Option<String>,
}
fn write_request(process: &Process, count: &mut u64, op: &str, args: Value) -> Option<String> {
    *count += 1;
    let id = format!("desktop-{count}");
    let mut bytes = serde_json::to_vec(&json!({"v":1,"id":id,"op":op,"args":args})).ok()?;
    bytes.push(b'\n');
    process.writer.send(bytes).ok()?;
    Some(id)
}
fn actor(
    path: PathBuf,
    args: Vec<String>,
    tx: mpsc::Sender<Input>,
    rx: mpsc::Receiver<Input>,
    emit: impl Fn(Value),
) {
    let mut process: Option<Process> = None;
    let mut gen = 0;
    let mut count = 0;
    let mut pending = HashMap::<String, Pending>::new();
    let mut subscriptions = HashMap::<String, Subscription>::new();
    let mut late_subs = HashMap::<String, Instant>::new();
    let mut retry = Instant::now();
    let mut backoff = Duration::from_millis(250);
    loop {
        if process.is_none() && Instant::now() >= retry {
            gen += 1;
            match Process::launch(&path, &args, gen, &tx) {
                Ok(child) => {
                    emit(json!({"status":"connected"}));
                    backoff = Duration::from_millis(250);
                    for (stable, sub) in &subscriptions {
                        if let Some(id) = write_request(
                            &child,
                            &mut count,
                            "subscribe",
                            json!({"instance_id":sub.instance}),
                        ) {
                            pending.insert(
                                id,
                                Pending {
                                    reply: None,
                                    deadline: Instant::now() + Duration::from_secs(10),
                                    action: Action::Restore(stable.clone()),
                                },
                            );
                        }
                    }
                    process = Some(child);
                }
                Err(_) => {
                    emit(
                        json!({"status":"restarting","message":"No se pudo iniciar el motor; reintentando"}),
                    );
                    retry = Instant::now() + backoff;
                    backoff = (backoff * 2).min(Duration::from_secs(5));
                }
            }
        }
        let now = Instant::now();
        let expired: Vec<_> = pending
            .iter()
            .filter(|(_, p)| now >= p.deadline)
            .map(|(id, _)| id.clone())
            .collect();
        for id in expired {
            if let Some(p) = pending.remove(&id) {
                if matches!(p.action, Action::Subscribe(..) | Action::Restore(..)) {
                    late_subs.insert(id, now + Duration::from_secs(30));
                }
                if let Some(reply) = p.reply {
                    let _ = reply.send(Err("El motor no respondió a tiempo".into()));
                } else {
                    emit(
                        json!({"status":"subscription-error","message":"No se pudo restaurar la suscripción a tiempo"}),
                    );
                }
            }
        }
        late_subs.retain(|_, until| *until > now);
        match rx.recv_timeout(Duration::from_millis(15)) {
            Ok(Input::Shutdown) | Err(mpsc::RecvTimeoutError::Disconnected) => break,
            Ok(Input::Request {
                op,
                mut args,
                deadline,
                reply,
            }) => {
                if Instant::now() >= deadline {
                    let _ = reply.send(Err("El motor no respondió a tiempo".into()));
                    continue;
                }
                if op == "unsubscribe" {
                    let stable = args["sub"].as_str().unwrap_or("").to_string();
                    let wire = subscriptions.remove(&stable).and_then(|s| s.wire);
                    if let (Some(child), Some(wire)) = (process.as_ref(), wire) {
                        args["sub"] = json!(wire);
                        if let Some(id) = write_request(child, &mut count, &op, args) {
                            pending.insert(
                                id,
                                Pending {
                                    reply: Some(reply),
                                    deadline,
                                    action: Action::Unsubscribe(stable),
                                },
                            );
                        } else {
                            let _ = reply.send(Err("El canal del motor se cerró".into()));
                        }
                    } else {
                        let _ = reply.send(Ok(json!({"unsubscribed":stable})));
                    }
                    continue;
                }
                let Some(ref child) = process else {
                    let _ = reply.send(Err("Motor reiniciándose; vuelve a intentarlo".into()));
                    continue;
                };
                let action = if op == "subscribe" {
                    Action::Subscribe(
                        format!("desktop-sub-{}", count + 1),
                        args["instance_id"].as_str().unwrap_or("").into(),
                    )
                } else {
                    Action::Normal
                };
                if let Some(id) = write_request(child, &mut count, &op, args) {
                    pending.insert(
                        id,
                        Pending {
                            reply: Some(reply),
                            deadline,
                            action,
                        },
                    );
                } else {
                    let _ = reply.send(Err("El canal del motor se cerró".into()));
                }
            }
            Ok(Input::Packet(generation, mut packet)) if generation == gen => {
                if let Some(wire) = packet["sub"].as_str() {
                    if let Some((stable, _)) = subscriptions
                        .iter()
                        .find(|(_, s)| s.wire.as_deref() == Some(wire))
                    {
                        packet["sub"] = json!(stable);
                        emit(packet);
                    }
                    continue;
                }
                let id = packet["id"].as_str().unwrap_or("").to_string();
                let Some(p) = pending.remove(&id) else {
                    if late_subs.remove(&id).is_some() && packet["ok"] == true {
                        if let Some(ref child) = process {
                            let _ = write_request(
                                child,
                                &mut count,
                                "unsubscribe",
                                json!({"sub":packet["result"]["sub"]}),
                            );
                        }
                    }
                    continue;
                };
                let result = if packet["ok"] == true {
                    Ok(packet["result"].clone())
                } else {
                    Err(format!(
                        "{}: {}",
                        packet["error"]["code"].as_str().unwrap_or("ERROR"),
                        packet["error"]["message"]
                            .as_str()
                            .unwrap_or("Error del motor")
                    ))
                };
                let result = match p.action {
                    Action::Subscribe(stable, instance) => result.and_then(|r| {
                        let wire = r["sub"].as_str().ok_or("Suscripción inválida")?.to_string();
                        subscriptions.insert(
                            stable.clone(),
                            Subscription {
                                instance,
                                wire: Some(wire),
                            },
                        );
                        Ok(json!({"sub":stable}))
                    }),
                    Action::Restore(stable) => result.and_then(|r| {
                        let wire = r["sub"].as_str().ok_or("Suscripción inválida")?.to_string();
                        if let Some(sub) = subscriptions.get_mut(&stable) {
                            sub.wire = Some(wire);
                        } else if let Some(ref child) = process {
                            let _ = write_request(
                                child,
                                &mut count,
                                "unsubscribe",
                                json!({"sub":wire}),
                            );
                        }
                        Ok(r)
                    }),
                    Action::Normal => result,
                    Action::Unsubscribe(stable) => result.map(|_| json!({"unsubscribed":stable})),
                };
                if let Some(reply) = p.reply {
                    let _ = reply.send(result);
                } else if result.is_err() {
                    emit(
                        json!({"status":"subscription-error","message":"No se pudo restaurar una suscripción"}),
                    );
                }
            }
            Ok(Input::Dead(generation)) if generation == gen && process.is_some() => {
                process.take().unwrap().close();
                for (_, p) in pending.drain() {
                    if let Some(reply) = p.reply {
                        let _ = reply.send(Err("Motor reiniciándose; vuelve a intentarlo".into()));
                    }
                }
                for sub in subscriptions.values_mut() {
                    sub.wire = None;
                }
                late_subs.clear();
                emit(
                    json!({"status":"restarting","message":"El motor se reinició; recuperando la conversación"}),
                );
                retry = Instant::now() + backoff;
            }
            _ => {}
        }
    }
    for (_, p) in pending {
        if let Some(reply) = p.reply {
            let _ = reply.send(Err("El motor está cerrado".into()));
        }
    }
    if let Some(child) = process {
        child.close();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::{fs, process::Command, sync::mpsc, time::Duration};
    fn fixture() -> (Proxy, mpsc::Receiver<serde_json::Value>, std::path::PathBuf) {
        let root = std::env::temp_dir().join(format!(
            "agents-bridge-proxy-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir(&root).unwrap();
        let exe = root.join(if cfg!(windows) { "fake.exe" } else { "fake" });
        assert!(Command::new("rustc")
            .args(["--edition=2021", "tests/fixtures/fake_sidecar.rs", "-o"])
            .arg(&exe)
            .status()
            .unwrap()
            .success());
        let (tx, rx) = mpsc::channel();
        (
            Proxy::start(exe, vec![], move |p| {
                let _ = tx.send(p);
            }),
            rx,
            root,
        )
    }
    #[test]
    fn correlates_out_of_order_and_times_out() {
        let (proxy, _, root) = fixture();
        let (reply, slow) = mpsc::channel();
        proxy
            .0
            .tx
            .send(Input::Request {
                op: "slow".into(),
                args: json!({}),
                deadline: Instant::now() + Duration::from_secs(2),
                reply,
            })
            .unwrap();
        assert_eq!(
            proxy
                .request("fast", json!({}), Duration::from_secs(2))
                .unwrap()["name"],
            "fast"
        );
        assert_eq!(
            slow.recv_timeout(Duration::from_secs(2)).unwrap().unwrap()["name"],
            "slow"
        );
        assert!(proxy
            .request("timeout", json!({}), Duration::from_millis(80))
            .unwrap_err()
            .contains("tiempo"));
        proxy.shutdown();
        fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn unsubscribe_during_restart_removes_the_intent() {
        let (proxy, rx, root) = fixture();
        let sub = proxy
            .request(
                "subscribe",
                json!({"instance_id":"bridge"}),
                Duration::from_secs(2),
            )
            .unwrap()["sub"]
            .as_str()
            .unwrap()
            .to_string();
        assert!(proxy
            .request("crash", json!({}), Duration::from_secs(2))
            .is_err());
        loop {
            let p = rx.recv_timeout(Duration::from_secs(2)).unwrap();
            if p["status"] == "restarting" {
                break;
            }
        }
        assert_eq!(
            proxy
                .request("unsubscribe", json!({"sub":sub}), Duration::from_secs(2))
                .unwrap()["unsubscribed"],
            sub
        );
        std::thread::sleep(Duration::from_millis(400));
        assert!(proxy
            .request("hello", json!({}), Duration::from_secs(2))
            .is_ok());
        while let Ok(p) = rx.try_recv() {
            assert_ne!(p["event"], "state", "cancelled subscription restored");
        }
        proxy.shutdown();
        fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn forwards_events_and_restores_stable_subscription_after_crash() {
        let (proxy, rx, root) = fixture();
        let sub = proxy
            .request(
                "subscribe",
                json!({"instance_id":"bridge"}),
                Duration::from_secs(2),
            )
            .unwrap()["sub"]
            .as_str()
            .unwrap()
            .to_string();
        let next_event = || loop {
            let p = rx.recv_timeout(Duration::from_secs(3)).unwrap();
            if p["event"] == "state" {
                break p;
            }
        };
        assert_eq!(next_event()["sub"], sub);
        assert!(proxy
            .request("crash", json!({}), Duration::from_secs(2))
            .is_err());
        assert_eq!(next_event()["sub"], sub);
        assert!(proxy
            .request("hello", json!({}), Duration::from_secs(2))
            .is_ok());
        proxy
            .request("unsubscribe", json!({"sub":sub}), Duration::from_secs(2))
            .unwrap();
        proxy.shutdown();
        assert!(proxy
            .request("hello", json!({}), Duration::from_secs(1))
            .is_err());
        fs::remove_dir_all(root).unwrap();
    }
}
