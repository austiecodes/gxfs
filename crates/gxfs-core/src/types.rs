use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum SourceKind {
    Repo,
    Docs,
    Docset,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SourceRef {
    pub kind: SourceKind,
    pub name: String,
    pub path: String,
}

impl std::fmt::Display for SourceRef {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let kind = match &self.kind {
            SourceKind::Repo => "repo",
            SourceKind::Docs => "docs",
            SourceKind::Docset => "docset",
        };
        write!(f, "{kind}://{}/{}", self.name, self.path)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Node {
    pub path: String,
    pub name: String,
    pub kind: NodeKind,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub size: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mod_time: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hash: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum NodeKind {
    Dir,
    File,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Match {
    pub path: String,
    pub line: usize,
    pub text: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub before: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub after: Option<Vec<String>>,
}

// Request / Response types

#[derive(Debug, Serialize, Deserialize)]
pub struct LsRequest { pub path: String, #[serde(flatten)] pub opts: LsOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct LsOpts {
    pub sort: Option<String>, pub reverse: bool, pub recursive: bool,
    pub all: bool, pub offset: Option<usize>, pub limit: Option<usize>,
}
#[derive(Debug, Serialize, Deserialize)]
pub struct LsResponse { pub nodes: Vec<Node>, pub total: usize }

#[derive(Debug, Serialize, Deserialize)]
pub struct TreeRequest { pub path: String, #[serde(flatten)] pub opts: TreeOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct TreeOpts {
    pub depth: Option<usize>, pub dirs_only: bool, pub full_path: bool,
    pub show_size: bool, pub sort: Option<String>, pub dirs_first: bool,
}
#[derive(Debug, Serialize, Deserialize)]
pub struct TreeResponse { pub output: String }

#[derive(Debug, Serialize, Deserialize)]
pub struct CatRequest { pub path: String, #[serde(rename = "ifNoneMatch")] pub if_none_match: Option<String> }
#[derive(Debug, Serialize, Deserialize)]
pub struct CatResponse { pub content: String, pub etag: Option<String> }

#[derive(Debug, Serialize, Deserialize)]
pub struct GrepRequest { pub pattern: String, pub path: String, #[serde(flatten)] pub opts: GrepOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct GrepOpts {
    pub case_insensitive: bool, pub invert: bool, pub whole_word: bool,
    pub whole_line: bool, pub context: Option<usize>,
    pub include: Option<String>, pub exclude: Option<String>,
}
#[derive(Debug, Serialize, Deserialize)]
pub struct GrepResponse { pub matches: Vec<Match>, pub total: usize }

#[derive(Debug, Serialize, Deserialize)]
pub struct FindRequest { pub path: String, #[serde(flatten)] pub opts: FindOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct FindOpts {
    pub name: Option<String>, pub r#type: Option<String>,
    pub max_depth: Option<usize>, pub min_depth: Option<usize>,
    pub iname: Option<String>, pub offset: Option<usize>, pub limit: Option<usize>,
}
#[derive(Debug, Serialize, Deserialize)]
pub struct FindResponse { pub nodes: Vec<Node>, pub total: usize }

#[derive(Debug, Serialize, Deserialize)]
pub struct StatRequest { pub path: String }
#[derive(Debug, Serialize, Deserialize)]
pub struct StatResponse { pub node: Node }

#[derive(Debug, Serialize, Deserialize)]
pub struct PutRequest { pub path: String, pub content: String, pub expected_hash: Option<String> }
#[derive(Debug, Serialize, Deserialize)]
pub struct PutResponse { pub hash: String }

#[derive(Debug, Serialize, Deserialize)]
pub struct DeleteRequest { pub path: String, pub expected_hash: Option<String> }
#[derive(Debug, Serialize, Deserialize)]
pub struct DeleteResponse {}

#[derive(Debug, Serialize, Deserialize)]
pub struct EditRequest {
    pub path: String, pub old: String, pub new: String,
    pub all: bool, pub expected_hash: Option<String>,
}
#[derive(Debug, Serialize, Deserialize)]
pub struct EditResponse { pub replacements: usize, pub hash: String }

#[derive(Debug, Serialize, Deserialize)]
pub struct SearchRequest { pub query: String, #[serde(flatten)] pub opts: SearchOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct SearchOpts { pub limit: Option<usize>, pub offset: Option<usize> }
#[derive(Debug, Serialize, Deserialize)]
pub struct SearchResponse { pub matches: Vec<SearchMatch>, pub total: usize }

#[derive(Debug, Serialize, Deserialize)]
pub struct SearchMatch { pub path: String, pub rank: f64, pub snippet: Option<String> }

#[derive(Debug, Serialize, Deserialize)]
pub struct GlobRequest { pub pattern: String, #[serde(flatten)] pub opts: GlobOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct GlobOpts { pub offset: Option<usize>, pub limit: Option<usize> }
#[derive(Debug, Serialize, Deserialize)]
pub struct GlobResponse { pub nodes: Vec<Node>, pub total: usize }

#[derive(Debug, Serialize, Deserialize)]
pub struct LocateRequest { pub query: String, #[serde(flatten)] pub opts: LocateOpts }
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct LocateOpts { pub limit: Option<usize>, pub offset: Option<usize> }
#[derive(Debug, Serialize, Deserialize)]
pub struct LocateResponse { pub matches: Vec<LocateMatch>, pub total: usize }

#[derive(Debug, Serialize, Deserialize)]
pub struct LocateMatch { pub path: String, pub score: f64, pub snippet: Option<String> }

#[derive(Debug, Serialize, Deserialize)]
pub struct HashRequest { pub paths: Vec<String> }
#[derive(Debug, Serialize, Deserialize)]
pub struct HashResponse { pub hashes: Vec<ContentHash> }

#[derive(Debug, Serialize, Deserialize)]
pub struct ContentHash { pub path: String, pub hash: String }
