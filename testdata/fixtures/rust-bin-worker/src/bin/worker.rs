use std::time::Duration;

fn main() {
    let token = std::env::var("DISCORD_TOKEN").expect("DISCORD_TOKEN is required");
    println!("bot starting ({} token chars)", token.len());
    loop {
        println!("tick");
        std::thread::sleep(Duration::from_secs(5));
    }
}
