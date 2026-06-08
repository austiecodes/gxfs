#[derive(clap::Args)]
pub struct Args {
    #[command(subcommand)]
    pub command: MountCommand,
}

#[derive(clap::Subcommand)]
pub enum MountCommand {
    Add,
    Rm,
    Ls,
    Sources,
    Attach,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
