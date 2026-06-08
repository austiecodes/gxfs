use crate::error::Result;
use crate::types::*;

pub trait Adapter: Send + Sync {
    fn ls(&self, req: LsRequest) -> impl std::future::Future<Output = Result<LsResponse>> + Send;
    fn tree(&self, req: TreeRequest) -> impl std::future::Future<Output = Result<TreeResponse>> + Send;
    fn cat(&self, req: CatRequest) -> impl std::future::Future<Output = Result<CatResponse>> + Send;
    fn grep(&self, req: GrepRequest) -> impl std::future::Future<Output = Result<GrepResponse>> + Send;
    fn find(&self, req: FindRequest) -> impl std::future::Future<Output = Result<FindResponse>> + Send;
    fn stat(&self, req: StatRequest) -> impl std::future::Future<Output = Result<StatResponse>> + Send;
    fn put(&self, req: PutRequest) -> impl std::future::Future<Output = Result<PutResponse>> + Send;
    fn delete(&self, req: DeleteRequest) -> impl std::future::Future<Output = Result<DeleteResponse>> + Send;
    fn edit(&self, req: EditRequest) -> impl std::future::Future<Output = Result<EditResponse>> + Send;
    fn search(&self, req: SearchRequest) -> impl std::future::Future<Output = Result<SearchResponse>> + Send;
    fn glob(&self, req: GlobRequest) -> impl std::future::Future<Output = Result<GlobResponse>> + Send;
    fn locate(&self, req: LocateRequest) -> impl std::future::Future<Output = Result<LocateResponse>> + Send;
}
