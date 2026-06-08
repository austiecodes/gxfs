#[derive(clap::Args)]
pub struct Args {
    #[arg(long, default_value = "conf/server.toml")]
    pub config: String,
}

pub async fn run(_args: Args) -> gxfs_core::error::Result<()> {
    todo!()
}
