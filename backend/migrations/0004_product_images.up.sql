-- +goose Up
-- Products carry an ordered gallery of image object keys (first = main).
-- Backfill from the legacy single main_image column, then drop it: one
-- source of truth, no drift between two columns. (Repo convention: forward
-- migrations only, no down sections.)

ALTER TABLE products ADD COLUMN images JSONB NOT NULL DEFAULT '[]';

UPDATE products
SET images = to_jsonb(ARRAY[main_image])
WHERE btrim(main_image) <> '';

ALTER TABLE products DROP COLUMN main_image;
