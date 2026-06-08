#[derive(clap::Args)]
pub struct Args {
    pub path: String,
    pub content: Option<String>,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
