-- Phase 1B: preserve curated sets when upgrading from collection-backed tables.
do $$
begin
    if to_regclass('{{.LegacyCollectionsRegClass}}') is not null then
        insert into {{.DocsetsTable}} (id, name, description, visibility, created_at, updated_at)
        select id, name, description, visibility, created_at, updated_at
        from {{.LegacyCollectionsTable}}
        on conflict do nothing;
    end if;

    if to_regclass('{{.LegacyCollectionDocsRegClass}}') is not null then
        insert into {{.DocsetDocsTable}} (docset_id, doc_id, path)
        select collection_id, doc_id, path
        from {{.LegacyCollectionDocsTable}}
        on conflict do nothing;
    end if;
end $$;
