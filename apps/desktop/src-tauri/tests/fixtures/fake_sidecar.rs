use std::io::{self, BufRead, Write};
fn field(s: &str, key: &str) -> String {
    s.split(&format!("\"{}\":\"", key)).nth(1).unwrap_or("").split('"').next().unwrap_or("").into()
}
fn reply(id: &str, result: &str) {
    println!("{{\"v\":1,\"id\":\"{id}\",\"ok\":true,\"result\":{result}}}");
    io::stdout().flush().unwrap();
}
fn main() {
    let mut held = String::new();
    let mut n = 0;
    for line in io::stdin().lock().lines() {
        let line = line.unwrap();
        let id = field(&line, "id");
        let op = field(&line, "op");
        match op.as_str() {
            "slow" => held = id,
            "fast" => { reply(&id, "{\"name\":\"fast\"}"); reply(&held, "{\"name\":\"slow\"}"); }
            "timeout" => {},
            "crash" => std::process::exit(7),
            "subscribe" => {
                n += 1;
                let sub = format!("wire-{}-{n}", std::process::id());
                reply(&id, &format!("{{\"sub\":\"{sub}\"}}"));
                println!("{{\"v\":1,\"sub\":\"{sub}\",\"event\":\"state\",\"data\":{{\"instance_id\":\"bridge\",\"state\":\"running\"}}}}");
                io::stdout().flush().unwrap();
            }
            "unsubscribe" => reply(&id, "{}"),
            _ => reply(&id, "{\"engine_version\":\"fake\",\"contract_version\":1}"),
        }
    }
}
