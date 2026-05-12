-- Full-text search: generated tsvector column on content + GIN index
alter table {{.ContentTable}} add column if not exists content_search tsvector
    generated always as (to_tsvector('english', coalesce(content, ''))) stored;

create index if not exists idx_content_search on {{.ContentTable}} using gin (content_search);
