-- Expose curated docsets through the same read binding shape used by
-- document-backed repo and docs namespace adapters.
create or replace view {{.DocsetPathsView}} as
select
    ds.name as docset,
    dd.path,
    dd.doc_id,
    length(d.content)::bigint as size,
    d.updated_at as mtime
from {{.DocsetDocsTable}} dd
join {{.DocsetsTable}} ds on ds.id = dd.docset_id
join {{.DocsTable}} d on d.id = dd.doc_id;
