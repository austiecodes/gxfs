#[derive(clap::Args)]
pub struct Args {
    pub path: Option<String>,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
