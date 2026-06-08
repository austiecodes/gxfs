#[derive(clap::Args)]
pub struct Args {
    #[command(subcommand)]
    pub command: Option<ConfigCommand>,
}

#[derive(clap::Subcommand)]
pub enum ConfigCommand {
    Doctor,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
