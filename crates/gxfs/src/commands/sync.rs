#[derive(clap::Args)]
pub struct Args {
    #[command(subcommand)]
    pub command: SyncCommand,
}

#[derive(clap::Subcommand)]
pub enum SyncCommand {
    Push,
    Pull,
    Refresh,
    Materialize,
    Dematerialize,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
