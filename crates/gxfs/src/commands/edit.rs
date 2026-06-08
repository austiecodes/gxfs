#[derive(clap::Args)]
pub struct Args {
    pub path: String,
    #[arg(long)]
    pub old: String,
    #[arg(long)]
    pub new: String,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
