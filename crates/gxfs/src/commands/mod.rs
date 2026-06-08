mod cat;
mod config;
mod docset;
mod edit;
mod find;
mod glob;
mod grep;
mod hook;
mod init;
mod locate;
mod ls;
mod mount;
mod repo;
mod rm;
mod search;
#[cfg(feature = "server")]
mod serve;
mod stat;
mod sync;
mod tree;
mod write;

use clap::Subcommand;

#[derive(Subcommand)]
pub enum Command {
    Ls(ls::Args),
    Tree(tree::Args),
    Cat(cat::Args),
    Grep(grep::Args),
    Find(find::Args),
    Stat(stat::Args),
    Write(write::Args),
    Edit(edit::Args),
    Rm(rm::Args),
    Search(search::Args),
    Locate(locate::Args),
    Glob(glob::Args),
    Init(init::Args),
    Config(config::Args),
    Sync(sync::Args),
    Mount(mount::Args),
    Repo(repo::Args),
    Docset(docset::Args),
    Hook(hook::Args),
    #[cfg(feature = "server")]
    Serve(serve::Args),
}

pub async fn run(cmd: Command) -> gxfs_core::error::Result<()> {
    match cmd {
        Command::Ls(args) => ls::run(args).await,
        Command::Tree(args) => tree::run(args).await,
        Command::Cat(args) => cat::run(args).await,
        Command::Grep(args) => grep::run(args).await,
        Command::Find(args) => find::run(args).await,
        Command::Stat(args) => stat::run(args).await,
        Command::Write(args) => write::run(args).await,
        Command::Edit(args) => edit::run(args).await,
        Command::Rm(args) => rm::run(args).await,
        Command::Search(args) => search::run(args).await,
        Command::Locate(args) => locate::run(args).await,
        Command::Glob(args) => glob::run(args).await,
        Command::Init(args) => init::run(args).await,
        Command::Config(args) => config::run(args).await,
        Command::Sync(args) => sync::run(args).await,
        Command::Mount(args) => mount::run(args).await,
        Command::Repo(args) => repo::run(args).await,
        Command::Docset(args) => docset::run(args).await,
        Command::Hook(args) => hook::run(args).await,
        #[cfg(feature = "server")]
        Command::Serve(args) => serve::run(args).await,
    }
}
