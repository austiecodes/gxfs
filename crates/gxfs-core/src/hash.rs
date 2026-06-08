use sha2::{Sha256, Digest};

pub fn hash_content(content: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(content.as_bytes());
    let hash = hasher.finalize();
    format!("sha256:{hash:x}")
}
