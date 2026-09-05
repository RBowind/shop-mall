-- +goose Up
-- Products gain a category for the mall's category tab and search. Empty
-- string means "uncategorized"; the public categories endpoint aggregates
-- non-empty values from on-sale products. (Repo convention: forward
-- migrations only, no down sections.)

ALTER TABLE products
    ADD COLUMN category VARCHAR(32) NOT NULL DEFAULT '';
