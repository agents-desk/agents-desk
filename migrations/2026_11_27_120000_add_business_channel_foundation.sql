-- +goose Up
-- 为商务沟通底座补齐数据库结构：
-- 1. channels.public_code 支持网页商务链接独立短链与一键重置访问码；
-- 2. contacts.company 支持记录商务合作方所属公司或组织；
-- 3. members.title / members.bio 支持成员对外商务名片的头衔与简介。

ALTER TABLE channels
    ADD COLUMN public_code text;

UPDATE channels
SET public_code = substring(replace(id::text, '-', '') FROM 1 FOR 10)
WHERE kind = 'business_link' AND public_code IS NULL;

CREATE UNIQUE INDEX channels_public_code_unique
    ON channels (public_code)
    WHERE public_code IS NOT NULL;

COMMENT ON COLUMN channels.public_code IS '网页商务链接的公开短码，用于 /c/{code} 短链访问与重置访问码；非商务链接为空';

ALTER TABLE contacts
    ADD COLUMN company text NOT NULL DEFAULT '';

COMMENT ON COLUMN contacts.company IS '联系人所属公司或组织名称';

ALTER TABLE members
    ADD COLUMN title text NOT NULL DEFAULT '',
    ADD COLUMN bio   text NOT NULL DEFAULT '';

COMMENT ON COLUMN members.title IS '成员对外展示的头衔或职位，用于商务沟通名片';
COMMENT ON COLUMN members.bio IS '成员对外展示的简短介绍，用于商务沟通名片';

-- +goose Down
ALTER TABLE members
    DROP COLUMN IF EXISTS bio,
    DROP COLUMN IF EXISTS title;

ALTER TABLE contacts
    DROP COLUMN IF EXISTS company;

DROP INDEX IF EXISTS channels_public_code_unique;

ALTER TABLE channels
    DROP COLUMN IF EXISTS public_code;
