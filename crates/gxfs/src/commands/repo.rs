#[derive(clap::Args)]
pub struct Args {
    #[command(subcommand)]
    pub command: RepoCommand,
}

#[derive(clap::Subcommand)]
pub enum RepoCommand {
    Ls,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
