#[derive(clap::Args)]
pub struct Args {
    #[command(subcommand)]
    pub command: DocsetCommand,
}

#[derive(clap::Subcommand)]
pub enum DocsetCommand {
    Create,
    List,
    Show,
    Add,
    Rm,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
