#[derive(clap::Args)]
pub struct Args {
    #[command(subcommand)]
    pub command: HookCommand,
}

#[derive(clap::Subcommand)]
pub enum HookCommand {
    SessionStart,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
