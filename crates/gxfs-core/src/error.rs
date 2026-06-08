use thiserror::Error;

#[derive(Error, Debug)]
pub enum Error {
    #[error("not found")]
    NotFound,

    #[error("is a directory")]
    IsDir,

    #[error("not a directory")]
    NotDir,

    #[error("content not ready")]
    ContentNotReady,

    #[error("empty old value")]
    EmptyOld,

    #[error("old value not found")]
    OldNotFound,

    #[error("read-only mount")]
    ReadOnlyMount,

    #[error("cannot delete root")]
    CannotDeleteRoot,

    #[error("unknown repo")]
    UnknownRepo,

    #[error("repo already exists")]
    RepoExists,

    #[error("unknown source")]
    UnknownSource,

    #[error("empty query")]
    EmptyQuery,

    #[error("invalid parameter")]
    InvalidParam,

    #[error("not modified")]
    NotModified,

    #[error("conflict")]
    Conflict,

    #[error("not supported")]
    NotSupported,

    #[error("invalid name")]
    InvalidName,

    #[error("docset name exists")]
    DocsetNameExists,

    #[error("docset not found")]
    DocsetNotFound,

    #[error("docset member exists")]
    DocsetMemberExists,

    #[error("document already in docset")]
    DocAlreadyInDocset,

    #[error("config error: {0}")]
    Config(String),

    #[error("io error: {0}")]
    Io(#[from] std::io::Error),

    #[error("http error: {0}")]
    Http(#[from] reqwest::Error),

    #[error("{0}")]
    Other(String),
}

pub type Result<T> = std::result::Result<T, Error>;
