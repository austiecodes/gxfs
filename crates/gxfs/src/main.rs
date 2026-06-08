mod commands;

use clap::Parser;

#[derive(Parser)]
#[command(name = "gxfs", version, about = "A Unix-style virtual filesystem for shared docs")]
struct Cli {
    #[command(subcommand)]
    command: commands::Command,
}

#[tokio::main]
async fn main() {
    let cli = Cli::parse();
    if let Err(e) = commands::run(cli.command).await {
        eprintln!("error: {e}");
        std::process::exit(1);
    }
}
